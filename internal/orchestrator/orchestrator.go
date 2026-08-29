// Package orchestrator manages multiple projects within a single global daemon
// process. It owns a map of project supervisors and implements control.MultiBackend
// so the control server can route per-project requests and daemon-level commands
// (list_projects, start_project, stop_project, stop_daemon) through one socket.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/dag"
	"github.com/blesswinsamuel/local-compose/internal/gitlog"
	"github.com/blesswinsamuel/local-compose/internal/gitwatcher"
	"github.com/blesswinsamuel/local-compose/internal/procstat"
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

	// File and Order hold the parsed config for a registered-but-never-started
	// project (Sup == nil, e.g. skipped by autostart). The stoppedBackend uses
	// them to report the project's services and actions as stopped.
	File  *config.File
	Order []string
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
	mu        sync.Mutex
	projects  map[string]*Project
	startTime time.Time
	pid       int

	stopCh   chan struct{}
	stopOnce sync.Once
	exited   chan struct{}

	onStateChange       func(project string, state *protocol.ServiceState)
	onActionStateChange func(project string, state *protocol.ActionState)
	onGitChange         func(project string)

	watcher *gitwatcher.RepoWatcher
}

// New creates a Daemon with no projects. Call StartProject to add projects.
func New() *Daemon {
	d := &Daemon{
		projects:  make(map[string]*Project),
		stopCh:    make(chan struct{}),
		exited:    make(chan struct{}),
		startTime: time.Now(),
		pid:       os.Getpid(),
	}
	w, err := gitwatcher.New(func(project string) {
		d.mu.Lock()
		fn := d.onGitChange
		d.mu.Unlock()
		if fn != nil {
			fn(project)
		}
	})
	if err == nil {
		d.watcher = w
	}
	return d
}

// StopCh returns a channel that is closed when StopDaemon is called. The daemon
// process should exit when this channel closes.
func (d *Daemon) StopCh() <-chan struct{} { return d.stopCh }

// SetOnStateChange registers a callback that fires whenever any supervised
// service changes state. The callback is invoked from the service's run-loop
// goroutine, NOT under the daemon lock.
func (d *Daemon) SetOnStateChange(fn func(project string, state *protocol.ServiceState)) {
	d.mu.Lock()
	d.onStateChange = fn
	d.mu.Unlock()
}

// SetOnActionStateChange registers a callback that fires whenever any action's
// runtime state changes (started, completed). Same semantics as SetOnStateChange.
func (d *Daemon) SetOnActionStateChange(fn func(project string, state *protocol.ActionState)) {
	d.mu.Lock()
	d.onActionStateChange = fn
	d.mu.Unlock()
}

// SetOnGitChange registers a callback that fires whenever repository files change.
func (d *Daemon) SetOnGitChange(fn func(project string)) {
	d.mu.Lock()
	d.onGitChange = fn
	d.mu.Unlock()
}

// StartProject loads the config at configPath, creates a Supervisor for it,
// starts it, and adds it to the daemon's map. If build is true, pre-start
// builds are run before starting services. envFile is the absolute path to an
// env file to layer under service env (empty falls back to .env next to the
// config file). If the project is already running, stopped services are
// resumed, orphan services are handled according to removeOrphans.
func (d *Daemon) StartProject(configPath string, build bool, envFile string, removeOrphans bool) error {
	return d.startProject(configPath, build, envFile, removeOrphans)
}

// startProject is the shared implementation for StartProject and Autostart.
func (d *Daemon) startProject(configPath string, build bool, envFile string, removeOrphans bool) error {
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
			return d.createAndStartProject(cfg, configPath, build, nil, nil)
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
			if sup == nil {
				return fmt.Errorf("project %q is running but has no supervisor", name)
			}
			if err := sup.Reconcile(cfg.File, cfg.Order, removeOrphans); err != nil {
				return err
			}
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
			p.TotalServices = len(cfg.File.Services)
			return sup.StartStopped()
		default: // stopped
			old := p
			d.mu.Unlock()
			return d.createAndStartProject(cfg, configPath, build, nil, old)
		}
	}
}

// StartService starts one service of a project by name. If the project's
// supervisor is running, the service is resumed in place (no-op when it is
// already up). If the project is stopped, a supervisor is materialized that
// starts just the requested service and its depends_on chain; the rest stay
// stopped. The project-level stopped marker is cleared so an explicit start
// re-arms autostart. Project-level state does not gate an explicit service
// start.
func (d *Daemon) StartService(projectName, serviceName string) error {
	for {
		d.mu.Lock()
		p, exists := d.projects[projectName]
		if !exists {
			d.mu.Unlock()
			return fmt.Errorf("project %q is not running", projectName)
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
			if sup == nil {
				return fmt.Errorf("project %q is running but has no supervisor", projectName)
			}
			return sup.StartService(serviceName)
		default: // stopped
			configPath := p.ConfigPath
			old := p
			d.mu.Unlock()
			cfg, err := loadConfig(configPath, "")
			if err != nil {
				return err
			}
			if _, ok := cfg.File.Services[serviceName]; !ok {
				return fmt.Errorf("supervisor: unknown service %q", serviceName)
			}
			return d.createAndStartProject(cfg, configPath, false, serviceClosure(cfg.File, serviceName), old)
		}
	}
}

// serviceClosure returns the set of service names that must run for name to
// start: name itself plus every transitive depends_on dependency.
func serviceClosure(file *config.File, name string) map[string]bool {
	selected := map[string]bool{name: true}
	var visit func(n string)
	visit = func(n string) {
		svc, ok := file.Services[n]
		if !ok {
			return
		}
		for _, dep := range svc.DependsOn.Order {
			if selected[dep] {
				continue
			}
			selected[dep] = true
			visit(dep)
		}
	}
	visit(name)
	return selected
}

// createAndStartProject builds a new supervisor and installs it in the map.
// selected, when non-empty, limits the services launched at Start to that set
// (used by lazy `start <service>`); nil means all services. old, if non-nil,
// is a stopped project entry whose supervisor (if any) is closed before
// replacement. The project remains listed under the same name.
func (d *Daemon) createAndStartProject(cfg *loadedConfig, configPath string, build bool, selected map[string]bool, old *Project) error {
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

	if build {
		if err := runBuilds(cfg); err != nil {
			return err
		}
	}

	selectedNames := make([]string, 0, len(selected))
	if len(selected) > 0 {
		for _, n := range cfg.Order {
			if selected[n] {
				selectedNames = append(selectedNames, n)
			}
		}
	}

	sup, err := supervisor.New(supervisor.Options{
		Locations:  locs,
		File:       cfg.File,
		Order:      cfg.Order,
		BaseDir:    cfg.BaseDir,
		Foreground: false,
		Env:        config.BaseEnv(cfg.DotEnv),
		Selected:   selectedNames,
		OnStateChange: func(svc string, state *protocol.ServiceState) {
			d.mu.Lock()
			fn := d.onStateChange
			d.mu.Unlock()
			if fn != nil {
				fn(name, state)
			}
		},
		OnActionStateChange: func(action string, state *protocol.ActionState) {
			d.mu.Lock()
			fn := d.onActionStateChange
			d.mu.Unlock()
			if fn != nil {
				fn(name, state)
			}
		},
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
		return existing.Sup.StartStopped()
	}
	d.projects[name] = p
	if d.watcher != nil && p.BaseDir != "" {
		_ = d.watcher.AddProject(name, p.BaseDir)
	}
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
		if d.watcher != nil {
			d.watcher.RemoveProject(name)
		}
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
// so autostart can resume them after a daemon restart.
func (d *Daemon) StopDaemon() error {
	defer d.stopOnce.Do(func() { close(d.stopCh) })

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

	if d.watcher != nil {
		_ = d.watcher.Close()
	}

	return nil
}

// DaemonStatus returns metrics and status information about the global daemon process.
func (d *Daemon) DaemonStatus() (*protocol.DaemonInfo, error) {
	d.mu.Lock()
	pid := d.pid
	startTime := d.startTime
	d.mu.Unlock()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	var rss uint64
	if s, ok := procstat.SampleGroup(pid); ok {
		rss = s.RSS
	}

	return &protocol.DaemonInfo{
		Pid:         int32(pid),
		StartTime:   protocol.TimeToProto(startTime),
		Goroutines:  int32(runtime.NumGoroutine()),
		MemoryAlloc: mem.Alloc,
		MemorySys:   mem.Sys,
		MemoryRss:   rss,
		GoVersion:   runtime.Version(),
	}, nil
}

// StopDaemonKeepServices flushes state to disk and signals the daemon process to exit
// without stopping the supervised project services, allowing a replacement daemon
// to adopt them.
func (d *Daemon) StopDaemonKeepServices() error {
	defer d.stopOnce.Do(func() { close(d.stopCh) })

	d.mu.Lock()
	projects := make([]*Project, 0, len(d.projects))
	for _, p := range d.projects {
		projects = append(projects, p)
	}
	d.mu.Unlock()

	for _, p := range projects {
		d.mu.Lock()
		if p.Sup != nil && p.Status() == "running" {
			_ = p.Sup.SaveState()
		}
		d.mu.Unlock()
	}

	if d.watcher != nil {
		_ = d.watcher.Close()
	}

	return nil
}

// RestartDaemon spawns a replacement daemon process and shuts down the running daemon.
// If restartServices is true, all running services are stopped and restarted.
// If restartServices is false, services remain running and are adopted by the new daemon.
func (d *Daemon) RestartDaemon(restartServices bool) error {
	locs, err := project.ResolveDaemon()
	if err != nil {
		return fmt.Errorf("resolve daemon locations: %w", err)
	}
	if _, err := daemon.SpawnDaemon(locs); err != nil {
		return fmt.Errorf("spawn replacement daemon: %w", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		if restartServices {
			_ = d.StopDaemon()
		} else {
			_ = d.StopDaemonKeepServices()
		}
	}()
	return nil
}

// ListProjects returns a snapshot of all known projects (both running and stopped)
// and their statuses, service counts, and auto-cleans any stale projects whose
// config files no longer exist on disk.
func (d *Daemon) ListProjects() []*protocol.ProjectInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]*protocol.ProjectInfo, 0, len(d.projects))
	var stale []string

	for name, p := range d.projects {
		if _, err := os.Stat(p.ConfigPath); err != nil {
			stale = append(stale, name)
			continue
		}

		info := &protocol.ProjectInfo{
			Name:          name,
			Status:        p.Status(),
			ConfigPath:    p.ConfigPath,
			TotalServices: int32(p.TotalServices),
		}

		if p.Sup != nil && info.Status == "running" {
			states := p.Sup.States()
			if len(states) > 0 {
				info.TotalServices = int32(len(states))
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
// error if the project is not known. A stopped project with a retained
// supervisor is returned as-is so `ps` can report its (stopped) service states;
// a registered-but-never-started project is served by a stoppedBackend that
// reports every service and action as stopped. Mutating calls on either fail
// because the project is stopped.
func (d *Daemon) ProjectBackend(project string) (control.Backend, error) {
	d.mu.Lock()
	p, ok := d.projects[project]
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	if p.Sup == nil {
		return stoppedBackend{project: project, file: p.File, order: p.Order}, nil
	}
	return supervisor.NewControlBackend(p.Sup), nil
}

// GitLog returns the git commit log for a project's working directory (the
// directory containing its config file), newest first. It errors when the
// project is unknown or its directory is not a git repository.
func (d *Daemon) GitLog(name string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return nil, nil, nil, nil, fmt.Errorf("project %q is not a git repository", name)
	}
	commits, branches, tags, stashes, err := gitlog.Log(p.BaseDir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return commits, branches, tags, stashes, nil
}

// GitDiff returns the metadata, changed file list, and patch diff for a commit in a project's working directory.
func (d *Daemon) GitDiff(name string, hash string, path string, contextLines ...int) (*protocol.GitDiffResult, error) {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return nil, fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Diff(p.BaseDir, hash, path, contextLines...)
}

// GitCommit stages all changes and creates a new commit in a project's working directory.
func (d *Daemon) GitCommit(name string, message string) error {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Commit(p.BaseDir, message)
}

// GitStage stages or unstages files in a project's working directory.
func (d *Daemon) GitStage(name string, path string, stageAll bool, unstage bool) error {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Stage(p.BaseDir, path, stageAll, unstage)
}

// GitPush pushes the current branch to its upstream remote in a project's
// working directory.
func (d *Daemon) GitPush(name string) (string, error) {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return "", fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Push(p.BaseDir, "")
}

// GitPull pulls changes from the current branch's upstream remote in a
// project's working directory.
func (d *Daemon) GitPull(name string) (string, error) {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return "", fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Pull(p.BaseDir, "")
}

// GitFetch downloads refs from the remote in a project's working directory
// without touching the working tree.
func (d *Daemon) GitFetch(name string) (string, error) {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("project %q is not running", name)
	}
	if !gitlog.IsRepo(p.BaseDir) {
		return "", fmt.Errorf("project %q is not a git repository", name)
	}
	return gitlog.Fetch(p.BaseDir, "")
}

// stoppedBackend adapts a registered-but-never-started project (Sup == nil) to
// control.Backend. Its retained config lets reads report every service as
// stopped and list the project's actions; mutations report that the project is
// stopped. Log paths resolve to the project's state dir so historical logs from
// a previous run stay viewable.
type stoppedBackend struct {
	project string
	file    *config.File
	order   []string
}

func (b stoppedBackend) err() error {
	return fmt.Errorf("project %q is stopped", b.project)
}

func (b stoppedBackend) States() []*protocol.ServiceState {
	out := make([]*protocol.ServiceState, 0, len(b.order))
	for _, name := range b.order {
		st := &protocol.ServiceState{
			Name:   name,
			Status: string(supervisor.StatusStopped),
			Health: "n/a",
		}
		if svc, ok := b.file.Services[name]; ok {
			st.HasHealth = svc.Healthcheck != nil
		}
		out = append(out, st)
	}
	return out
}

func (b stoppedBackend) Stop(context.Context) error    { return b.err() }
func (b stoppedBackend) StopService(string) error      { return b.err() }
func (b stoppedBackend) StartService(string) error     { return b.err() }
func (b stoppedBackend) KillService(_, _ string) error { return b.err() }
func (b stoppedBackend) Restart(string) error          { return b.err() }
func (b stoppedBackend) Top(string) ([]*protocol.ServiceStat, error) {
	return nil, b.err()
}
func (b stoppedBackend) Ports() ([]*protocol.PortBinding, error) {
	return nil, nil
}
func (b stoppedBackend) ListActions() []*protocol.ActionState {
	return supervisor.ListActionsFromFile(b.file)
}
func (b stoppedBackend) RunAction(context.Context, string, []string, io.Writer) (int, error) {
	return 1, b.err()
}

func (b stoppedBackend) knownService(name string) error {
	if b.file != nil {
		if _, ok := b.file.Services[name]; ok {
			return nil
		}
	}
	return fmt.Errorf("supervisor: unknown service %q", name)
}

func (b stoppedBackend) knownAction(name string) error {
	if b.file != nil {
		if _, ok := b.file.Actions[name]; ok {
			return nil
		}
	}
	return fmt.Errorf("action %q not found", name)
}

func (b stoppedBackend) logsDir() (string, error) {
	locs, err := project.Resolve(b.project)
	if err != nil {
		return "", err
	}
	return locs.LogsDir, nil
}

func (b stoppedBackend) LogPath(name string) (string, error) {
	if err := b.knownService(name); err != nil {
		return "", err
	}
	dir, err := b.logsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".log"), nil
}

func (b stoppedBackend) PreviousLogPath(name string) (string, error) {
	if err := b.knownService(name); err != nil {
		return "", err
	}
	dir, err := b.logsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".prev.log"), nil
}

func (b stoppedBackend) ActionLogPath(name string) (string, error) {
	if err := b.knownAction(name); err != nil {
		return "", err
	}
	dir, err := b.logsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "actions", name+".log"), nil
}

func (b stoppedBackend) ActionPreviousLogPath(name string) (string, error) {
	if err := b.knownAction(name); err != nil {
		return "", err
	}
	dir, err := b.logsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "actions", name+".prev.log"), nil
}

// Compile-time assertion that stoppedBackend satisfies control.Backend.
var _ control.Backend = (*stoppedBackend)(nil)

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
	slog.Info("building service", "project", cfg.Name, "service", name, "command", spec.Command)
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
// all of them except those with a project-level .stopped marker. Skipped
// projects are still registered in memory as stopped so list commands stay
// complete. It is called by the daemon child on startup. Returns the number of
// projects started and the number skipped.
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

		shouldAutostart := !hasProjectStoppedMarker(locs)

		if !shouldAutostart {
			// Register stopped project in memory so d.projects owns all registered projects.
			d.mu.Lock()
			if _, exists := d.projects[name]; !exists {
				d.projects[name] = &Project{
					Name:          name,
					ConfigPath:    configPath,
					BaseDir:       cfg.BaseDir,
					TotalServices: len(cfg.File.Services),
					File:          cfg.File,
					Order:         cfg.Order,
					done:          closedChan(),
				}
			}
			d.mu.Unlock()
			skipped++
			continue
		}

		if err := d.startProject(configPath, false, "", true); err != nil {
			slog.Error("autostart failed for project", "project", name, "error", err)
			skipped++
			continue
		}
		started++
	}
	return started, skipped, nil
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

// Ports returns open listening sockets for a project (or all projects when project is empty).
func (d *Daemon) Ports(project string) ([]*protocol.PortBinding, error) {
	if project != "" {
		b, err := d.ProjectBackend(project)
		if err != nil {
			return nil, err
		}
		return b.Ports()
	}

	d.mu.Lock()
	projects := make([]string, 0, len(d.projects))
	for name := range d.projects {
		projects = append(projects, name)
	}
	d.mu.Unlock()

	var all []*protocol.PortBinding
	for _, name := range projects {
		b, err := d.ProjectBackend(name)
		if err != nil {
			continue
		}
		p, err := b.Ports()
		if err == nil {
			all = append(all, p...)
		}
	}
	return all, nil
}
