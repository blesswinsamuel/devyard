// Package events_test covers the Watch stream: snapshot first, a change for
// every transition, monotonic revisions, and convergence after reconnects
// and slow consumers.
package events_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const pair = `services:
  tick:
    run: {{fixture "ticker"}} -interval 200ms
  once:
    run: {{fixture "exiter"}} -code 4 -after 300ms
    restart: never
tasks:
  job: {{fixture "exiter"}} -code 2 -after 100ms
`

func TestEvents_FirstMessageIsSnapshot(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("snap", pair, nil)
	p.Start()
	d := sb.Daemon()
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(10*time.Second))
	defer cancel()
	stream, err := d.Client().Watch(ctx, connect.NewRequest(&v1.WatchRequest{}))
	harness.NoError(t, err, "Watch")
	defer func() { _ = stream.Close() }()
	if !stream.Receive() {
		t.Fatalf("no first message: %v", stream.Err())
	}
	snap := stream.Msg().GetSnapshot()
	if snap == nil {
		t.Fatalf("first message is not a snapshot: %v", stream.Msg())
	}
	if snap.GetDaemon().GetPid() != int32(d.Pid()) {
		t.Errorf("snapshot daemon pid %d, want %d", snap.GetDaemon().GetPid(), d.Pid())
	}
	st := harness.StateFromSnapshot(stream.Msg().GetRevision(), snap)
	if st.Project("snap") == nil || st.Service("snap", "tick") == nil || st.Task("snap", "job") == nil {
		t.Errorf("snapshot incomplete:\n%s", st)
	}
	if st.Task("snap", "job").GetStatus() != "idle" {
		t.Errorf("never-run task status %q, want idle", st.Task("snap", "job").GetStatus())
	}
}

func TestEvents_EveryTransitionEmitted(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	w.WaitSnapshots(t, 1)
	p := sb.WriteProject("trans", pair, nil)
	p.Start()
	st := w.WaitFor(t, "tick running, once exited", func(s harness.State) bool {
		return s.Running("trans", "tick") && s.ServiceIs("trans", "once", "exited", "failed")
	})
	if st.Service("trans", "once").GetExitCode() != 4 {
		t.Errorf("once exit code %d, want 4", st.Service("trans", "once").GetExitCode())
	}
	tickPid := st.ServicePid("trans", "tick")

	_, err := d.Client().RestartService(d.Ctx(), connect.NewRequest(&v1.RestartServiceRequest{Project: "trans", Service: "tick"}))
	harness.NoError(t, err, "RestartService")
	w.WaitFor(t, "tick restarted", func(s harness.State) bool {
		return s.Running("trans", "tick") && s.ServicePid("trans", "tick") != tickPid
	})
	_, err = d.Client().KillService(d.Ctx(), connect.NewRequest(&v1.KillServiceRequest{Project: "trans", Service: "tick"}))
	harness.NoError(t, err, "KillService")
	w.WaitFor(t, "tick killed", func(s harness.State) bool { return s.ServiceIs("trans", "tick", "exited", "failed", "stopped") })

	_, err = d.Client().RunTask(d.Ctx(), connect.NewRequest(&v1.RunTaskRequest{Project: "trans", Task: "job"}))
	harness.NoError(t, err, "RunTask")
	w.WaitFor(t, "task finished", func(s harness.State) bool {
		tk := s.Task("trans", "job")
		return tk.GetStatus() == "failed" || tk.GetStatus() == "exited"
	})

	_, err = d.Client().StopProject(d.Ctx(), connect.NewRequest(&v1.StopProjectRequest{Project: "trans"}))
	harness.NoError(t, err, "StopProject")
	w.WaitFor(t, "project stopped", func(s harness.State) bool { return s.Project("trans").GetStatus() == "stopped" })

	// The change stream alone must reconstruct every transition.
	seen := map[string]bool{}
	pids := map[int32]bool{}
	for _, e := range w.ServiceEvents("trans", "tick") {
		seen[e.Status] = true
		if e.Pid > 0 {
			pids[e.Pid] = true
		}
	}
	for _, want := range []string{"running", "stopping"} {
		if !seen[want] {
			t.Errorf("no %q change for tick; events:\n%v", want, w.ServiceEvents("trans", "tick"))
		}
	}
	if !seen["exited"] && !seen["failed"] && !seen["stopped"] {
		t.Errorf("no exit change for tick after kill")
	}
	if len(pids) < 2 {
		t.Errorf("restart did not surface a new pid through changes: %v", pids)
	}
	var taskStatuses []string
	for _, e := range w.Events() {
		if e.Kind == "task" && e.Name == "job" {
			taskStatuses = append(taskStatuses, e.Status)
		}
	}
	if len(taskStatuses) < 2 || taskStatuses[len(taskStatuses)-1] == "running" {
		t.Errorf("task transitions: %v", taskStatuses)
	}
	projectEvents := 0
	for _, e := range w.Events() {
		if e.Kind == "project" && e.Project == "trans" {
			projectEvents++
		}
	}
	if projectEvents < 2 {
		t.Errorf("only %d project changes", projectEvents)
	}
	if diff := harness.DiffServices(w.State(), d.State()); diff != "" {
		t.Errorf("watch state diverged from GetState:\n%s", diff)
	}

	// Removal is an explicit change.
	_, err = d.Client().RemoveProject(d.Ctx(), connect.NewRequest(&v1.RemoveProjectRequest{Project: "trans"}))
	harness.NoError(t, err, "RemoveProject")
	w.WaitFor(t, "project removed", func(s harness.State) bool { return s.Project("trans") == nil })
	removed := false
	for _, e := range w.Events() {
		if e.Kind == "removed" && e.Project == "trans" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("no removed change")
	}
	w.AssertNoViolations(t)
}

// O13: after a reconnect (here across a daemon crash) the new snapshot
// reflects the truth, including exits that happened while disconnected.
func TestLedger_O13_ReconnectSnapshotMatchesTruth(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("recon", pair, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w, "tick")
	tickPid := st.ServicePid("recon", "tick")
	snaps := w.Snapshots()

	d.KillHard()
	// Kill the service behind the daemon's back while nobody watches.
	_ = syscall.Kill(-tickPid, syscall.SIGKILL)
	sb.CLI("daemon", "start").MustSucceed(t)
	d.WaitReady()
	w.WaitSnapshots(t, snaps+1)
	harness.Eventually(t, "watch state converges with GetState", func(c *harness.C) {
		ws := w.State()
		if ws.ServicePid("recon", "tick") == tickPid && ws.Running("recon", "tick") {
			c.Fatalf("watch still shows the dead pid %d as running", tickPid)
		}
		if diff := harness.DiffServices(ws, d.State()); diff != "" {
			c.Errorf("diverged:\n%s", diff)
		}
	})
	w.AssertNoViolations(t)
}

// O13: a subscriber that stops reading either gets every change or a
// resync snapshot; it never silently ends up with stale state.
func TestLedger_O13_SlowSubscriberConverges(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("slow", `services:
  s1:
    run: {{fixture "ticker"}} -interval 1s
  s2:
    run: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := d.Client().Watch(ctx, connect.NewRequest(&v1.WatchRequest{}))
	harness.NoError(t, err, "Watch")
	defer func() { _ = stream.Close() }()
	if !stream.Receive() || stream.Msg().GetSnapshot() == nil {
		t.Fatalf("no snapshot: %v", stream.Err())
	}
	state := harness.StateFromSnapshot(stream.Msg().GetRevision(), stream.Msg().GetSnapshot())

	// Generate a burst of changes while the raw stream is not being read.
	for i := 0; i < 60; i++ {
		svc := []string{"s1", "s2"}[i%2]
		_, err := d.Client().RestartService(d.Ctx(), connect.NewRequest(&v1.RestartServiceRequest{Project: "slow", Service: svc}))
		harness.NoError(t, err, "RestartService")
	}
	truth := w.WaitFor(t, "settled", func(s harness.State) bool { return s.AllRunning("slow") })

	// Now drain: apply changes (or resync snapshots) until the state
	// matches, within a deadline.
	msgs := make(chan *v1.WatchResponse, 1024)
	go func() {
		defer close(msgs)
		for stream.Receive() {
			msgs <- stream.Msg()
		}
	}()
	resyncs := 0
	deadline := time.After(harness.Scale(15 * time.Second))
	for harness.DiffServices(state, truth) != "" {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatalf("stream ended while behind (%v) with stale state:\n%s", stream.Err(), harness.DiffServices(state, truth))
			}
			switch ev := m.GetEvent().(type) {
			case *v1.WatchResponse_Snapshot:
				resyncs++
				state = harness.StateFromSnapshot(m.GetRevision(), ev.Snapshot)
			case *v1.WatchResponse_Change:
				applyChange(&state, m)
			}
		case <-deadline:
			t.Fatalf("slow subscriber never converged:\n%s", harness.DiffServices(state, truth))
		}
	}
	t.Logf("converged with %d resync snapshot(s)", resyncs)
}

func applyChange(s *harness.State, m *v1.WatchResponse) {
	snap := &v1.Snapshot{Daemon: s.Daemon}
	for _, p := range s.Projects {
		snap.Projects = append(snap.Projects, p)
	}
	for _, sv := range s.Services {
		snap.Services = append(snap.Services, sv)
	}
	for _, tk := range s.Tasks {
		snap.Tasks = append(snap.Tasks, tk)
	}
	ch := m.GetChange()
	switch x := ch.GetChange().(type) {
	case *v1.Change_Service:
		out := snap.Services[:0]
		for _, sv := range snap.Services {
			if sv.GetProject() != x.Service.GetProject() || sv.GetName() != x.Service.GetName() {
				out = append(out, sv)
			}
		}
		snap.Services = append(out, x.Service)
	case *v1.Change_Removed:
		if x.Removed.GetKind() == "service" {
			out := snap.Services[:0]
			for _, sv := range snap.Services {
				if sv.GetProject() != x.Removed.GetProject() || sv.GetName() != x.Removed.GetName() {
					out = append(out, sv)
				}
			}
			snap.Services = out
		}
	}
	*s = harness.StateFromSnapshot(m.GetRevision(), snap)
}

// S15: an exited service is never flipped back to running by late events.
func TestLedger_S15_ExitedNeverFlipsToRunning(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("s15", `services:
  blip:
    run: {{fixture "exiter"}} -code 0
  anchor:
    run: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	for i := 0; i < 15; i++ {
		_, err := d.Client().StartService(d.Ctx(), connect.NewRequest(&v1.StartServiceRequest{Project: "s15", Service: "blip"}))
		harness.NoError(t, err, "StartService")
	}
	st := w.WaitFor(t, "blip exited", func(s harness.State) bool {
		sv := s.Service("s15", "blip")
		return sv.GetStatus() == "exited" && !harness.PidAlive(int(sv.GetPid()))
	})
	run := st.Service("s15", "blip").GetRun()
	harness.Consistently(t, "blip stays exited", 1500*time.Millisecond, func(c *harness.C) {
		sv := w.State().Service("s15", "blip")
		if sv.GetRun() == run && sv.GetStatus() != "exited" {
			c.Errorf("run %d flipped to %s", run, sv.GetStatus())
		}
	})
	if got := d.State().ServiceStatus("s15", "blip"); got != "exited" {
		t.Errorf("GetState says %q", got)
	}
	// Within one run, statuses never go backwards.
	lastByRun := map[int64]string{}
	for _, e := range w.ServiceEvents("s15", "blip") {
		if prev := lastByRun[e.Run]; (prev == "exited" || prev == "failed") && (e.Status == "running" || e.Status == "starting") {
			t.Errorf("run %d went %s -> %s", e.Run, prev, e.Status)
		}
		lastByRun[e.Run] = e.Status
	}
	w.AssertNoViolations(t)
}
