// Package gitstate keeps every project's git status current on the event
// bus and serializes remote git operations per project.
package gitstate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/blesswinsamuel/devyard/internal/events"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gitlog"
	"github.com/blesswinsamuel/devyard/internal/gitwatcher"
)

// ErrBusy is returned when a remote operation is already running.
var ErrBusy = errors.New("another git operation is in progress")

// Tracker watches project repositories and publishes their status.
type Tracker struct {
	bus     *events.Bus
	watcher *gitwatcher.RepoWatcher
	log     *slog.Logger

	mu       sync.Mutex
	dirs     map[string]string // project -> repo dir
	seq      map[string]int64
	syncOp   map[string]string
	refresh  map[string]chan struct{}
	closing  bool
	wg       sync.WaitGroup
	shutdown chan struct{}
}

// New starts a tracker.
func New(bus *events.Bus, log *slog.Logger) (*Tracker, error) {
	t := &Tracker{
		bus:      bus,
		log:      log,
		dirs:     map[string]string{},
		seq:      map[string]int64{},
		syncOp:   map[string]string{},
		refresh:  map[string]chan struct{}{},
		shutdown: make(chan struct{}),
	}
	w, err := gitwatcher.New(t.Changed)
	if err != nil {
		return nil, err
	}
	t.watcher = w
	return t, nil
}

// Track starts or updates tracking of a project whose config lives at
// configPath.
func (t *Tracker) Track(project, configPath string) {
	dir := filepath.Dir(configPath)
	t.mu.Lock()
	if t.closing || t.dirs[project] == dir {
		t.mu.Unlock()
		return
	}
	t.dirs[project] = dir
	ch := t.refresh[project]
	if ch == nil {
		ch = make(chan struct{}, 1)
		t.refresh[project] = ch
		t.wg.Add(1)
		go t.worker(project, ch)
	}
	t.mu.Unlock()
	if err := t.watcher.AddProject(project, dir); err != nil {
		t.log.Warn("git watch failed", "project", project, "error", err)
	}
	t.Changed(project)
}

// Untrack stops tracking a project.
func (t *Tracker) Untrack(project string) {
	t.watcher.RemoveProject(project)
	t.mu.Lock()
	delete(t.dirs, project)
	if ch := t.refresh[project]; ch != nil {
		close(ch)
		delete(t.refresh, project)
	}
	t.mu.Unlock()
}

// Dir returns the repository directory of a project.
func (t *Tracker) Dir(project string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.dirs[project]
	return d, ok
}

// Changed schedules a status refresh (coalesced per project).
func (t *Tracker) Changed(project string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Sent under the lock so Untrack cannot close the channel mid-send.
	if ch := t.refresh[project]; ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// pollInterval is how often the working tree is fingerprinted. Watching
// .git catches commits, branch switches and staging; plain file edits only
// show up here.
const pollInterval = 3 * time.Second

func (t *Tracker) worker(project string, ch chan struct{}) {
	defer t.wg.Done()
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	last := ""
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			last = t.fingerprint(project)
			t.publish(project, true)
		case <-poll.C:
			if fp := t.fingerprint(project); fp != last {
				last = fp
				t.publish(project, true)
			}
		case <-t.shutdown:
			return
		}
	}
}

func (t *Tracker) fingerprint(project string) string {
	dir, ok := t.Dir(project)
	if !ok {
		return ""
	}
	return gitlog.Fingerprint(dir)
}

func (t *Tracker) publish(project string, changed bool) {
	t.mu.Lock()
	dir, ok := t.dirs[project]
	if changed {
		t.seq[project]++
	}
	seq := t.seq[project]
	op := t.syncOp[project]
	t.mu.Unlock()
	if !ok {
		return
	}
	st, err := gitlog.Status(dir)
	if err != nil {
		st = &pb.GitStatus{IsRepo: false, IsClean: true}
	}
	st.Project = project
	st.ChangeSeq = seq
	st.SyncOperation = op
	t.bus.UpsertGit(st)
}

// Remote runs push, pull or fetch for a project, publishing progress via
// GitStatus.sync_operation.
func (t *Tracker) Remote(ctx context.Context, project, op string) (string, error) {
	dir, ok := t.Dir(project)
	if !ok {
		return "", fmt.Errorf("project %q is not tracked", project)
	}
	t.mu.Lock()
	if t.syncOp[project] != "" {
		busy := t.syncOp[project]
		t.mu.Unlock()
		return "", fmt.Errorf("%w (%s)", ErrBusy, busy)
	}
	t.syncOp[project] = op
	t.mu.Unlock()
	t.publish(project, false)
	defer func() {
		t.mu.Lock()
		delete(t.syncOp, project)
		t.mu.Unlock()
		t.publish(project, true)
	}()
	switch op {
	case "push":
		return gitlog.Push(ctx, dir, "")
	case "pull":
		return gitlog.Pull(ctx, dir, "")
	case "fetch":
		return gitlog.Fetch(ctx, dir, "")
	}
	return "", fmt.Errorf("unknown git operation %q", op)
}

// Close stops the tracker.
func (t *Tracker) Close() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
	close(t.shutdown)
	_ = t.watcher.Close()
	t.wg.Wait()
}
