package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/dag"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// loaded is a project's parsed configuration with every process resolved.
type loaded struct {
	file     *config.File
	envFile  string
	dotenv   map[string]string
	warnings []string
	order    []string // services in dependency order
	services map[string]*ProcessDef
	tasks    map[string]*ProcessDef
}

// loadProject reads and resolves the project's config.
func loadProject(dirs paths.Dirs, reg *Registration) (*loaded, error) {
	envFile, dotenv, err := config.ResolveDotEnv(reg.ConfigPath, reg.EnvFile)
	if err != nil {
		return nil, err
	}
	file, warnings, err := config.Load(reg.ConfigPath, config.InterpolationEnv(reg.Env, dotenv))
	if err != nil {
		return nil, err
	}
	if file.ID() != reg.ID {
		return nil, fmt.Errorf("project name changed from %q to %q; remove the project and start it again", reg.ID, file.ID())
	}
	order, err := serviceOrder(file)
	if err != nil {
		return nil, err
	}
	pd, err := dirs.Project(reg.ID)
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(reg.ConfigPath)
	l := &loaded{
		file:     file,
		envFile:  envFile,
		dotenv:   dotenv,
		warnings: warnings,
		order:    order,
		services: make(map[string]*ProcessDef, len(file.Services)),
		tasks:    make(map[string]*ProcessDef, len(file.Tasks)),
	}
	for name, svc := range file.Services {
		dir := resolveDir(baseDir, svc.WorkingDir)
		env := config.ChildEnv(reg.Env, dotenv, svc.Env)
		def := &ProcessDef{
			Project:   reg.ID,
			Kind:      "service",
			Name:      name,
			Command:   svc.Command,
			Shell:     svc.Shell,
			Dir:       dir,
			Env:       env,
			EnvKeys:   envKeys(dotenv, svc.Env),
			TTY:       svc.TTY,
			Deps:      deps(svc.DependsOn),
			Restart:   svc.Restart,
			Health:    svc.Healthcheck,
			ProcDir:   pd.Proc("service", name),
			Socket:    dirs.RunnerSocket(reg.ID, "service", name),
			StopGrace: defaultStopGrace,
		}
		if svc.StopGracePeriod > 0 {
			def.StopGrace = svc.StopGracePeriod
		}
		if svc.Ports != nil {
			def.Ports = svc.Ports.Entries
		}
		if svc.Proxy != nil {
			def.ProxyHost = svc.Proxy.Host
		}
		if svc.Build != nil {
			b := svc.Build.Spec
			def.Build = &runner.BuildSpec{
				Command: b.Command,
				Shell:   b.Shell,
				Dir:     resolveDir(baseDir, b.WorkingDir),
				Env:     config.ChildEnv(reg.Env, dotenv, b.Env),
			}
		}
		def.RuntimeHash = runtimeHash(def)
		l.services[name] = def
	}
	for name, task := range file.Tasks {
		spec := task.Spec
		env := config.ChildEnv(reg.Env, dotenv, spec.Env)
		def := &ProcessDef{
			Project: reg.ID,
			Kind:    "task",
			Name:    name,
			Command: spec.Command,
			Shell:   spec.Shell,
			Dir:     resolveDir(baseDir, spec.WorkingDir),
			Env:     env,
			EnvKeys: envKeys(dotenv, spec.Env),
			TTY:     spec.IsTTY(),
			Deps:    deps(spec.DependsOn),
			ProcDir: pd.Proc("task", name),
			Socket:  dirs.RunnerSocket(reg.ID, "task", name),
		}
		def.RuntimeHash = runtimeHash(def)
		l.tasks[name] = def
	}
	return l, nil
}

func resolveDir(base, dir string) string {
	if dir == "" {
		return base
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(base, dir)
}

func deps(d config.DependsOn) []Dep {
	names := append([]string(nil), d.Order...)
	sort.Strings(names)
	out := make([]Dep, 0, len(names))
	for _, n := range names {
		out = append(out, Dep{Name: n, Condition: d.Entries[n].Condition})
	}
	return out
}

// envKeys lists the variable names the project itself defines (env file and
// the process's own env), not the whole launch environment.
func envKeys(layers ...map[string]string) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, m := range layers {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// runtimeHash identifies the parts of a definition that require restarting
// the process when they change.
func runtimeHash(d *ProcessDef) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%v\x00", d.Command, d.Shell, d.Dir, d.TTY)
	for _, kv := range d.Env {
		_, _ = fmt.Fprintf(h, "%s\x00", kv)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func serviceOrder(file *config.File) ([]string, error) {
	g := make(map[string][]string, len(file.Services))
	for name, svc := range file.Services {
		g[name] = append([]string(nil), svc.DependsOn.Order...)
	}
	graph, err := dag.New(g)
	if err != nil {
		return nil, err
	}
	return graph.Order()
}

// closure returns names plus their transitive service dependencies, in
// dependency order.
func (l *loaded) closure(names []string) ([]string, error) {
	want := make(map[string]bool)
	var visit func(string) error
	visit = func(n string) error {
		def, ok := l.services[n]
		if !ok {
			return fmt.Errorf("service %q: %w", n, ErrNotFound)
		}
		if want[n] {
			return nil
		}
		want[n] = true
		for _, d := range def.Deps {
			if err := visit(d.Name); err != nil {
				return err
			}
		}
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	var out []string
	for _, n := range l.order {
		if want[n] {
			out = append(out, n)
		}
	}
	return out, nil
}
