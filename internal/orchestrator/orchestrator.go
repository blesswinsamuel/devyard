// Package orchestrator manages multiple projects within a single global daemon
// process. It owns a map of project supervisors and implements control.MultiBackend
// so the control server can route per-project requests and daemon-level commands
// (list_projects, start_project, stop_project, stop_daemon) through one socket.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/dag"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
)

// Project is one project managed by the daemon. It holds the supervisor, the
// config path it was started from, and a done channel that is closed when the
// supervisor's run loops have all exited.
type Project struct {
	Name       string
	ConfigPath string
	BaseDir    string
	Sup        *supervisor.Supervisor
	cancel     context.CancelFunc
	done       chan struct{}
}

// Status returns "running" if the supervisor is still active, "stopped" once
// all service run loops have exited.
func (p *Project) Status() string {
	select {
	case <-p.done:
		return "stopped"
	default:
		return "running"
	}
}

// Daemon owns multiple project supervisors and implements control.MultiBackend.
// It is the core of the global daemon process.
type Daemon struct {
	mu       sync.Mutex
	projects map[string]*Project

	stopCh   chan struct{}
	stopOnce sync.Once
	exited   chan struct{}
}

// New creates a Daemon with no projects. Call StartProject to add projects.
func New() *Daemon {
	return &Daemon{
		projects: make(map[string]*Project),
		stopCh:   make(chan struct{}),
		exited:   make(chan struct{}),
	}
}

// StopCh returns a channel that is closed when StopDaemon is called. The daemon
// process should exit when this channel closes.
func (d *Daemon) StopCh() <-chan struct{} { return d.stopCh }

// StartProject loads the config at configPath, creates a Supervisor for it,
// starts it, and adds it to the daemon's map. If build is true, pre-start
// builds are run before starting services. If a project with the same name is
// already running, it returns an error.
func (d *Daemon) StartProject(configPath string, build bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	name := cfg.Name

	d.mu.Lock()
	if _, exists := d.projects[name]; exists {
		d.mu.Unlock()
		return fmt.Errorf("project %q is already running; use stop_project first", name)
	}
	d.mu.Unlock()

	locs, err := project.Resolve(name)
	if err != nil {
		return err
	}
	if err := locs.MkdirAll(); err != nil {
		return err
	}

	if build {
		if err := runBuilds(cfg); err != nil {
			return err
		}
	}

	sup, err := supervisor.New(supervisor.Options{
		Locations:  locs,
		File:       cfg.File,
		Order:      cfg.Order,
		BaseDir:    cfg.BaseDir,
		Foreground: false,
	})
	if err != nil {
		return fmt.Errorf("supervisor: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := sup.Start(ctx); err != nil {
		cancel()
		_ = sup.Close()
		return fmt.Errorf("supervisor start: %w", err)
	}

	p := &Project{
		Name:       name,
		ConfigPath: configPath,
		BaseDir:    cfg.BaseDir,
		Sup:        sup,
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	d.mu.Lock()
	d.projects[name] = p
	d.mu.Unlock()

	// Write config path for autostart discovery (phase 5).
	_ = writeConfigPath(locs, configPath)

	go func() {
		sup.Wait()
		close(p.done)
	}()

	return nil
}

// StopProject stops the named project's services, closes its supervisor, and
// removes it from the daemon's map.
func (d *Daemon) StopProject(name string) error {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q is not running", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
	defer cancel()
	if err := p.Sup.Stop(ctx); err != nil {
		return err
	}
	<-p.done
	_ = p.Sup.Close()
	p.cancel()

	d.mu.Lock()
	delete(d.projects, name)
	d.mu.Unlock()
	return nil
}

// StopDaemon stops all projects and signals the daemon process to exit.
func (d *Daemon) StopDaemon() error {
	d.stopOnce.Do(func() { close(d.stopCh) })

	d.mu.Lock()
	projects := make([]*Project, 0, len(d.projects))
	for _, p := range d.projects {
		projects = append(projects, p)
	}
	d.mu.Unlock()

	for _, p := range projects {
		ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
		_ = p.Sup.Stop(ctx)
		cancel()
		<-p.done
		_ = p.Sup.Close()
		p.cancel()
	}

	d.mu.Lock()
	d.projects = make(map[string]*Project)
	d.mu.Unlock()
	return nil
}

// ListProjects returns a snapshot of all known projects and their statuses.
func (d *Daemon) ListProjects() []protocol.ProjectInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]protocol.ProjectInfo, 0, len(d.projects))
	for name, p := range d.projects {
		out = append(out, protocol.ProjectInfo{
			Name:       name,
			Status:     p.Status(),
			ConfigPath: p.ConfigPath,
		})
	}
	return out
}

// ProjectBackend returns the control.Backend for the named project, or an
// error if the project is not known.
func (d *Daemon) ProjectBackend(project string) (control.Backend, error) {
	d.mu.Lock()
	p, ok := d.projects[project]
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	return supervisor.NewControlBackend(p.Sup), nil
}

// loadedConfig bundles everything needed to create a Supervisor.
type loadedConfig struct {
	File    *config.File
	Name    string
	BaseDir string
	Order   []string
}

func loadConfig(configPath string) (*loadedConfig, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	file, err := config.Load(abs)
	if err != nil {
		return nil, err
	}

	deps := make(map[string][]string, len(file.Services))
	for name, svc := range file.Services {
		deps[name] = append([]string{}, svc.DependsOn.Order...)
	}
	g, err := dag.New(deps)
	if err != nil {
		return nil, err
	}
	order, err := g.Order()
	if err != nil {
		return nil, err
	}

	return &loadedConfig{
		File:    file,
		Name:    file.Name,
		BaseDir: filepath.Dir(abs),
		Order:   order,
	}, nil
}

func runBuilds(cfg *loadedConfig) error {
	for _, name := range cfg.Order {
		svc, ok := cfg.File.Services[name]
		if !ok {
			return fmt.Errorf("build: unknown service %q", name)
		}
		if svc.Build == nil {
			continue
		}
		// Build execution is handled by the CLI in Phase 2; for now builds
		// run inline in the daemon.
		if err := runOneBuild(cfg, name, svc.Build.Spec); err != nil {
			return fmt.Errorf("build %q: %w", name, err)
		}
	}
	return nil
}

func runOneBuild(cfg *loadedConfig, name string, spec config.BuildSpec) error {
	shell := spec.Shell
	if shell == "" {
		shell = config.DefaultShell
	}
	dir := spec.WorkingDir
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.BaseDir, dir)
	}
	// Use the build command from the CLI's build.go logic.
	return runBuildCommand(shell, spec.Command, dir, spec.Env)
}

// writeConfigPath persists the config path in the project's state dir so the
// daemon can discover known projects for autostart (phase 5).
func writeConfigPath(locs *project.Locations, configPath string) error {
	path := filepath.Join(locs.State, "config-path")
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	return os.WriteFile(path, []byte(abs+"\n"), 0o644)
}
