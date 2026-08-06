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
	"sort"
	"strings"
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
// supervisor's run loops have all exited. Stopped projects remain in the
// daemon map so list commands stay complete; only remove/rm deletes the entry.
// After StopProject the supervisor is closed but retained so `ps` can still
// report per-service status until the next StartProject recreates it.
type Project struct {
	Name          string
	ConfigPath    string
	BaseDir       string
	TotalServices int
	Sup           *supervisor.Supervisor
	cancel        context.CancelFunc
	done          chan struct{}
	stopping      bool // true while StopProject is in progress
}

// Status returns "running", "stopping", or "stopped".
func (p *Project) Status() string {
	if p.Sup == nil {
		// Registered-but-never-started (autostart skip) or cleared entry.
		return "stopped"
	}
	if p.stopping {
		return "stopping"
	}
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
// builds are run before starting services. envFile is the absolute path to an
// env file to layer under service env (empty falls back to .env next to the
// config file). Explicit `up`/`start` clears per-service unless-stopped markers
// so previously stopped services are started. If the project is already
// running, stopped services are resumed and running ones are left alone.
func (d *Daemon) StartProject(configPath string, build bool, envFile string) error {
	return d.startProject(configPath, build, envFile, true)
}

// startProject is the shared implementation for StartProject and Autostart.
// When clearServiceMarkers is true (user-initiated up/start), per-service
// unless-stopped markers are removed so Stop then Up starts everything.
// Autostart passes false so an explicit stop continues to suppress resume
// across daemon restarts.
func (d *Daemon) startProject(configPath string, build bool, envFile string, clearServiceMarkers bool) error {
	cfg, err := loadConfig(configPath, envFile)
	if err != nil {
		return err
	}

	name := cfg.Name

	for {
		d.mu.Lock()
		p, exists := d.projects[name]
		if !exists {
			d.mu.Unlock()
			return d.createAndStartProject(cfg, configPath, build, clearServiceMarkers, nil)
		}
		switch p.Status() {
		case "stopping":
			done := p.done
			d.mu.Unlock()
			<-done
			continue
		case "running":
			sup := p.Sup
			d.mu.Unlock()
			if !clearServiceMarkers {
				return fmt.Errorf("project %q is already running", name)
			}
			if sup == nil {
				return fmt.Errorf("project %q is running but has no supervisor", name)
			}
			sup.UpdateFile(cfg.File)
			if build {
				if err := runBuilds(cfg); err != nil {
					return err
				}
			}
			// Clear project marker on resume so a later daemon restart may
			// autostart again (user explicitly asked to run the project).
			if locs, locErr := project.Resolve(name); locErr == nil {
				_ = removeProjectStoppedMarker(locs)
			}
			return sup.StartStopped()
		default: // stopped
			old := p
			d.mu.Unlock()
			return d.createAndStartProject(cfg, configPath, build, clearServiceMarkers, old)
		}
	}
}

// createAndStartProject builds a new supervisor and installs it in the map.
// old, if non-nil, is a stopped project entry whose supervisor (if any) is
// closed before replacement. The project remains listed under the same name.
func (d *Daemon) createAndStartProject(cfg *loadedConfig, configPath string, build, clearServiceMarkers bool, old *Project) error {
	name := cfg.Name

	if old != nil && old.Sup != nil {
		_ = old.Sup.Close()
		if old.cancel != nil {
			old.cancel()
		}
	}

	locs, err := project.Resolve(name)
	if err != nil {
		return err
	}
	if err := locs.MkdirAll(); err != nil {
		return err
	}

	// Remove the project-level stopped marker so autostart will resume this
	// project on the next daemon start.
	_ = removeProjectStoppedMarker(locs)

	if clearServiceMarkers {
		clearServiceStoppedMarkers(locs, cfg.File.Services)
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
		Env:        config.BaseEnv(cfg.DotEnv),
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
		Name:          name,
		ConfigPath:    configPath,
		BaseDir:       cfg.BaseDir,
		TotalServices: len(cfg.File.Services),
		Sup:           sup,
		cancel:        cancel,
		done:          make(chan struct{}),
	}

	d.mu.Lock()
	if existing, ok := d.projects[name]; ok && existing.Status() == "running" {
		d.mu.Unlock()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
		_ = sup.Stop(stopCtx)
		stopCancel()
		_ = sup.Close()
		cancel()
		if !clearServiceMarkers {
			return fmt.Errorf("project %q is already running", name)
		}
		return existing.Sup.StartStopped()
	}
	d.projects[name] = p
	d.mu.Unlock()

	// Write config path for autostart discovery.
	_ = writeConfigPath(locs, configPath)

	go func() {
		sup.Wait()
		close(p.done)
	}()

	return nil
}

// StopProject stops the named project's services, writes a project-level
// stopped marker (so autostart won't resume it), and closes its supervisor.
// The project remains in the daemon's map with Status "stopped"; the closed
// supervisor is retained so `ps` can still report service states.
func (d *Daemon) StopProject(name string) error {
	d.mu.Lock()
	p, ok := d.projects[name]
	if !ok {
		d.mu.Unlock()
		return fmt.Errorf("project %q is not running", name)
	}
	switch p.Status() {
	case "stopped":
		d.mu.Unlock()
		return nil
	case "stopping":
		done := p.done
		d.mu.Unlock()
		<-done
		return nil
	}
	p.stopping = true
	sup := p.Sup
	cancelFn := p.cancel
	done := p.done
	d.mu.Unlock()

	locs, err := project.Resolve(name)
	if err == nil {
		_ = writeProjectStoppedMarker(locs)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
	defer cancel()
	if sup != nil {
		if err := sup.Stop(ctx); err != nil {
			d.mu.Lock()
			p.stopping = false
			d.mu.Unlock()
			return err
		}
	}
	<-done
	if sup != nil {
		_ = sup.Close()
	}
	if cancelFn != nil {
		cancelFn()
	}

	d.mu.Lock()
	p.stopping = false
	d.mu.Unlock()

	return nil
}

// RemoveProject stops the named project's services if running, removes the project
// from the daemon's map, and deletes its runtime and state directories.
func (d *Daemon) RemoveProject(name string) error {
	d.mu.Lock()
	p, exists := d.projects[name]
	d.mu.Unlock()

	if exists {
		status := p.Status()
		if status == "running" || status == "stopping" {
			if status == "running" {
				locs, err := project.Resolve(name)
				if err == nil {
					_ = writeProjectStoppedMarker(locs)
				}
				d.mu.Lock()
				p.stopping = true
				sup := p.Sup
				cancelFn := p.cancel
				done := p.done
				d.mu.Unlock()

				ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
				if sup != nil {
					if err := sup.Stop(ctx); err != nil {
						cancel()
						d.mu.Lock()
						p.stopping = false
						d.mu.Unlock()
						return err
					}
				}
				cancel()
				<-done
				if sup != nil {
					_ = sup.Close()
				}
				if cancelFn != nil {
					cancelFn()
				}
			} else {
				<-p.done
			}
		}

		d.mu.Lock()
		delete(d.projects, name)
		d.mu.Unlock()
	}

	locs, err := project.Resolve(name)
	if err != nil {
		return err
	}

	// Delete runtime and state directories to completely remove it.
	_ = os.RemoveAll(locs.Runtime)
	_ = os.RemoveAll(locs.State)

	return nil
}

// StopDaemon stops all projects and signals the daemon process to exit.
// Projects remain in the map as stopped; no project .stopped marker is written
// so unless-stopped autostart can resume after a daemon restart.
func (d *Daemon) StopDaemon() error {
	d.stopOnce.Do(func() { close(d.stopCh) })

	d.mu.Lock()
	projects := make([]*Project, 0, len(d.projects))
	for _, p := range d.projects {
		projects = append(projects, p)
	}
	d.mu.Unlock()

	for _, p := range projects {
		d.mu.Lock()
		if p.Sup == nil || p.Status() == "stopped" {
			d.mu.Unlock()
			continue
		}
		if p.Status() == "stopping" {
			done := p.done
			d.mu.Unlock()
			<-done
			continue
		}
		p.stopping = true
		sup := p.Sup
		cancelFn := p.cancel
		done := p.done
		d.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
		_ = sup.Stop(ctx)
		cancel()
		<-done
		_ = sup.Close()
		if cancelFn != nil {
			cancelFn()
		}

		d.mu.Lock()
		p.stopping = false
		d.mu.Unlock()
	}

	return nil
}

// ListProjects returns a snapshot of all known projects (both running and stopped)
// and their statuses, service counts, and auto-cleans any stale projects whose
// config files no longer exist on disk.
func (d *Daemon) ListProjects() []protocol.ProjectInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]protocol.ProjectInfo, 0, len(d.projects))
	var stale []string

	for name, p := range d.projects {
		if _, err := os.Stat(p.ConfigPath); err != nil {
			stale = append(stale, name)
			continue
		}

		info := protocol.ProjectInfo{
			Name:          name,
			Status:        p.Status(),
			ConfigPath:    p.ConfigPath,
			TotalServices: p.TotalServices,
		}

		if p.Sup != nil && info.Status == "running" {
			states := p.Sup.States()
			if len(states) > 0 {
				info.TotalServices = len(states)
			}
			for _, st := range states {
				if st.Status == supervisor.StatusRunning || st.Status == supervisor.StatusStarting || st.Status == supervisor.StatusBackoff {
					info.RunningServices++
				}
			}
		}

		out = append(out, info)
	}

	for _, name := range stale {
		delete(d.projects, name)
		locs, err := project.Resolve(name)
		if err == nil {
			_ = os.RemoveAll(locs.Runtime)
			_ = os.RemoveAll(locs.State)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

	return out
}

// ProjectBackend returns the control.Backend for the named project, or an
// error if the project is not known or has no supervisor. A stopped project's
// closed supervisor is still returned so `ps` can report service states;
// mutating calls (Restart, etc.) fail because the supervisor is stopped.
func (d *Daemon) ProjectBackend(project string) (control.Backend, error) {
	d.mu.Lock()
	p, ok := d.projects[project]
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	if p.Sup == nil {
		return nil, fmt.Errorf("project %q is stopped", project)
	}
	return supervisor.NewControlBackend(p.Sup), nil
}

// loadedConfig bundles everything needed to create a Supervisor.
type loadedConfig struct {
	File    *config.File
	Name    string
	BaseDir string
	Order   []string
	EnvFile string
	DotEnv  map[string]string
}

func loadConfig(configPath, envFile string) (*loadedConfig, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	envFile, dotenv, err := config.ResolveDotEnv(abs, envFile)
	if err != nil {
		return nil, err
	}
	file, err := config.LoadWithEnv(abs, dotenv)
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
		EnvFile: envFile,
		DotEnv:  dotenv,
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
		// Build execution runs inline in the daemon.
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
	// Run the build command; output goes to the daemon's stderr (daemon log).
	return runBuildCommand(shell, spec.Command, dir, config.BaseEnv(cfg.DotEnv), spec.Env)
}

// writeConfigPath persists the config path in the project's state dir so the
// daemon can discover known projects for autostart.
func writeConfigPath(locs *project.Locations, configPath string) error {
	path := filepath.Join(locs.State, "config-path")
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	return os.WriteFile(path, []byte(abs+"\n"), 0o644)
}

// writeProjectStoppedMarker writes a project-level ".stopped" marker file so
// the daemon knows not to autostart this project (unless the user explicitly
// starts it again, which removes the marker).
func writeProjectStoppedMarker(locs *project.Locations) error {
	path := filepath.Join(locs.State, ".stopped")
	return os.WriteFile(path, []byte("stopped\n"), 0o644)
}

// removeProjectStoppedMarker removes the project-level ".stopped" marker file
// so the project will autostart on the next daemon start.
func removeProjectStoppedMarker(locs *project.Locations) error {
	path := filepath.Join(locs.State, ".stopped")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// clearServiceStoppedMarkers removes per-service unless-stopped markers so an
// explicit `up`/`start` launches services that were previously stopped.
func clearServiceStoppedMarkers(locs *project.Locations, services map[string]config.Service) {
	for name := range services {
		path := filepath.Join(locs.State, name+".stopped")
		_ = os.Remove(path)
	}
}

// HasProjectStoppedMarker reports whether the named project has a project-level
// ".stopped" marker in its state dir.
func HasProjectStoppedMarker(name string) bool {
	locs, err := project.Resolve(name)
	if err != nil {
		return false
	}
	return hasProjectStoppedMarker(locs)
}

// RemoveProjectStoppedMarker removes the project-level ".stopped" marker for
// the named project.
func RemoveProjectStoppedMarker(name string) error {
	locs, err := project.Resolve(name)
	if err != nil {
		return err
	}
	return removeProjectStoppedMarker(locs)
}

// hasProjectStoppedMarker reports whether the project has a ".stopped" marker.
func hasProjectStoppedMarker(locs *project.Locations) bool {
	path := filepath.Join(locs.State, ".stopped")
	_, err := os.Stat(path)
	return err == nil
}

// readConfigPath reads the config path stored in the project's state dir.
func readConfigPath(locs *project.Locations) (string, bool) {
	path := filepath.Join(locs.State, "config-path")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := string(data)
	return strings.TrimSpace(s), true
}

// Autostart scans the state dir for known projects and starts supervisors for
// those that have services with restart: always or restart: unless-stopped
// (unless a project-level .stopped marker exists). It is called by the daemon
// child on startup. Returns the number of projects started and the number
// skipped.
func (d *Daemon) Autostart() (started, skipped int, err error) {
	stateBase, err := stateBaseDir()
	if err != nil {
		return 0, 0, err
	}
	appDir := filepath.Join(stateBase, project.AppDir)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("autostart: read state dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		locs, err := project.Resolve(name)
		if err != nil {
			continue
		}
		configPath, ok := readConfigPath(locs)
		if !ok {
			continue
		}
		if _, err := os.Stat(configPath); err != nil {
			continue
		}

		cfg, err := loadConfig(configPath, "")
		if err != nil {
			continue
		}

		policy := autostartPolicy(cfg.File)
		shouldAutostart := (policy == config.RestartAlways || policy == config.RestartUnlessStopped) &&
			!hasProjectStoppedMarker(locs)

		if !shouldAutostart {
			// Register stopped project in memory so d.projects owns all registered projects.
			d.mu.Lock()
			if _, exists := d.projects[name]; !exists {
				d.projects[name] = &Project{
					Name:          name,
					ConfigPath:    configPath,
					BaseDir:       cfg.BaseDir,
					TotalServices: len(cfg.File.Services),
					done:          closedChan(),
				}
			}
			d.mu.Unlock()
			skipped++
			continue
		}

		// Do not clear per-service stopped markers: an explicit `stop` should
		// still suppress resume across daemon restarts for unless-stopped.
		if err := d.startProject(configPath, false, "", false); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: autostart: project %q: %v\n", name, err)
			skipped++
			continue
		}
		started++
	}
	return started, skipped, nil
}

// autostartPolicy returns the restart policy that determines whether a
// project is eligible for autostart. If any service has restart: always or
// restart: unless-stopped, the project may autostart (subject to the project
// .stopped marker). Otherwise the project does not autostart.
func autostartPolicy(file *config.File) config.RestartPolicy {
	hasAlways := false
	hasUnlessStopped := false
	for _, svc := range file.Services {
		switch svc.Restart {
		case config.RestartAlways:
			hasAlways = true
		case config.RestartUnlessStopped:
			hasUnlessStopped = true
		}
	}
	if hasAlways {
		return config.RestartAlways
	}
	if hasUnlessStopped {
		return config.RestartUnlessStopped
	}
	return config.RestartNo
}

// stateBaseDir returns the XDG state base directory (without the app dir
// segment).
func stateBaseDir() (string, error) {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return s, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine state dir: %w", err)
	}
	return filepath.Join(h, project.DefaultStateBase), nil
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
