// Package gitwatcher provides efficient, debounced file system watching for
// Git repositories. It watches .git repository state (HEAD, index, refs, logs)
// to detect commits, branch switches, stashes, and staging operations without
// recursively watching worktree source files.
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

type RepoWatcher struct {
	mu           sync.Mutex
	watcher      *fsnotify.Watcher
	onChange     func(project string)
	projects     map[string]string // project -> dir
	dirToProject map[string]string // watched path -> project
	debounce     time.Duration
	timers       map[string]*time.Timer
	stopCh       chan struct{}
	closeOnce    sync.Once
}

// New creates a new RepoWatcher that triggers onChange(projectName) when repository
// files or .git states change.
func New(onChange func(project string)) (*RepoWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("gitwatcher: new watcher: %w", err)
	}

	rw := &RepoWatcher{
		watcher:      fw,
		onChange:     onChange,
		projects:     make(map[string]string),
		dirToProject: make(map[string]string),
		debounce:     300 * time.Millisecond,
		timers:       make(map[string]*time.Timer),
		stopCh:       make(chan struct{}),
	}

	go rw.run()
	return rw, nil
}

// SetDebounce updates the event debounce duration.
func (w *RepoWatcher) SetDebounce(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.debounce = d
}

// AddProject adds a project directory to be watched.
func (w *RepoWatcher) AddProject(name, dir string) error {
	if name == "" || dir == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("gitwatcher: abs path %s: %w", dir, err)
	}

	if oldDir, exists := w.projects[name]; exists {
		if oldDir == absDir {
			return nil
		}
		w.removeProjectLocked(name, oldDir)
	}

	w.projects[name] = absDir
	w.watchProjectLocked(name, absDir)
	return nil
}

// RemoveProject stops watching a project directory.
func (w *RepoWatcher) RemoveProject(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if dir, exists := w.projects[name]; exists {
		w.removeProjectLocked(name, dir)
		delete(w.projects, name)
	}
}

func (w *RepoWatcher) removeProjectLocked(name, dir string) {
	for watchedPath, proj := range w.dirToProject {
		if proj == name {
			_ = w.watcher.Remove(watchedPath)
			delete(w.dirToProject, watchedPath)
		}
	}
	if t, exists := w.timers[name]; exists {
		t.Stop()
		delete(w.timers, name)
	}
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
	// If .git is a file (e.g. git worktree or submodule), read `gitdir: <path>`
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false
	}
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "gitdir:") {
		target := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		target = filepath.Clean(target)
		if tfi, err := os.Stat(target); err == nil && tfi.IsDir() {
			return target, true
		}
	}
	return "", false
}

func (w *RepoWatcher) watchProjectLocked(name, dir string) {
	gitDir, ok := resolveGitDir(dir)
	if !ok {
		return
	}

	w.addWatchPath(name, gitDir)
	w.addWatchPath(name, filepath.Join(gitDir, "refs"))
	w.addWatchPath(name, filepath.Join(gitDir, "refs", "heads"))
	w.addWatchPath(name, filepath.Join(gitDir, "refs", "tags"))
	w.addWatchPath(name, filepath.Join(gitDir, "refs", "remotes"))
	w.addWatchPath(name, filepath.Join(gitDir, "logs"))
	w.addWatchPath(name, filepath.Join(gitDir, "logs", "refs"))
	w.addWatchPath(name, filepath.Join(gitDir, "logs", "refs", "heads"))
}

func (w *RepoWatcher) addWatchPath(name, path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := w.watcher.Add(path); err == nil {
		w.dirToProject[path] = name
	}
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
	base := filepath.Base(event.Name)
	if strings.HasPrefix(base, ".DS_Store") || strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".lock") {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	matchedProject, ok := w.dirToProject[event.Name]
	if !ok {
		eventDir := filepath.Dir(event.Name)
		matchedProject, ok = w.dirToProject[eventDir]
	}
	if !ok {
		for proj, dir := range w.projects {
			if event.Name == dir || strings.HasPrefix(event.Name, dir+string(os.PathSeparator)) {
				matchedProject = proj
				break
			}
		}
	}

	if matchedProject == "" {
		return
	}

	if t, exists := w.timers[matchedProject]; exists {
		t.Stop()
	}

	proj := matchedProject
	w.timers[proj] = time.AfterFunc(w.debounce, func() {
		if w.onChange != nil {
			w.onChange(proj)
		}
	})
}

// Close releases watcher resources and stops event loops.
func (w *RepoWatcher) Close() error {
	w.closeOnce.Do(func() {
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
