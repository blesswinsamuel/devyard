package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

// Event is one Watch message as recorded by a Watcher.
type Event struct {
	Conn     int // connection number (1-based; increments on reconnect)
	Revision uint64
	At       time.Time
	Snapshot bool
	Kind     string // "project" | "service" | "task" | "git" | "daemon" | "removed"
	Project  string
	Name     string
	Status   string
	Health   string
	Pid      int32
	Run      int64
	Exit     int32
	Restarts int32
}

func (e Event) String() string {
	if e.Snapshot {
		return fmt.Sprintf("#%d rev=%d SNAPSHOT", e.Conn, e.Revision)
	}
	return fmt.Sprintf("#%d rev=%d %s %s/%s status=%s health=%s pid=%d run=%d exit=%d", e.Conn, e.Revision, e.Kind, e.Project, e.Name, e.Status, e.Health, e.Pid, e.Run, e.Exit)
}

// Watcher maintains the state materialized from Watch (snapshot, changes,
// removals, resyncs), reconnecting when the stream breaks.
type Watcher struct {
	sb     *Sandbox
	client devyardv1connect.DaemonServiceClient
	cancel context.CancelFunc
	done   chan struct{}
	record bool // record events (off for the sandbox's own leak recorder)

	mu         sync.Mutex
	state      State
	haveState  bool
	events     []Event
	snapshots  int
	conns      int
	violations []string
	lastErr    error
	notify     chan struct{}
}

// Watch starts a watcher that runs until ctx is done or the sandbox is torn
// down.
func (d *Daemon) Watch(ctx context.Context) *Watcher {
	w := d.sb.newWatcher(ctx, true)
	d.sb.t.Cleanup(w.Close)
	return w
}

func (sb *Sandbox) newWatcher(ctx context.Context, record bool) *Watcher {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-sb.ctx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	w := &Watcher{
		sb:     sb,
		client: sb.Client(),
		cancel: cancel,
		done:   make(chan struct{}),
		record: record,
		state:  emptyState(),
		notify: make(chan struct{}),
	}
	go w.loop(ctx)
	return w
}

// ensureRecorder starts the sandbox-wide watcher that feeds the leak
// checker with every pid the API ever reports.
func (sb *Sandbox) ensureRecorder() {
	sb.mu.Lock()
	if sb.recorder != nil || sb.closing {
		sb.mu.Unlock()
		return
	}
	sb.mu.Unlock()
	w := sb.newWatcher(sb.ctx, false) // takes sb.mu via Client()
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.recorder != nil || sb.closing {
		w.cancel()
		return
	}
	sb.recorder = w
}

// Close stops the watcher and waits for its goroutine.
func (w *Watcher) Close() {
	w.cancel()
	<-w.done
}

func (w *Watcher) loop(ctx context.Context) {
	defer close(w.done)
	for ctx.Err() == nil {
		stream, err := w.client.Watch(ctx, connect.NewRequest(&v1.WatchRequest{}))
		if err != nil {
			w.setErr(err)
			sleepCtx(ctx, 100*time.Millisecond)
			continue
		}
		w.mu.Lock()
		w.conns++
		conn := w.conns
		w.mu.Unlock()
		first := true
		var lastRev uint64
		for stream.Receive() {
			msg := stream.Msg()
			w.handle(conn, first, lastRev, msg)
			first = false
			if msg.GetRevision() > lastRev {
				lastRev = msg.GetRevision()
			}
		}
		w.setErr(stream.Err())
		_ = stream.Close()
		sleepCtx(ctx, 100*time.Millisecond)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (w *Watcher) setErr(err error) {
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
}

func (w *Watcher) handle(conn int, first bool, lastRev uint64, msg *v1.WatchResponse) {
	rev := msg.GetRevision()
	now := time.Now()
	w.mu.Lock()
	switch ev := msg.GetEvent().(type) {
	case *v1.WatchResponse_Snapshot:
		if !first && rev < lastRev {
			w.violations = append(w.violations, fmt.Sprintf("conn %d: resync snapshot rev %d < previous rev %d", conn, rev, lastRev))
		}
		w.state = StateFromSnapshot(rev, ev.Snapshot)
		w.haveState = true
		w.snapshots++
		if w.record {
			w.events = append(w.events, Event{Conn: conn, Revision: rev, At: now, Snapshot: true})
		}
	case *v1.WatchResponse_Change:
		if first {
			w.violations = append(w.violations, fmt.Sprintf("conn %d: first message is a change (rev %d), want a snapshot", conn, rev))
		} else if rev <= lastRev {
			w.violations = append(w.violations, fmt.Sprintf("conn %d: change rev %d not greater than previous %d", conn, rev, lastRev))
		}
		w.state.apply(rev, ev.Change)
		if w.record {
			w.events = append(w.events, changeEvent(conn, rev, now, ev.Change))
		}
	case *v1.WatchResponse_Heartbeat:
		if first {
			w.violations = append(w.violations, fmt.Sprintf("conn %d: first message is a heartbeat, want a snapshot", conn))
		} else if rev != 0 && rev < lastRev {
			w.violations = append(w.violations, fmt.Sprintf("conn %d: heartbeat rev %d < previous %d", conn, rev, lastRev))
		}
	default:
		w.violations = append(w.violations, fmt.Sprintf("conn %d: message with no event (rev %d)", conn, rev))
	}
	st := w.state
	ch := w.notify
	w.notify = make(chan struct{})
	w.mu.Unlock()
	close(ch)
	w.sb.recordState(st)
}

func changeEvent(conn int, rev uint64, at time.Time, ch *v1.Change) Event {
	e := Event{Conn: conn, Revision: rev, At: at}
	switch x := ch.GetChange().(type) {
	case *v1.Change_Project:
		e.Kind, e.Project, e.Status = "project", x.Project.GetId(), x.Project.GetStatus()
	case *v1.Change_Service:
		s := x.Service
		e.Kind, e.Project, e.Name, e.Status, e.Health, e.Pid, e.Run, e.Exit = "service", s.GetProject(), s.GetName(), s.GetStatus(), s.GetHealth(), s.GetPid(), s.GetRun(), s.GetExitCode()
		e.Restarts = s.GetRestarts()
	case *v1.Change_Task:
		tk := x.Task
		e.Kind, e.Project, e.Name, e.Status, e.Pid, e.Run, e.Exit = "task", tk.GetProject(), tk.GetName(), tk.GetStatus(), tk.GetPid(), tk.GetRun(), tk.GetExitCode()
	case *v1.Change_Git:
		e.Kind, e.Project = "git", x.Git.GetProject()
	case *v1.Change_Daemon:
		e.Kind, e.Pid = "daemon", x.Daemon.GetPid()
	case *v1.Change_Removed:
		e.Kind, e.Project, e.Name, e.Status = "removed", x.Removed.GetProject(), x.Removed.GetName(), x.Removed.GetKind()
	}
	return e
}

// State returns a copy of the current materialized state.
func (w *Watcher) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state.clone()
}

// Events returns every recorded event so far.
func (w *Watcher) Events() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Event(nil), w.events...)
}

// ServiceEvents returns the recorded changes of one service.
func (w *Watcher) ServiceEvents(project, name string) []Event {
	var out []Event
	for _, e := range w.Events() {
		if e.Kind == "service" && e.Project == project && e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// Snapshots returns how many snapshots have been received (initial +
// reconnects + resyncs).
func (w *Watcher) Snapshots() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snapshots
}

// Connections returns how many streams have been opened.
func (w *Watcher) Connections() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conns
}

// Violations returns protocol violations seen so far (non-monotonic
// revisions, a first message that isn't a snapshot, ...).
func (w *Watcher) Violations() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.violations...)
}

// AssertNoViolations fails t if any protocol violation was recorded.
func (w *Watcher) AssertNoViolations(t TB) {
	t.Helper()
	if v := w.Violations(); len(v) > 0 {
		t.Errorf("watch protocol violations:\n  %s", strings.Join(v, "\n  "))
	}
}

// WaitFor waits (DefaultWait, scaled) until pred holds for the materialized
// state, and returns that state. On timeout it fails t with the last state,
// the daemon log tail and the service logs.
func (w *Watcher) WaitFor(t TB, desc string, pred func(State) bool) State {
	t.Helper()
	return w.WaitForWithin(t, DefaultWait, desc, pred)
}

// WaitForWithin is WaitFor with an explicit (unscaled) deadline.
func (w *Watcher) WaitForWithin(t TB, d time.Duration, desc string, pred func(State) bool) State {
	t.Helper()
	timeout := Scale(d)
	deadline := time.After(timeout)
	for {
		w.mu.Lock()
		st := w.state.clone()
		have := w.haveState
		ch := w.notify
		w.mu.Unlock()
		if have && pred(st) {
			return st
		}
		select {
		case <-ch:
		case <-time.After(250 * time.Millisecond):
			// Re-evaluate periodically: predicates may consult the clock or
			// the process table, not just the state.
		case <-deadline:
			w.mu.Lock()
			lastErr := w.lastErr
			conns, snaps := w.conns, w.snapshots
			w.mu.Unlock()
			t.Fatalf("watch: timed out after %s waiting for %s\nwatch: conns=%d snapshots=%d lastErr=%v\nlast observed %s\nrecent events:\n%s\n%s",
				timeout, desc, conns, snaps, lastErr, st, w.recentEvents(30), w.sb.Diagnostics())
			return st
		}
	}
}

// WaitSnapshots waits until at least n snapshots have been received.
func (w *Watcher) WaitSnapshots(t TB, n int) State {
	t.Helper()
	Eventually(t, fmt.Sprintf("%d watch snapshots", n), func(c *C) {
		if got := w.Snapshots(); got < n {
			c.Errorf("got %d snapshots", got)
		}
	}, Within(30*time.Second), WithDiagnostics(w.sb.daemonLogDiag))
	return w.State()
}

func (w *Watcher) recentEvents(n int) string {
	ev := w.Events()
	if len(ev) > n {
		ev = ev[len(ev)-n:]
	}
	var b strings.Builder
	for _, e := range ev {
		b.WriteString("  " + e.String() + "\n")
	}
	return b.String()
}
