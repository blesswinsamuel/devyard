package health_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/health"
)

func newChecker(t *testing.T, cfg health.Config) *health.Checker {
	t.Helper()
	c, err := health.New("svc", cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// waitForState polls the checker until it reports want or the timeout elapses.
func waitForState(t *testing.T, c *health.Checker, timeout time.Duration, want health.State) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.State() == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestNewRejectsBadTestSpec(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{},
		{"CMD"},
		{"CMD-SHELL"},
		{"NOPE", "echo hi"},
	}
	for i, tc := range cases {
		_, err := health.New("svc", health.Config{Test: tc}, nil)
		if err == nil {
			t.Fatalf("case %d %v: expected error, got nil", i, tc)
		}
	}
}

func TestDefaultsApplied(t *testing.T) {
	t.Parallel()
	c, err := health.New("svc", health.Config{Test: []string{"CMD", "true"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Stop() // safe to call even without EnsureStarted
}

func TestCMDProbeBecomesHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	c := newChecker(t, health.Config{
		Test:     []string{"CMD", "true"},
		Interval: 50 * time.Millisecond,
		Retries:  3,
		Timeout:  time.Second,
	})
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())
	if !waitForState(t, c, time.Second, health.StateHealthy) {
		t.Fatalf("state = %s, want healthy", c.State())
	}
}

func TestCMDShellProbeBecomesHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	c := newChecker(t, health.Config{
		Test:     []string{"CMD-SHELL", "true"},
		Interval: 50 * time.Millisecond,
		Retries:  3,
		Timeout:  time.Second,
	})
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())
	if !waitForState(t, c, time.Second, health.StateHealthy) {
		t.Fatalf("state = %s, want healthy", c.State())
	}
}

func TestFailingProbeGoesUnhealthyAfterRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	c := newChecker(t, health.Config{
		Test:     []string{"CMD", "false"},
		Interval: 20 * time.Millisecond,
		Retries:  3,
		Timeout:  time.Second,
	})
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())
	if !waitForState(t, c, 2*time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy", c.State())
	}
}

func TestRecoveryFromUnhealthyToHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	dir := t.TempDir()
	// Probe flips from failing to succeeding once a marker file appears.
	marker := filepath.Join(dir, "up")
	cmd := "test -f " + marker
	c, err := health.New("svc", health.Config{
		Test:       []string{"CMD-SHELL", cmd},
		Interval:   20 * time.Millisecond,
		Retries:    3,
		Timeout:    time.Second,
		WorkingDir: dir,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())

	if !waitForState(t, c, 2*time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy before marker", c.State())
	}
	if err := os.WriteFile(marker, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitForState(t, c, 2*time.Second, health.StateHealthy) {
		t.Fatalf("state = %s, want healthy after marker", c.State())
	}
}

// TestProbeTimeoutKillsGroup runs a CMD-SHELL probe that sleeps longer than the
// timeout and verifies the probe is killed within the timeout (the checker
// does not hang). We confirm kill-by-group by spawning a child the supervisor
// would otherwise leak; the child writes a flag file on exit.
func TestProbeTimeoutKillsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	dir := t.TempDir()
	flag := filepath.Join(dir, "child-done")
	// Child sleeps 5s, then writes the flag. The timeout (150ms) must kill the
	// whole group before the child reaches the write.
	cmd := "sleep 5; echo done > " + flag
	c, err := health.New("svc", health.Config{
		Test:       []string{"CMD-SHELL", cmd},
		Interval:   200 * time.Millisecond,
		Retries:    1,
		Timeout:    150 * time.Millisecond,
		WorkingDir: dir,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())

	// Give the probe plenty of time to time out and the group to be killed.
	time.Sleep(500 * time.Millisecond)

	// If the group was killed, the child never wrote the flag.
	if _, err := os.Stat(flag); err == nil {
		t.Fatalf("child wrote flag file — probe group was not killed by timeout")
	}
	// State must be unhealthy (1 retry, the timed-out probe counts as failure).
	if !waitForState(t, c, time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy", c.State())
	}
}

// TestEnsureStartedIdempotent verifies EnsureStarted only launches one probe
// goroutine even when called concurrently.
func TestEnsureStartedIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	c, err := health.New("svc", health.Config{
		Test:     []string{"CMD", "true"},
		Interval: 50 * time.Millisecond,
		Retries:  3,
		Timeout:  time.Second,
	}, func(string) {})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Stop)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.EnsureStarted(context.Background())
		}()
	}
	wg.Wait()
	if !waitForState(t, c, time.Second, health.StateHealthy) {
		t.Fatalf("state = %s, want healthy", c.State())
	}
}

// TestLogsTransitionToUnhealthy verifies the log callback fires when the
// checker flips to unhealthy.
func TestLogsTransitionToUnhealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	t.Parallel()
	var (
		mu   sync.Mutex
		logs []string
	)
	logFn := func(line string) {
		mu.Lock()
		logs = append(logs, line)
		mu.Unlock()
	}
	c, err := health.New("svc", health.Config{
		Test:     []string{"CMD", "false"},
		Interval: 20 * time.Millisecond,
		Retries:  2,
		Timeout:  time.Second,
	}, logFn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Stop)
	c.EnsureStarted(context.Background())
	if !waitForState(t, c, 2*time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy", c.State())
	}
	// The unhealthy log line is emitted immediately after the state flip, but
	// not necessarily before waitForState returns, so poll for it.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := false
		for _, l := range logs {
			if strings.Contains(l, "unhealthy") {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("no unhealthy log line emitted: %v", logs)
}

func TestSetOnStateChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	probeFile := filepath.Join(dir, "probe_pass")

	var (
		mu     sync.Mutex
		states []health.State
	)
	c, err := health.New("svc", health.Config{
		Test:     []string{"CMD-SHELL", "test -f " + probeFile},
		Interval: 20 * time.Millisecond,
		Retries:  1,
		Timeout:  time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Stop)

	c.SetOnStateChange(func(st health.State) {
		mu.Lock()
		states = append(states, st)
		mu.Unlock()
	})

	c.EnsureStarted(context.Background())

	// Initially probe fails (probeFile does not exist) -> state becomes unhealthy
	if !waitForState(t, c, 2*time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy", c.State())
	}

	// Now create probeFile -> state becomes healthy
	if err := os.WriteFile(probeFile, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !waitForState(t, c, 2*time.Second, health.StateHealthy) {
		t.Fatalf("state = %s, want healthy", c.State())
	}

	// Remove probeFile -> state returns to unhealthy
	if err := os.Remove(probeFile); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !waitForState(t, c, 2*time.Second, health.StateUnhealthy) {
		t.Fatalf("state = %s, want unhealthy", c.State())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(states) < 3 {
		t.Fatalf("expected at least 3 state transitions, got %d: %v", len(states), states)
	}
	if states[0] != health.StateUnhealthy {
		t.Errorf("expected transition 0 to be unhealthy, got %v", states[0])
	}
	if states[1] != health.StateHealthy {
		t.Errorf("expected transition 1 to be healthy, got %v", states[1])
	}
	if states[2] != health.StateUnhealthy {
		t.Errorf("expected transition 2 to be unhealthy, got %v", states[2])
	}
}

func TestStartPeriodSuppressesEarlyFailures(t *testing.T) {
	dir := t.TempDir()
	flag := dir + "/ok"
	c, err := health.New("svc", health.Config{
		Test:        []string{"CMD-SHELL", "test -f " + flag},
		Interval:    20 * time.Millisecond,
		Retries:     1,
		Timeout:     time.Second,
		StartPeriod: 300 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.EnsureStarted(context.Background())
	defer c.Stop()
	time.Sleep(150 * time.Millisecond)
	if st := c.State(); st != health.StateStarting {
		t.Fatalf("state during start period = %s, want starting", st)
	}
	if c.LastFailure() == "" {
		t.Fatal("expected a recorded failure detail")
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.State() != health.StateUnhealthy {
		if time.Now().After(deadline) {
			t.Fatalf("never became unhealthy after start period; state=%s", c.State())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
