package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/dag"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// loaded is a project's parsed configuration with every process resolved.
type loaded struct {
	project  *config.Project
	warnings []string
	order    []string // services in dependency order
	// autostart holds the services a project start launches: those with
	// autostart and their dependencies.
	autostart map[string]bool
	services  map[string]*ProcessDef
	tasks     map[string]*ProcessDef
}

// loadProject reads and resolves the project's config. It allocates and
// persists ports for `auto` ports, so it must only run on the project's
// actor.
func loadProject(dirs paths.Dirs, reg *Registration) (*loaded, error) {
	loadConfig := config.Load
	if !reg.Configured {
		loadConfig = config.LoadAllowMissing
	}
	proj, err := loadConfig(reg.ConfigPath, reg.Env)
	if err != nil {
		return nil, err
	}
	if !proj.Missing {
		reg.Configured = true
	}
	if proj.ID != reg.ID {
		return nil, fmt.Errorf("project name changed from %q to %q; remove the project and start it again", reg.ID, proj.ID)
	}
	order, err := serviceOrder(proj.File)
	if err != nil {
		return nil, err
	}
	pd, err := dirs.Project(reg.ID)
	if err != nil {
		return nil, err
	}
	assigned, err := config.ReadPortAssignments(pd.Ports())
	if err != nil {
		return nil, err
	}
	res, err := proj.Resolve(reg.Env, assigned, nil)
	if err != nil {
		return nil, err
	}
	if !maps.Equal(assigned, res.Ports) {
		if err := res.Ports.Save(pd.Ports()); err != nil {
			return nil, err
		}
	}
	file := proj.File
	l := &loaded{
		project:   proj,
		warnings:  proj.Warnings,
		order:     order,
		autostart: map[string]bool{},
		services:  make(map[string]*ProcessDef, len(file.Services)),
		tasks:     make(map[string]*ProcessDef, len(file.Tasks)),
	}
	position := make(map[string]int, len(order))
	for i, name := range order {
		position[name] = i
	}
	for name, svc := range file.Services {
		rs := res.Services[name]
		if svc.Stop.Signal != "" {
			if _, err := runner.ParseSignal(svc.Stop.Signal); err != nil {
				return nil, fmt.Errorf("service %q: stop.signal: %w", name, err)
			}
		}
		def := &ProcessDef{
			Project:    reg.ID,
			Kind:       "service",
			Name:       name,
			Order:      position[name],
			Cmd:        rs.Cmd,
			Dir:        rs.Dir,
			Env:        rs.Env,
			EnvKeys:    rs.EnvKeys,
			TTY:        svc.TTY,
			Deps:       sortedCopy(svc.DependsOn),
			Restart:    svc.Restart,
			Ready:      rs.Ready,
			Ports:      rs.Ports,
			ProxyHost:  svc.Host,
			StopSignal: svc.Stop.Signal,
			StopGrace:  svc.Stop.Timeout,
			Autostart:  svc.Starts(),
			ProcDir:    pd.Proc("service", name),
			Socket:     dirs.RunnerSocket(reg.ID, "service", name),
		}
		if b := rs.Build; b != nil {
			def.Build = &runner.BuildSpec{
				Argv:    b.Cmd.Args(),
				Display: b.Cmd.String(),
				Dir:     b.Dir,
				Env:     b.Env,
			}
			def.BuildSources = b.Sources
		}
		def.RuntimeHash = runtimeHash(def)
		l.services[name] = def
	}
	var starts []string
	for _, name := range order {
		if l.services[name].Autostart {
			starts = append(starts, name)
		}
	}
	closure, err := l.closure(starts)
	if err != nil {
		return nil, err
	}
	for _, name := range closure {
		l.autostart[name] = true
	}
	for name, task := range file.Tasks {
		rt := res.Tasks[name]
		def := &ProcessDef{
			Project: reg.ID,
			Kind:    "task",
			Name:    name,
			Cmd:     rt.Cmd,
			Dir:     rt.Dir,
			Env:     rt.Env,
			EnvKeys: rt.EnvKeys,
			TTY:     task.IsTTY(),
			Deps:    sortedCopy(task.DependsOn),
			ProcDir: pd.Proc("task", name),
			Socket:  dirs.RunnerSocket(reg.ID, "task", name),
		}
		def.RuntimeHash = runtimeHash(def)
		l.tasks[name] = def
	}
	return l, nil
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// runtimeHash identifies the parts of a definition that require restarting
// the process when they change.
func runtimeHash(d *ProcessDef) string {
	h := sha256.New()
	for _, a := range d.Cmd.Args() {
		_, _ = fmt.Fprintf(h, "%s\x00", a)
	}
	_, _ = fmt.Fprintf(h, "%s\x00%v\x00%s\x00", d.Dir, d.TTY, d.StopSignal)
	for _, kv := range d.Env {
		_, _ = fmt.Fprintf(h, "%s\x00", kv)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func serviceOrder(file *config.File) ([]string, error) {
	g := make(map[string][]string, len(file.Services))
	for name, svc := range file.Services {
		g[name] = append([]string(nil), svc.DependsOn...)
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
			if err := visit(d); err != nil {
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

// runSocket derives a run's own runner socket from the process's base
// socket path. Distinct paths per run mean a finishing runner can never
// clobber the socket of the run that replaced it.
func runSocket(base string, run int64) string {
	return strings.TrimSuffix(base, ".sock") + "-" + strconv.FormatInt(run, 10) + ".sock"
}
