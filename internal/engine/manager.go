package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/paths"
)

// Manager is the registry of projects owned by the daemon.
type Manager struct {
	dirs     paths.Dirs
	launcher Launcher
	obs      Observer
	log      *slog.Logger

	mu       sync.Mutex
	projects map[string]*Project
	draining bool
}

// NewManager returns an empty manager.
func NewManager(dirs paths.Dirs, launcher Launcher, obs Observer, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{dirs: dirs, launcher: launcher, obs: obs, log: log, projects: map[string]*Project{}}
}

// Load registers every persisted project. Each project adopts its live runs
// and starts what it wants running (see serviceActor.initialize). A project
// whose config cannot be loaded is still registered, in the error state.
func (m *Manager) Load() error {
	ids, err := listRegistrations(m.dirs)
	if err != nil {
		return fmt.Errorf("engine: list projects: %w", err)
	}
	for _, id := range ids {
		reg, err := loadRegistration(m.dirs, id)
		if err != nil {
			m.log.Error("skipping unreadable project registration", "project", id, "error", err)
			continue
		}
		p := newProject(m.dirs, *reg, m.launcher, m.obs)
		m.mu.Lock()
		m.projects[id] = p
		m.mu.Unlock()
		if v := p.View(); v.LoadErr != "" {
			m.log.Warn("project config could not be loaded", "project", id, "error", v.LoadErr)
		}
	}
	return nil
}

// AddOptions registers (or re-registers) a project.
type AddOptions struct {
	ConfigPath string
	EnvFile    string
	// Env is the launch environment; nil means the daemon's environment.
	Env   []string
	Start bool
	Build bool
}

// Add registers a project from its config, or updates the registration of
// an existing project with the same config. Registering a different config
// that declares the same project name fails with ErrAlreadyExists.
func (m *Manager) Add(ctx context.Context, opts AddOptions) (*Project, error) {
	abs, err := filepath.Abs(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	env = CleanEnv(env)
	envFile := opts.EnvFile
	if envFile != "" {
		if envFile, err = filepath.Abs(envFile); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	// Parse once to learn the project id.
	resolvedEnvFile, dotenv, err := config.ResolveDotEnv(abs, envFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	_ = resolvedEnvFile
	file, _, err := config.Load(abs, config.InterpolationEnv(env, dotenv))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	id := file.ID()

	m.mu.Lock()
	if m.draining {
		m.mu.Unlock()
		return nil, ErrShuttingDown
	}
	p, exists := m.projects[id]
	if !exists {
		reg := Registration{
			ID:         id,
			ConfigPath: abs,
			EnvFile:    envFile,
			Env:        env,
			Desired:    DesiredStopped,
			CreatedAt:  time.Now(),
		}
		if err := saveRegistration(m.dirs, &reg); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		p = newProject(m.dirs, reg, m.launcher, m.obs)
		m.projects[id] = p
	}
	m.mu.Unlock()

	if exists {
		if err := p.reconfigure(ctx, abs, envFile, env); err != nil {
			return p, err
		}
	}
	if opts.Start {
		if err := p.Start(ctx, nil, opts.Build); err != nil {
			return p, err
		}
	}
	return p, nil
}

// Get returns a project by id.
func (m *Manager) Get(id string) (*Project, error) {
	if err := paths.ValidateID(id); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return nil, fmt.Errorf("project %q: %w", id, ErrNotFound)
	}
	return p, nil
}

// List returns all projects sorted by id.
func (m *Manager) List() []*Project {
	m.mu.Lock()
	out := make([]*Project, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, p)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Remove stops a project and deletes its registration, state and logs.
func (m *Manager) Remove(ctx context.Context, id string) error {
	p, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := p.remove(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	if m.projects[id] == p {
		delete(m.projects, id)
	}
	m.mu.Unlock()
	return nil
}

// Draining reports whether the manager is shutting down.
func (m *Manager) Draining() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.draining
}

// Shutdown stops accepting commands and ends every project actor. With stop,
// services and tasks are stopped first; otherwise they keep running under
// their runners and are adopted by the next daemon.
func (m *Manager) Shutdown(ctx context.Context, stop bool) error {
	m.mu.Lock()
	m.draining = true
	projects := make([]*Project, 0, len(m.projects))
	for _, p := range m.projects {
		projects = append(projects, p)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	errs := make([]error, len(projects))
	for i, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = p.shutdown(ctx, stop)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
