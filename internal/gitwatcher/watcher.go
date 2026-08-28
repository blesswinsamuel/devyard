// Package gitwatcher provides efficient, debounced file system watching for
// Git repositories. It watches .git state and source files while filtering out
// heavy build and dependency directories.
package gitwatcher

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var defaultIgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".cache":       true,
	"bin":          true,
	".venv":        true,
	"__pycache__":  true,
	".idea":        true,
	".vscode":      true,
}

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

func (w *RepoWatcher) watchProjectLocked(name, dir string) {
	gitDir := filepath.Join(dir, ".git")
	w.addWatchPath(name, gitDir)
	w.addWatchPath(name, filepath.Join(gitDir, "refs"))
	w.addWatchPath(name, filepath.Join(gitDir, "refs", "heads"))
	w.addWatchPath(name, filepath.Join(gitDir, "refs", "tags"))
	w.addWatchPath(name, filepath.Join(gitDir, "logs"))

	ignored := make(map[string]bool)
	for k, v := range defaultIgnoredDirs {
		ignored[k] = v
	}
	readGitIgnore(filepath.Join(dir, ".gitignore"), ignored)

	const maxDepth = 6
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		if rel != "." {
			if strings.Count(rel, string(os.PathSeparator)) >= maxDepth {
				return filepath.SkipDir
			}

			base := d.Name()
			if ignored[base] || strings.HasPrefix(base, ".") {
				if path != gitDir {
					return filepath.SkipDir
				}
			}
		}

		w.addWatchPath(name, path)
		return nil
	})
}

func (w *RepoWatcher) addWatchPath(name, path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := w.watcher.Add(path); err == nil {
		w.dirToProject[path] = name
	}
}

func readGitIgnore(gitignorePath string, ignored map[string]bool) {
	f, err := os.Open(gitignorePath)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if !strings.Contains(line, "*") && !strings.Contains(line, "/") {
			ignored[line] = true
		}
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
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *RepoWatcher) handleEvent(event fsnotify.Event) {
	base := filepath.Base(event.Name)
	if strings.HasPrefix(base, ".DS_Store") || strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var matchedProject string
	eventDir := filepath.Dir(event.Name)

	for watchedPath, proj := range w.dirToProject {
		if event.Name == watchedPath || eventDir == watchedPath || strings.HasPrefix(event.Name, watchedPath+string(os.PathSeparator)) {
			matchedProject = proj
			break
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
