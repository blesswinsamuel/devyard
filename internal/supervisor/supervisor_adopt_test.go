package supervisor_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
)

// startOrphanGroup spawns `sh -c 'sleep 60'` in its own process group,
// detached from the test's supervision — the same shape a service has after
// the daemon that launched it exits. The returned pid is the group leader.
func startOrphanGroup(t *testing.T) (int, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphan group: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid, cmd
}

// writeState marshals snap and writes it to the locations' state.json.
func writeState(t *testing.T, locs *project.Locations, snap *supervisor.SupervisorStateSnapshot) {
	t.Helper()
	if err := locs.MkdirAll(); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(supervisor.StateFilePath(locs), data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// waitForServiceState polls the supervisor until name reports the wanted
// status (and, when wantPid > 0, that pid).
func waitForServiceState(t *testing.T, s *supervisor.Supervisor, name string, wantStatus supervisor.Status, wantPid int) supervisor.ServiceState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range s.States() {
			if st.Name != name {
				continue
			}
			if st.Status == wantStatus && (wantPid <= 0 || st.PID == wantPid) {
				return st
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service %s never reached status %q (pid want %d)", name, wantStatus, wantPid)
	return supervisor.ServiceState{}
}

// TestAdoptOrStartAdoptsLiveGroup verifies that a supervisor starting against
// a snapshot describing a live process group adopts it instead of spawning a
// second copy, and persists the adopted state.
func TestAdoptOrStartAdoptsLiveGroup(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	leader, _ := startOrphanGroup(t)

	writeState(t, locs, &supervisor.SupervisorStateSnapshot{
		Services: map[string]supervisor.ServiceStateSnapshot{
			"s1": {Name: "s1", Status: supervisor.StatusRunning, PID: leader, PGID: leader, StartedAt: time.Now().Add(-time.Minute)},
		},
		SavedAt: time.Now(),
	})

	file := fileWith(map[string]config.Service{
		"s1": {Command: "sleep 60"},
	})
	s, err := supervisor.New(supervisor.Options{
		Locations: locs,
		File:      file,
		Order:     []string{"s1"},
		BaseDir:   t.TempDir(),
		Backoff:   testBackoff(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	st := waitForServiceState(t, s, "s1", supervisor.StatusRunning, leader)
	if st.PID != leader {
		t.Errorf("adopted service pid = %d, want original %d", st.PID, leader)
	}

	// Adoption must be persisted immediately so a second restart sees it.
	snap, err := supervisor.LoadState(locs)
	if err != nil {
		t.Fatalf("LoadState after adopt: %v", err)
	}
	if snap == nil || snap.Services["s1"].PGID != leader {
		t.Errorf("adopted state not persisted (snap=%+v)", snap)
	}
}

// TestAdoptOrStartRejectsStaleSnapshots verifies that snapshots which do not
// describe a verifiably-live leader (dead pid, exited status, recycled pgid)
// fall back to a fresh start instead of a ghost adoption.
func TestAdoptOrStartRejectsStaleSnapshots(t *testing.T) {
	t.Parallel()

	leader, liveCmd := startOrphanGroup(t)

	cases := []struct {
		name string
		snap supervisor.ServiceStateSnapshot
	}{
		{"dead pid", supervisor.ServiceStateSnapshot{Name: "s1", Status: supervisor.StatusRunning, PID: 999999999, PGID: 999999999}},
		{"exited status", supervisor.ServiceStateSnapshot{Name: "s1", Status: supervisor.StatusExited, PID: leader, PGID: leader}},
		{"pgid mismatch", supervisor.ServiceStateSnapshot{Name: "s1", Status: supervisor.StatusRunning, PID: leader, PGID: leader + 1}},
		{"zero pid", supervisor.ServiceStateSnapshot{Name: "s1", Status: supervisor.StatusRunning, PID: 0, PGID: 0}},
		{"stopping status", supervisor.ServiceStateSnapshot{Name: "s1", Status: supervisor.StatusStopping, PID: leader, PGID: leader}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			locs := testLocations(t)
			writeState(t, locs, &supervisor.SupervisorStateSnapshot{
				Services: map[string]supervisor.ServiceStateSnapshot{"s1": tc.snap},
				SavedAt:  time.Now(),
			})

			file := fileWith(map[string]config.Service{
				"s1": {Command: "sleep 60"},
			})
			s, err := supervisor.New(supervisor.Options{
				Locations: locs,
				File:      file,
				Order:     []string{"s1"},
				BaseDir:   t.TempDir(),
				Backoff:   testBackoff(),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := s.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = s.Stop(context.Background()) }()

			st := waitForServiceState(t, s, "s1", supervisor.StatusRunning, 0)
			if st.PID == leader {
				t.Errorf("service pid = %d, adopted the snapshot's leader instead of starting fresh", st.PID)
			}
		})
	}

	_ = liveCmd.Process.Kill()
	_, _ = liveCmd.Process.Wait()
}

// TestAdoptedServiceExitRestartsWithoutPanic is the regression test for the
// adoption handoff crash: when an adopted process exits and the restart
// policy relaunches it, the supervisor must survive the eventual end of that
// relaunch (previously both adoptService and runService closed rt.done,
// panicking the daemon).
func TestAdoptedServiceExitRestartsWithoutPanic(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	leader, _ := startOrphanGroup(t)

	file := fileWith(map[string]config.Service{
		"s1": {Command: "sleep 60", Restart: config.RestartAlways},
	})
	writeState(t, locs, &supervisor.SupervisorStateSnapshot{
		Services: map[string]supervisor.ServiceStateSnapshot{
			"s1": {Name: "s1", Status: supervisor.StatusRunning, PID: leader, PGID: leader, StartedAt: time.Now()},
		},
		SavedAt: time.Now(),
	})

	s, err := supervisor.New(supervisor.Options{
		Locations:           locs,
		File:                file,
		Order:               []string{"s1"},
		BaseDir:             t.TempDir(),
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	waitForServiceState(t, s, "s1", supervisor.StatusRunning, leader)

	// Kill the adopted process group; the restart policy (always) must hand
	// off to a fresh supervise loop.
	if err := syscall.Kill(-leader, syscall.SIGKILL); err != nil {
		t.Fatalf("kill adopted group: %v", err)
	}

	// The relaunched run loop uses the supervisor's own launcher, so a new
	// pid appears after the 500ms adoption poll + backoff.
	var newPid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range s.States() {
			if st.Name == "s1" && st.Status == supervisor.StatusRunning && st.PID != 0 && st.PID != leader {
				newPid = st.PID
				break
			}
		}
		if newPid != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if newPid == 0 {
		t.Fatal("adopted service was not relaunched after its process exited")
	}

	// End the relaunched loop: with the old double-close handoff this
	// panicked ("close of closed channel") and took the daemon down.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestAdoptOrStartRestoresLazySelection verifies that a persisted lazy-start
// selection survives a daemon restart: only the selected service runs, the
// rest stay stopped.
func TestAdoptOrStartRestoresLazySelection(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	writeState(t, locs, &supervisor.SupervisorStateSnapshot{
		Services: map[string]supervisor.ServiceStateSnapshot{
			"s1": {Name: "s1", Status: supervisor.StatusStopped, PID: 0, PGID: 0},
			"s2": {Name: "s2", Status: supervisor.StatusStopped, PID: 0, PGID: 0},
		},
		Selected: []string{"s1"},
		SavedAt:  time.Now(),
	})

	file := fileWith(map[string]config.Service{
		"s1": {Command: "sleep 60"},
		"s2": {Command: "sleep 60"},
	})
	s, err := supervisor.New(supervisor.Options{
		Locations: locs,
		File:      file,
		Order:     []string{"s1", "s2"},
		BaseDir:   t.TempDir(),
		Backoff:   testBackoff(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	// s1 was dead in the snapshot, so it starts fresh.
	waitForServiceState(t, s, "s1", supervisor.StatusRunning, 0)
	// s2 was never selected — it must remain stopped, not launched.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		states := s.States()
		if s2 := stateByName(states, "s2"); s2 != nil && s2.Status == supervisor.StatusStopped && s2.PID == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("unselected service s2 was started; want it to stay stopped: %+v", s.States())
}

func stateByName(states []supervisor.ServiceState, name string) *supervisor.ServiceState {
	for i := range states {
		if states[i].Name == name {
			return &states[i]
		}
	}
	return nil
}

// waitForTaskState polls the supervisor until the named task appears in
// ListTasks and returns its state.
func waitForTaskState(t *testing.T, s *supervisor.Supervisor, name string) *protocol.TaskState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, act := range s.ListTasks() {
			if act.Name == name {
				return act
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s never appeared in ListTasks", name)
	return nil
}

// TestAdoptOrStartNormalizesDeadTask verifies that a snapshot recording a
// task as running whose process is long gone (the daemon that ran it
// exited) is restored as exited — so ps/web stop showing a ghost run and
// StopTask reports "not running" instead of signalling a recycled pid.
func TestAdoptOrStartNormalizesDeadTask(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}

	writeState(t, locs, &supervisor.SupervisorStateSnapshot{
		Tasks: map[string]supervisor.TaskStateSnapshot{
			"logs": {Name: "logs", Status: supervisor.StatusRunning, PID: dead.Process.Pid, StartedAt: time.Now().Add(-time.Hour)},
		},
		SavedAt: time.Now(),
	})

	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Tasks = map[string]config.Task{
		"logs": {Spec: config.TaskSpec{Command: "sleep 60", Shell: "sh"}},
	}
	s, err := supervisor.New(supervisor.Options{
		Locations: locs,
		File:      file,
		Order:     []string{"svc"},
		BaseDir:   t.TempDir(),
		Backoff:   testBackoff(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitForTaskState(t, s, "logs")
	if st.Status != string(supervisor.StatusExited) {
		t.Errorf("dead task restored with status %q, want exited", st.Status)
	}
	if st.Pid != 0 {
		t.Errorf("dead task restored with pid %d, want 0", st.Pid)
	}
	if err := s.StopTask("logs"); err == nil {
		t.Errorf("StopTask on normalized task: expected error, got nil")
	}
}

// TestAdoptOrStartAdoptsAliveTask verifies that a snapshot recording a
// task as running whose process group genuinely survived the daemon exit
// is adopted (still shown running) and remains stoppable.
func TestAdoptOrStartAdoptsAliveTask(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	leader, _ := startOrphanGroup(t)

	writeState(t, locs, &supervisor.SupervisorStateSnapshot{
		Tasks: map[string]supervisor.TaskStateSnapshot{
			"logs": {Name: "logs", Status: supervisor.StatusRunning, PID: leader, PGID: leader, StartedAt: time.Now().Add(-time.Hour)},
		},
		SavedAt: time.Now(),
	})

	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Tasks = map[string]config.Task{
		"logs": {Spec: config.TaskSpec{Command: "sleep 60", Shell: "sh"}},
	}
	s, err := supervisor.New(supervisor.Options{
		Locations: locs,
		File:      file,
		Order:     []string{"svc"},
		BaseDir:   t.TempDir(),
		Backoff:   testBackoff(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitForTaskState(t, s, "logs")
	if st.Status != string(supervisor.StatusRunning) || st.Pid != int32(leader) {
		t.Fatalf("alive task not adopted: status=%q pid=%d, want running/%d", st.Status, st.Pid, leader)
	}

	if err := s.StopTask("logs"); err != nil {
		t.Fatalf("StopTask on adopted task: %v", err)
	}

	st = waitForTaskState(t, s, "logs")
	if st.Status != string(supervisor.StatusExited) {
		t.Errorf("stopped adopted task status = %q, want exited", st.Status)
	}

	// The terminal state must be persisted so a restart doesn't resurrect it.
	snap, err := supervisor.LoadState(locs)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if act := snap.Tasks["logs"]; act.Status != supervisor.StatusExited || act.PID != 0 {
		t.Errorf("persisted adopted task = %+v, want exited/pid 0", act)
	}
}
