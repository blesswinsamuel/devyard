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
// builds are run before starting services. envFile is the absolute path to an
// env file to layer under service env (empty falls back to .env next to the
// config file). If a project with the same name is already running, it returns
// an error.
func (d *Daemon) StartProject(configPath string, build bool, envFile string) error {
	cfg, err := loadConfig(configPath, envFile)
	if err != nil {
		return err
	}

	name := cfg.Name

	d.mu.Lock()
	if p, exists := d.projects[name]; exists {
		if p.Status() == "running" {
			d.mu.Unlock()
			return fmt.Errorf("project %q is already running; run 'local-compose stop' first", name)
		}
	}
	d.mu.Unlock()

	locs, err := project.Resolve(name)
	if err != nil {
		return err
	}
	if err := locs.MkdirAll(); err != nil {
		return err
	}

	// Remove the project-level stopped marker so unless-stopped autostart
	// will resume this project on the next daemon start.
	_ = removeProjectStoppedMarker(locs)

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

	// Write config path for autostart discovery.
	_ = writeConfigPath(locs, configPath)

	go func() {
		sup.Wait()
		close(p.done)
	}()

	return nil
}

// StopProject stops the named project's services, writes a project-level
// stopped marker (so unless-stopped autostart won't resume it), and closes its
// supervisor. The project remains in the daemon's map.
func (d *Daemon) StopProject(name string) error {
	d.mu.Lock()
	p, ok := d.projects[name]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q is not running", name)
	}
	if p.Status() == "stopped" {
		return nil
	}

	locs, err := project.Resolve(name)
	if err == nil {
		_ = writeProjectStoppedMarker(locs)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
	defer cancel()
	if err := p.Sup.Stop(ctx); err != nil {
		return err
	}
	<-p.done
	_ = p.Sup.Close()
	p.cancel()

	return nil
}

// RemoveProject stops the named project's services if running, removes the project
// from the daemon's map, and deletes its runtime and state directories.
func (d *Daemon) RemoveProject(name string) error {
	d.mu.Lock()
	p, exists := d.projects[name]
	d.mu.Unlock()

	if exists {
		if p.Status() == "running" {
			locs, err := project.Resolve(name)
			if err == nil {
				_ = writeProjectStoppedMarker(locs)
			}
			ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultGracefulStopTimeout)
			if err := p.Sup.Stop(ctx); err != nil {
				cancel()
				return err
			}
			cancel()
			<-p.done
			_ = p.Sup.Close()
			p.cancel()
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

	return nil
}

// ListProjects returns a snapshot of all known projects (both running and stopped)
// and their statuses.
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

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

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
		shouldAutostart := policy == config.RestartAlways || (policy == config.RestartUnlessStopped && !hasProjectStoppedMarker(locs))

		if !shouldAutostart {
			// Register stopped project in memory so d.projects owns all registered projects.
			d.mu.Lock()
			if _, exists := d.projects[name]; !exists {
				d.projects[name] = &Project{
					Name:       name,
					ConfigPath: configPath,
					BaseDir:    cfg.BaseDir,
					done:       closedChan(),
				}
			}
			d.mu.Unlock()
			skipped++
			continue
		}

		if err := d.StartProject(configPath, false, ""); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: autostart: project %q: %v\n", name, err)
			skipped++
			continue
		}
		started++
	}
	return started, skipped, nil
}

// autostartPolicy returns the restart policy that determines whether a
// project should autostart. If any service has restart: always, the project
// autostarts unconditionally. If any service has restart: unless-stopped, the
// project autostarts unless a .stopped marker exists. Otherwise the project
// does not autostart.
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
