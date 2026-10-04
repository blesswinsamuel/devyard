// Package projects keeps the daemon's registered projects in line with the
// `projects` list of the global config: adding or removing a project edits
// that list (preserving comments), and a change to the list made by hand is
// reconciled into the engine. The list is the single source of truth for
// which projects exist and in which order; the state directory only holds
// their runtime state.
package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/paths"
)

// maxRecent bounds the recently-removed list.
const maxRecent = 20

// Service owns the project list. Methods are safe for concurrent use and
// serialized against each other: a reconcile never sees a half-made edit.
type Service struct {
	mgr        *engine.Manager
	configPath string
	recentPath string
	log        *slog.Logger

	mu sync.Mutex
}

// New returns a Service over the manager and the global config at
// dirs.GlobalConfig().
func New(mgr *engine.Manager, dirs paths.Dirs, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		mgr:        mgr,
		configPath: dirs.GlobalConfig(),
		recentPath: filepath.Join(dirs.State, "recent-projects.json"),
		log:        log,
	}
}

// Start brings the engine in line with the list at daemon startup. A global
// config without a `projects` key yet adopts the projects already
// registered, so projects added before the list existed are not lost.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := globalconfig.Load(s.configPath, nil)
	if err != nil {
		return err
	}
	if !cfg.ProjectsSet {
		var adopted []string
		for _, p := range s.mgr.List() {
			if _, err := globalconfig.AddProject(s.configPath, p.ConfigPath()); err != nil {
				return err
			}
			adopted = append(adopted, p.ID())
		}
		if len(adopted) > 0 {
			s.log.Info("adopted registered projects into the global config", "projects", adopted)
		}
		if cfg, err = globalconfig.Load(s.configPath, nil); err != nil {
			return err
		}
	}
	return s.reconcileLocked(ctx, cfg)
}

// Reconcile applies cfg's project list: listed projects are registered
// (stopped, with the daemon's environment, unless already known), unlisted
// ones are stopped and forgotten, and the display order is set. A config
// without a `projects` key changes nothing. The returned error joins the
// problems of individual entries; the others are still applied.
func (s *Service) Reconcile(ctx context.Context, cfg *globalconfig.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileLocked(ctx, cfg)
}

func (s *Service) reconcileLocked(ctx context.Context, cfg *globalconfig.Config) error {
	if !cfg.ProjectsSet {
		return nil
	}
	desired := cfg.ProjectPaths()
	var errs []error
	for _, path := range desired {
		if _, ok := s.mgr.ByConfigPath(path); ok {
			continue
		}
		if _, err := s.mgr.Add(ctx, engine.AddOptions{ConfigPath: path, Tolerant: true}); err != nil {
			errs = append(errs, fmt.Errorf("project %s: %w", globalconfig.AbbreviateHome(path), err))
		}
	}
	for _, p := range s.mgr.List() {
		if slices.Contains(desired, p.ConfigPath()) {
			continue
		}
		s.log.Info("project removed from the global config", "project", p.ID())
		if err := s.removeLocked(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	s.mgr.SetOrder(desired)
	return errors.Join(errs...)
}

// AddOptions registers a project.
type AddOptions struct {
	// Path is a project directory or a config file.
	Path string
	// Env is the caller's launch environment; nil means the daemon's.
	Env   []string
	Start bool
	Build bool
}

// Add appends the project to the list (when it is not there yet), registers
// it and optionally starts it. A project that cannot be registered (invalid
// config, name clash) is not left in the list.
func (s *Service) Add(ctx context.Context, opts AddOptions) (*engine.Project, error) {
	configPath, err := globalconfig.EntryConfigPath(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	added, err := globalconfig.AddProject(s.configPath, configPath)
	if err != nil {
		return nil, err
	}
	p, err := s.mgr.Add(ctx, engine.AddOptions{ConfigPath: configPath, Env: opts.Env, Start: opts.Start, Build: opts.Build})
	if err != nil && (p == nil || errors.Is(err, engine.ErrAlreadyExists)) {
		// Not registered (or the name belongs to another project): the
		// entry must not stay in the list.
		if added {
			if _, rerr := globalconfig.RemoveProject(s.configPath, configPath); rerr != nil {
				s.log.Error("could not undo adding project to the global config", "path", configPath, "error", rerr)
			}
		}
		return nil, err
	}
	s.syncOrderLocked()
	return p, err
}

// Remove drops the project from the list, stops its services and deletes
// its state and logs. Its path goes on the recently-removed list.
func (s *Service) Remove(ctx context.Context, id string) error {
	p, err := s.mgr.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := globalconfig.RemoveProject(s.configPath, p.ConfigPath()); err != nil {
		return err
	}
	if err := s.removeLocked(ctx, p); err != nil {
		return err
	}
	s.syncOrderLocked()
	return nil
}

func (s *Service) removeLocked(ctx context.Context, p *engine.Project) error {
	configPath := p.ConfigPath()
	if err := s.mgr.Remove(ctx, p.ID()); err != nil {
		return err
	}
	s.pushRecent(configPath)
	return nil
}

// Move places the project at index (0-based, clamped) in the list.
func (s *Service) Move(ctx context.Context, id string, index int) error {
	p, err := s.mgr.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := globalconfig.MoveProject(s.configPath, p.ConfigPath(), index); err != nil {
		return fmt.Errorf("%w: %v", engine.ErrNotFound, err)
	}
	s.syncOrderLocked()
	return nil
}

// syncOrderLocked pushes the list's order to the engine after an edit made
// here (a hand edit arrives through Reconcile).
func (s *Service) syncOrderLocked() {
	cfg, err := globalconfig.Load(s.configPath, nil)
	if err != nil {
		s.log.Warn("global config unreadable; project order not updated", "error", err)
		return
	}
	s.mgr.SetOrder(cfg.ProjectPaths())
}

// --- recently removed --------------------------------------------------------

func (s *Service) readRecent() []string {
	data, err := os.ReadFile(s.recentPath)
	if err != nil {
		return nil
	}
	var paths []string
	if json.Unmarshal(data, &paths) != nil {
		return nil
	}
	return paths
}

func (s *Service) pushRecent(configPath string) {
	list := []string{configPath}
	for _, p := range s.readRecent() {
		if p != configPath {
			list = append(list, p)
		}
	}
	if len(list) > maxRecent {
		list = list[:maxRecent]
	}
	data, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := s.recentPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		s.log.Warn("could not record the removed project", "error", err)
		return
	}
	_ = os.Rename(tmp, s.recentPath)
}

// Suggestion is a directory that could be added as a project.
type Suggestion struct {
	// Path is the absolute directory.
	Path      string
	HasConfig bool
	IsGit     bool
	// Listed is true when the directory is already a project.
	Listed bool
}

// Suggestions returns the recently removed projects that still exist (newest
// first) and the directories matching prefix: the subdirectories of its
// directory part whose names start with its last element.
func (s *Service) Suggestions(prefix string) (recent, completions []Suggestion) {
	listed := map[string]bool{}
	for _, p := range s.mgr.List() {
		listed[filepath.Dir(p.ConfigPath())] = true
	}
	for _, configPath := range s.readRecent() {
		dir := filepath.Dir(configPath)
		if listed[dir] {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		recent = append(recent, describe(dir, listed))
	}
	if strings.TrimSpace(prefix) == "" {
		return recent, nil
	}
	p := globalconfig.ExpandHome(prefix)
	parent, partial := filepath.Split(p)
	if parent == "" {
		return recent, nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return recent, nil
	}
	showHidden := strings.HasPrefix(partial, ".")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(partial)) || (!showHidden && strings.HasPrefix(name, ".")) {
			continue
		}
		full := filepath.Join(parent, name)
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			continue
		}
		completions = append(completions, describe(full, listed))
	}
	sort.Slice(completions, func(i, j int) bool { return completions[i].Path < completions[j].Path })
	if len(completions) > 50 {
		completions = completions[:50]
	}
	return recent, completions
}

func describe(dir string, listed map[string]bool) Suggestion {
	s := Suggestion{Path: dir, Listed: listed[dir]}
	if path, err := globalconfig.EntryConfigPath(dir); err == nil {
		_, statErr := os.Stat(path)
		s.HasConfig = statErr == nil
	}
	_, gitErr := os.Stat(filepath.Join(dir, ".git"))
	s.IsGit = gitErr == nil
	return s
}
