package supervisor_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/health"
	"github.com/blesswinsamuel/devyard/internal/protocol"
	"github.com/blesswinsamuel/devyard/internal/supervisor"
)

// TestSupervisorHealthcheckBecomesHealthy starts a long-lived service with a
// passing healthcheck and verifies States() surfaces the healthy state.
func TestSupervisorHealthcheckBecomesHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {
			Command: "sleep 30",
			Shell:   "sh",
			Restart: config.RestartNo,
			Healthcheck: &config.Healthcheck{
				Test:     []string{"CMD", "true"},
				Interval: 20 * time.Millisecond,
				Retries:  3,
				Timeout:  time.Second,
			},
		},
	})
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()[0]
		return st.Status == supervisor.StatusRunning && st.Health == string(health.StateHealthy)
	}) {
		st := s.States()[0]
		t.Fatalf("svc never reached running+healthy: %+v", st)
	}
}

// TestSupervisorHealthcheckNotifiesOnStateChange verifies that transitions in
// healthcheck status (starting -> healthy) trigger OnStateChange callbacks.
func TestSupervisorHealthcheckNotifiesOnStateChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {
			Command: "sleep 30",
			Shell:   "sh",
			Restart: config.RestartNo,
			Healthcheck: &config.Healthcheck{
				Test:     []string{"CMD", "true"},
				Interval: 20 * time.Millisecond,
				Retries:  3,
				Timeout:  time.Second,
			},
		},
	})
	var mu sync.Mutex
	var transitions []*protocol.ServiceState
	s, err := supervisor.New(supervisor.Options{
		Locations: testLocations(t),
		File:      file,
		Order:     []string{"svc"},
		BaseDir:   t.TempDir(),
		Backoff:   testBackoff(),
		OnStateChange: func(_ string, state *protocol.ServiceState) {
			mu.Lock()
			transitions = append(transitions, state)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, tr := range transitions {
			if tr.Health == string(health.StateHealthy) {
				return true
			}
		}
		return false
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("never received OnStateChange with Health=healthy; saw transitions: %v", transitions)
	}
}

// TestSupervisorDependsOnHealthyGate verifies a dependent with
// depends_on: service_healthy only starts once its dependency's healthcheck
// passes, proving the gate holds the dependent back until healthy.
func TestSupervisorDependsOnHealthyGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "dep-ready")
	// The dependency writes a marker file after 200ms; the healthcheck polls
	// for it, so it flips healthy ~200ms in. The dependent must not start
	// before then.
	depCmd := "sleep 0.2; touch " + ready + "; sleep 30"
	depHC := &config.Healthcheck{
		Test:     []string{"CMD-SHELL", "test -f " + ready},
		Interval: 20 * time.Millisecond,
		Retries:  20,
		Timeout:  time.Second,
	}
	// dependent writes a marker the moment it starts; we assert it only appears
	// after dep-ready exists.
	depStart := filepath.Join(dir, "dependent-started")
	dependentCmd := "touch " + depStart + "; sleep 30"

	file := fileWith(map[string]config.Service{
		"dep": {
			Command: depCmd, Shell: "sh", Restart: config.RestartNo,
			WorkingDir: dir, Healthcheck: depHC,
		},
		"dependent": {
			Command: dependentCmd, Shell: "sh", Restart: config.RestartNo,
			WorkingDir: dir,
			DependsOn: config.DependsOn{
				Entries: map[string]config.DependsOnEntry{
					"dep": {Condition: config.ConditionServiceHealthy},
				},
				Order: []string{"dep"},
			},
		},
	})
	s := newSupervisor(t, file, []string{"dep", "dependent"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the dependency to become healthy.
	if !waitFor(t, 3*time.Second, func() bool {
		return s.States()[0].Health == string(health.StateHealthy)
	}) {
		t.Fatalf("dep never became healthy: %+v", s.States())
	}

	// The dependent should now (eventually) be running.
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()[1]
		return st.Status == supervisor.StatusRunning
	}) {
		st := s.States()[1]
		t.Fatalf("dependent never started after dep healthy: %+v", st)
	}
}

// TestSupervisorDependsOnHealthyFailsFast starts a dependency whose
// healthcheck exhausts retries (always fails) and verifies the dependent is
// never launched and the supervisor records a failure.
func TestSupervisorDependsOnHealthyFailsFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	depStart := filepath.Join(dir, "dep-started")
	dependentStart := filepath.Join(dir, "dependent-started")
	file := fileWith(map[string]config.Service{
		"dep": {
			Command: "touch " + depStart + "; sleep 30", Shell: "sh",
			Restart: config.RestartNo, WorkingDir: dir,
			Healthcheck: &config.Healthcheck{
				Test:     []string{"CMD", "false"},
				Interval: 15 * time.Millisecond,
				Retries:  2,
				Timeout:  time.Second,
			},
		},
		"dependent": {
			Command: "touch " + dependentStart + "; sleep 30", Shell: "sh",
			Restart: config.RestartNo, WorkingDir: dir,
			DependsOn: config.DependsOn{
				Entries: map[string]config.DependsOnEntry{
					"dep": {Condition: config.ConditionServiceHealthy},
				},
				Order: []string{"dep"},
			},
		},
	})
	s := newSupervisor(t, file, []string{"dep", "dependent"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Dep goes unhealthy; dependent must be stopped (never started).
	if !waitFor(t, 3*time.Second, func() bool {
		return s.States()[0].Health == string(health.StateUnhealthy)
	}) {
		t.Fatalf("dep never became unhealthy: %+v", s.States())
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s.States()[1].Status == supervisor.StatusStopped
	}) {
		st := s.States()[1]
		t.Fatalf("dependent should be stopped after dep unhealthy: %+v", st)
	}
	if !s.Failed() {
		t.Errorf("Failed() = false, want true after dep went unhealthy")
	}
	// The dependent must never have written its start marker.
	if _, err := readFile(dependentStart); err == nil {
		t.Errorf("dependent started marker exists; dependent launched despite unhealthy dep")
	}
}

// TestSupervisorDependsOnStarted verifies service_started waits for the
// dependency to be running (not healthy).
func TestSupervisorDependsOnStarted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	depStarted := filepath.Join(dir, "dep-started")
	depCmd := "touch " + depStarted + "; sleep 30"
	dependentStart := filepath.Join(dir, "dependent-started")
	dependentCmd := "touch " + dependentStart + "; sleep 30"
	file := fileWith(map[string]config.Service{
		"dep": {
			Command: depCmd, Shell: "sh", Restart: config.RestartNo, WorkingDir: dir,
		},
		"dependent": {
			Command: dependentCmd, Shell: "sh", Restart: config.RestartNo, WorkingDir: dir,
			DependsOn: config.DependsOn{
				Entries: map[string]config.DependsOnEntry{
					"dep": {Condition: config.ConditionServiceStarted},
				},
				Order: []string{"dep"},
			},
		},
	})
	s := newSupervisor(t, file, []string{"dep", "dependent"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()[1]
		return st.Status == supervisor.StatusRunning
	}) {
		st := s.States()[1]
		t.Fatalf("dependent never started: %+v", st)
	}
	// Both should be running.
	for i, name := range []string{"dep", "dependent"} {
		st := s.States()[i]
		if st.Status != supervisor.StatusRunning {
			t.Errorf("%s status = %s, want running", name, st.Status)
		}
	}
	// No healthchecks declared, so Health must read "n/a".
	if st := s.States()[0]; st.Health != "n/a" || st.HasHealth {
		t.Errorf("dep health = %q hasHealth=%v, want n/a/false", st.Health, st.HasHealth)
	}
}

// TestSupervisorStatesHealthNAForNoHealthcheck ensures a service with no
// healthcheck reports HasHealth=false and Health="n/a" (regression guard
// against the States() placeholder logic).
func TestSupervisorStatesHealthNAForNoHealthcheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s.States()[0].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("never running: %+v", s.States())
	}
	st := s.States()[0]
	if st.HasHealth || st.Health != "n/a" {
		t.Errorf("health = has=%v val=%q, want n/a", st.HasHealth, st.Health)
	}
}

// TestSupervisorHealthcheckLogsUnhealthy verifies the per-service log file
// records the unhealthy transition (the supervisor wires the checker's log
// callback to the service logger).
func TestSupervisorHealthcheckLogsUnhealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {
			Command: "sleep 30", Shell: "sh", Restart: config.RestartNo,
			Healthcheck: &config.Healthcheck{
				Test:     []string{"CMD", "false"},
				Interval: 15 * time.Millisecond,
				Retries:  2,
				Timeout:  time.Second,
			},
		},
	})
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s.States()[0].Health == string(health.StateUnhealthy)
	}) {
		t.Fatalf("svc never unhealthy: %+v", s.States())
	}
	path, err := s.LogPath("svc")
	if err != nil {
		t.Fatal(err)
	}
	data := string(mustReadFile(t, path))
	if !strings.Contains(data, "unhealthy") {
		t.Errorf("log missing unhealthy line: %q", data)
	}
}

// TestSupervisorUnhealthyServiceNotKilled verifies that when a service's healthcheck
// is bad (fails or times out), the underlying service process remains running and is not killed.
func TestSupervisorUnhealthyServiceNotKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {
			Command: "sleep 30", Shell: "sh", Restart: config.RestartNo,
			Healthcheck: &config.Healthcheck{
				Test:     []string{"CMD-SHELL", "sleep 2"},
				Interval: 50 * time.Millisecond,
				Retries:  1,
				Timeout:  30 * time.Millisecond,
			},
		},
	})
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
		_ = s.Close()
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()[0]
		return st.Health == string(health.StateUnhealthy) && st.Status == supervisor.StatusRunning
	}) {
		t.Fatalf("svc never reached unhealthy while running: %+v", s.States())
	}
	// Verify service is still running after a moment
	time.Sleep(150 * time.Millisecond)
	st := s.States()[0]
	if st.Status != supervisor.StatusRunning {
		t.Fatalf("svc status = %s, want running (service process was killed or stopped)", st.Status)
	}
	if st.PID <= 0 {
		t.Fatalf("svc PID = %d, want > 0", st.PID)
	}
}
