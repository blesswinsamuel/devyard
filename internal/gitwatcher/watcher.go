// Package gitwatcher provides debounced file system watching for Git
// repositories. It watches repository state (HEAD, index, refs, logs) to
// detect commits, branch switches, stashes and staging without recursively
// watching worktree source files.
//
// Several projects may share one repository (or one git common dir with
// worktrees): watched paths are reference-counted per project, so removing
// one project never stops notifications for another.
package gitwatcher

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// RepoWatcher watches the git state of registered projects.
type RepoWatcher struct {
	mu       sync.Mutex
	watcher  *fsnotify.Watcher
	onChange func(project string)
	projects map[string]string              // project -> dir
	watching map[string]map[string]struct{} // watched path -> projects
	recurse  map[string]bool                // watched paths whose new subdirs are watched too
	debounce time.Duration
	timers   map[string]*time.Timer
	stopCh   chan struct{}
	once     sync.Once
}

// New creates a RepoWatcher that calls onChange(project) (debounced) when a
// project's repository state changes.
func New(onChange func(project string)) (*RepoWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("gitwatcher: new watcher: %w", err)
	}
	rw := &RepoWatcher{
		watcher:  fw,
		onChange: onChange,
		projects: make(map[string]string),
		watching: make(map[string]map[string]struct{}),
		recurse:  make(map[string]bool),
		debounce: 300 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
		stopCh:   make(chan struct{}),
	}
	go rw.run()
	return rw, nil
}

// SetDebounce updates the debounce duration.
func (w *RepoWatcher) SetDebounce(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.debounce = d
}

// AddProject starts (or updates) watching a project's directory.
func (w *RepoWatcher) AddProject(name, dir string) error {
	if name == "" || dir == "" {
		return nil
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("gitwatcher: abs path %s: %w", dir, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.projects[name]; ok {
		if old == absDir {
			return nil
		}
		w.unwatchLocked(name)
	}
	w.projects[name] = absDir
	gitDir, ok := resolveGitDir(absDir)
	if !ok {
		return nil
	}
	w.addLocked(name, gitDir, false)
	for _, d := range []string{"logs", filepath.Join("logs", "refs")} {
		w.addLocked(name, filepath.Join(gitDir, d), false)
	}
	common := commonDir(gitDir)
	for _, d := range []string{filepath.Join("refs", "heads"), filepath.Join("refs", "tags"), filepath.Join("refs", "remotes"), filepath.Join("logs", "refs", "heads")} {
		w.addTreeLocked(name, filepath.Join(common, d))
	}
	w.addLocked(name, filepath.Join(common, "refs"), false)
	if common != gitDir {
		w.addLocked(name, common, false)
	}
	return nil
}

// RemoveProject stops watching a project.
func (w *RepoWatcher) RemoveProject(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unwatchLocked(name)
	delete(w.projects, name)
	if t, ok := w.timers[name]; ok {
		t.Stop()
		delete(w.timers, name)
	}
}

func (w *RepoWatcher) unwatchLocked(name string) {
	for path, owners := range w.watching {
		if _, ok := owners[name]; !ok {
			continue
		}
		delete(owners, name)
		if len(owners) == 0 {
			_ = w.watcher.Remove(path)
			delete(w.watching, path)
			delete(w.recurse, path)
		}
	}
}

func (w *RepoWatcher) addLocked(name, path string, recurse bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return
	}
	owners, ok := w.watching[path]
	if !ok {
		if err := w.watcher.Add(path); err != nil {
			return
		}
		owners = make(map[string]struct{})
		w.watching[path] = owners
	}
	owners[name] = struct{}{}
	if recurse {
		w.recurse[path] = true
	}
}

// addTreeLocked watches root and every directory below it (branch names
// such as feature/x are directories under refs/heads).
func (w *RepoWatcher) addTreeLocked(name, root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		w.addLocked(name, path, true)
		return nil
	})
}

func resolveGitDir(dir string) (string, bool) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return gitPath, true
	}
	// A .git file (worktree or submodule) points at the real git dir.
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir:") {
		return "", false
	}
	target := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	target = filepath.Clean(target)
	if tfi, err := os.Stat(target); err == nil && tfi.IsDir() {
		return target, true
	}
	return "", false
}

// commonDir returns the shared git dir of a worktree (where refs live), or
// gitDir itself.
func commonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	c := strings.TrimSpace(string(data))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitDir, c)
	}
	return filepath.Clean(c)
}

func (w *RepoWatcher) run() {
	for {
		select {
		case <-w.stopCh:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				slog.Error("git watcher error", "error", err)
			}
		}
	}
}

func (w *RepoWatcher) handleEvent(event fsnotify.Event) {
	// Attribute-only events (atime updates from reading .git/index) would
	// cause refresh loops.
	if event.Op&^fsnotify.Chmod == 0 {
		return
	}
	base := filepath.Base(event.Name)
	if strings.HasPrefix(base, ".DS_Store") || strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".lock") {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	owners, ok := w.watching[event.Name]
	parent := filepath.Dir(event.Name)
	if !ok {
		owners, ok = w.watching[parent]
	}
	if !ok {
		return
	}
	// A new branch namespace directory (refs/heads/feature): watch it.
	if event.Op&fsnotify.Create != 0 && w.recurse[parent] {
		if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
			for name := range owners {
				w.addTreeLocked(name, event.Name)
			}
		}
	}
	for name := range owners {
		if t, exists := w.timers[name]; exists {
			t.Stop()
		}
		proj := name
		w.timers[proj] = time.AfterFunc(w.debounce, func() {
			if w.onChange != nil {
				w.onChange(proj)
			}
		})
	}
}

// Close releases watcher resources.
func (w *RepoWatcher) Close() error {
	w.once.Do(func() {
		close(w.stopCh)
		w.mu.Lock()
		for _, t := range w.timers {
			t.Stop()
		}
		w.mu.Unlock()
		_ = w.watcher.Close()
	})
	return nil
}
