package supervisor_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
)

// testBackoff is a fast backoff used by tests so restart loops settle quickly.
func testBackoff() supervisor.BackoffConfig {
	return supervisor.BackoffConfig{
		Base:        2 * time.Millisecond,
		Factor:      2,
		Cap:         20 * time.Millisecond,
		MaxAttempts: 10,
		Jitter:      0,
	}
}

func testLocations(t *testing.T) *project.Locations {
	t.Helper()
	dir := t.TempDir()
	return &project.Locations{
		Name:    "test",
		Runtime: filepath.Join(dir, "runtime"),
		State:   filepath.Join(dir, "state"),
		LogsDir: filepath.Join(dir, "state", "logs"),
	}
}

func newSupervisor(t *testing.T, file *config.File, order []string) *supervisor.Supervisor {
	t.Helper()
	return newSupervisorWithEnv(t, file, order, nil)
}

func newSupervisorWithEnv(t *testing.T, file *config.File, order []string, env []string) *supervisor.Supervisor {
	t.Helper()
	s, err := supervisor.New(supervisor.Options{
		Locations:           testLocations(t),
		File:                file,
		Order:               order,
		BaseDir:             t.TempDir(),
		Foreground:          false,
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
		Env:                 env,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func fileWith(services map[string]config.Service) *config.File {
	return &config.File{Version: "1", Name: "test", Services: services}
}

func TestBackoffDelayGrowthAndCap(t *testing.T) {
	t.Parallel()
	b := supervisor.BackoffConfig{Base: 10 * time.Millisecond, Factor: 2, Cap: 100 * time.Millisecond, Jitter: 0}
	if d := supervisor.BackoffDelayForTest(b, 1); d != 10*time.Millisecond {
		t.Fatalf("attempt 1 = %v, want 10ms", d)
	}
	if d := supervisor.BackoffDelayForTest(b, 2); d != 20*time.Millisecond {
		t.Fatalf("attempt 2 = %v, want 20ms", d)
	}
	if d := supervisor.BackoffDelayForTest(b, 5); d != 100*time.Millisecond {
		t.Fatalf("attempt 5 = %v, want 100ms (capped)", d)
	}
	if d := supervisor.BackoffDelayForTest(b, 99); d != 100*time.Millisecond {
		t.Fatalf("attempt 99 = %v, want 100ms (capped)", d)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	t.Parallel()
	b := supervisor.BackoffConfig{Base: 100 * time.Millisecond, Factor: 1, Cap: time.Second, Jitter: 0.2}
	for i := 0; i < 50; i++ {
		d := supervisor.BackoffDelayForTest(b, 1)
		lo := 80 * time.Millisecond
		hi := 120 * time.Millisecond
		if d < lo || d > hi {
			t.Fatalf("delay = %v, want within [%v,%v]", d, lo, hi)
		}
	}
}

func TestShouldRestart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		policy   config.RestartPolicy
		exitCode int
		want     bool
	}{
		{config.RestartNo, 0, false},
		{config.RestartNo, 1, false},
		{config.RestartAlways, 0, true},
		{config.RestartAlways, 1, true},
		{config.RestartOnFailure, 0, false},
		{config.RestartOnFailure, 1, true},
		{config.RestartUnlessStopped, 0, true},
		{config.RestartUnlessStopped, 1, true},
	}
	for i, c := range cases {
		got := supervisor.ShouldRestartForTest(c.policy, c.exitCode)
		if got != c.want {
			t.Fatalf("case %d: ShouldRestart(%s,%d) = %v, want %v", i, c.policy, c.exitCode, got, c.want)
		}
	}
}

func TestMergeEnvAdditive(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/usr/bin", "HOME=/u", "FOO=parent"}
	svc := map[string]string{"FOO": "child", "BAR": "new"}
	out := supervisor.MergeEnvForTest(parent, svc)
	got := map[string]string{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["FOO"] != "child" {
		t.Errorf("FOO = %q, want child (svc overlays parent)", got["FOO"])
	}
	if got["BAR"] != "new" {
		t.Errorf("BAR = %q, want new", got["BAR"])
	}
	if got["PATH"] != "/usr/bin" || got["HOME"] != "/u" {
		t.Errorf("parent env dropped: PATH=%q HOME=%q", got["PATH"], got["HOME"])
	}
}

func TestDefaultColorEnvValues(t *testing.T) {
	t.Parallel()
	env := supervisor.DefaultColorEnvForTest()
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["FORCE_COLOR"] != "1" {
		t.Errorf("FORCE_COLOR = %q, want 1", got["FORCE_COLOR"])
	}
	if got["CLICOLOR"] != "1" {
		t.Errorf("CLICOLOR = %q, want 1", got["CLICOLOR"])
	}
	if got["CLICOLOR_FORCE"] != "1" {
		t.Errorf("CLICOLOR_FORCE = %q, want 1", got["CLICOLOR_FORCE"])
	}
	if got["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", got["TERM"])
	}
}

func TestColorEnvOverrides(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/usr/bin", "TERM=dumb"}
	svc := map[string]string{"CLICOLOR_FORCE": "0"}
	base := supervisor.ApplyEnvDefaultsForTest(parent, supervisor.DefaultColorEnvForTest())
	out := supervisor.MergeEnvForTest(base, svc)
	got := map[string]string{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	// Parent env should override color defaults.
	if got["TERM"] != "dumb" {
		t.Errorf("TERM = %q, want dumb (parent wins over color default)", got["TERM"])
	}
	// Service env should override color defaults.
	if got["CLICOLOR_FORCE"] != "0" {
		t.Errorf("CLICOLOR_FORCE = %q, want 0 (svc wins over color default)", got["CLICOLOR_FORCE"])
	}
	// Color defaults that are not overridden should be present.
	if got["CLICOLOR"] != "1" {
		t.Errorf("CLICOLOR = %q, want 1 (default applied)", got["CLICOLOR"])
	}
}

// TestSupervisorColorEnv verifies a launched process receives the color
// environment variables.
func TestSupervisorColorEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"envcheck": {Command: "echo CLICOLOR=$CLICOLOR CLICOLOR_FORCE=$CLICOLOR_FORCE TERM=$TERM", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"envcheck"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	path, err := s.LogPath("envcheck")
	if err != nil {
		t.Fatal(err)
	}
	data := string(mustReadFile(t, path))
	if !strings.Contains(data, "CLICOLOR=1 CLICOLOR_FORCE=1 TERM=xterm-256color") {
		t.Errorf("log = %q, want color env vars present", data)
	}
}

// TestSupervisorColorEnvServiceOverride verifies service env overrides color defaults.
func TestSupervisorColorEnvServiceOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"envcheck": {
			Command: "echo CLICOLOR=$CLICOLOR",
			Shell:   "sh",
			Restart: config.RestartNo,
			Env:     map[string]string{"CLICOLOR": "0"},
		},
	})
	s := newSupervisor(t, file, []string{"envcheck"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	path, err := s.LogPath("envcheck")
	if err != nil {
		t.Fatal(err)
	}
	data := string(mustReadFile(t, path))
	if !strings.Contains(data, "CLICOLOR=0") {
		t.Errorf("log = %q, want CLICOLOR=0 (service override)", data)
	}
}

// TestSupervisorEnvFileLayer verifies env-file variables (Options.Env) reach
// the child environment under the service env.
func TestSupervisorEnvFileLayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"envcheck": {
			Command: "echo FROM_DOTENV=$FROM_DOTENV FROM_PARENT=$FROM_PARENT OVERRIDDEN=$OVERRIDDEN",
			Shell:   "sh",
			Restart: config.RestartNo,
			Env:     map[string]string{"OVERRIDDEN": "svc"},
		},
	})
	s := newSupervisorWithEnv(t, file, []string{"envcheck"}, []string{
		"FROM_DOTENV=dotenv",
		"FROM_PARENT=parent",
		"OVERRIDDEN=dotenv",
	})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	path, err := s.LogPath("envcheck")
	if err != nil {
		t.Fatal(err)
	}
	data := string(mustReadFile(t, path))
	for _, want := range []string{
		"FROM_DOTENV=dotenv",
		"FROM_PARENT=parent",
		"OVERRIDDEN=svc",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("log = %q, want %q present", data, want)
		}
	}
}

func TestResolveWorkingDir(t *testing.T) {
	t.Parallel()
	if got := supervisor.ResolveWorkingDirForTest("/base", "./api"); got != "/base/api" {
		t.Errorf("relative: %q, want /base/api", got)
	}
	if got := supervisor.ResolveWorkingDirForTest("/base", "/abs/x"); got != "/abs/x" {
		t.Errorf("absolute: %q, want /abs/x", got)
	}
	if got := supervisor.ResolveWorkingDirForTest("/base", ""); got != "/base" {
		t.Errorf("empty: %q, want /base (default to config dir)", got)
	}
}

func TestExitCodeFrom(t *testing.T) {
	t.Parallel()
	if code := supervisor.ExitCodeFromForTest(nil); code != 0 {
		t.Errorf("nil err: code %d, want 0", code)
	}
	if err := exec.Command("sh", "-c", "exit 5").Run(); err != nil {
		if code := supervisor.ExitCodeFromForTest(err); code != 5 {
			t.Errorf("exit 5: code %d, want 5", code)
		}
	}
	if err := exec.Command("sh", "-c", "kill -TERM $$").Run(); err != nil {
		if code := supervisor.ExitCodeFromForTest(err); code != -1 {
			t.Errorf("signal: code %d, want -1", code)
		}
	}
}

// TestSupervisorRunExit verifies a short-lived service runs, its output lands
// in the per-service log file, and Wait returns once it exits.
func TestSupervisorRunExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"echoer": {Command: "echo hello-echoer", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"echoer"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	states := s.States()
	if len(states) != 1 || states[0].Name != "echoer" {
		t.Fatalf("states: %+v", states)
	}
	if states[0].Status != supervisor.StatusExited {
		t.Errorf("status = %s, want exited", states[0].Status)
	}
	if states[0].ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", states[0].ExitCode)
	}

	path, err := s.LogPath("echoer")
	if err != nil {
		t.Fatal(err)
	}
	data := mustReadFile(t, path)
	if !strings.Contains(string(data), "hello-echoer") {
		t.Errorf("log = %q, want it to contain hello-echoer", string(data))
	}
}

// TestSupervisorRestartOnFailure runs a service that fails twice then
// succeeds and asserts the supervisor retried it.
func TestSupervisorRestartOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "n")
	// Each run increments a counter; exits non-zero until n>=3, then exits 0.
	cmd := "n=$(cat " + counter + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + counter + "; echo attempt $n; test $n -ge 3"
	file := fileWith(map[string]config.Service{
		"flaky": {Command: cmd, Shell: "sh", Restart: config.RestartOnFailure, WorkingDir: dir},
	})
	s := newSupervisor(t, file, []string{"flaky"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	states := s.States()
	if states[0].Status != supervisor.StatusExited {
		t.Errorf("status = %s, want exited", states[0].Status)
	}
	if states[0].ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", states[0].ExitCode)
	}
	if states[0].Restarts < 2 {
		t.Errorf("restarts = %d, want >= 2", states[0].Restarts)
	}
	path, err := s.LogPath("flaky")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustReadFile(t, path)), "attempt 3") {
		t.Errorf("log missing attempt 3")
	}
}

// TestSupervisorStopService starts a long sleep and verifies StopService tears
// it down and the run loop exits.
func TestSupervisorStopService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"sleeper": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"sleeper"})
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
		t.Fatalf("sleeper never reached running: %+v", s.States())
	}

	if err := s.StopService("sleeper", false); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	st := s.States()[0]
	if st.Status != supervisor.StatusStopped {
		t.Errorf("status = %s, want stopped", st.Status)
	}
	if st.PID != 0 {
		t.Errorf("pid = %d, want 0 after stop", st.PID)
	}
}

// TestSupervisorKillService starts a long sleep and verifies KillService sends
// the requested signal and the run loop exits.
func TestSupervisorKillService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"sleeper": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"sleeper"})
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
		t.Fatalf("sleeper never reached running: %+v", s.States())
	}

	if err := s.KillService("sleeper", "SIGTERM"); err != nil {
		t.Fatalf("KillService: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s.States()[0].Status == supervisor.StatusStopped
	}) {
		t.Fatalf("sleeper not stopped after kill: %+v", s.States())
	}
}

func TestKillServiceUnknownService(t *testing.T) {
	s := newSupervisor(t, fileWith(map[string]config.Service{
		"sleeper": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	}), []string{"sleeper"})
	if err := s.KillService("nope", "SIGKILL"); err == nil {
		t.Fatalf("KillService for unknown service: expected error, got nil")
	}
}

func TestKillServiceBadSignal(t *testing.T) {
	s := newSupervisor(t, fileWith(map[string]config.Service{
		"sleeper": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	}), []string{"sleeper"})
	if err := s.KillService("sleeper", "SIGBOGUS"); err == nil {
		t.Fatalf("KillService with bad signal: expected error, got nil")
	}
}

func TestParseSignal(t *testing.T) {
	for _, tc := range []struct {
		name string
		want syscall.Signal
		ok   bool
	}{
		{name: "", want: syscall.SIGKILL, ok: true},
		{name: "SIGKILL", want: syscall.SIGKILL, ok: true},
		{name: "KILL", want: syscall.SIGKILL, ok: true},
		{name: "sigterm", want: syscall.SIGTERM, ok: true},
		{name: "SIGTERM", want: syscall.SIGTERM, ok: true},
		{name: "TERM", want: syscall.SIGTERM, ok: true},
		{name: "SIGINT", want: syscall.SIGINT, ok: true},
		{name: "INT", want: syscall.SIGINT, ok: true},
		{name: "SIGHUP", want: syscall.SIGHUP, ok: true},
		{name: "9", want: syscall.SIGKILL, ok: true},
		{name: "15", want: syscall.SIGTERM, ok: true},
		{name: "SIGBOGUS", ok: false},
		{name: "BOGUS", ok: false},
		{name: "0", ok: false},
		{name: "32", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := supervisor.ParseSignalForTest(tc.name)
			if tc.ok {
				if err != nil {
					t.Fatalf("ParseSignal(%q): %v", tc.name, err)
				}
				if got != tc.want {
					t.Errorf("ParseSignal(%q) = %v, want %v", tc.name, got, tc.want)
				}
			} else if err == nil {
				t.Errorf("ParseSignal(%q): expected error, got %v", tc.name, got)
			}
		})
	}
}

// TestSupervisorUnlessStoppedMarker verifies an explicit stop persists a
// marker that prevents auto-resume on the next supervisor.
func TestSupervisorUnlessStoppedMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	locs := testLocations(t)
	file := fileWith(map[string]config.Service{
		"long": {Command: "sleep 30", Shell: "sh", Restart: config.RestartUnlessStopped},
	})
	mk := func() *supervisor.Supervisor {
		t.Helper()
		s, err := supervisor.New(supervisor.Options{
			Locations:           locs,
			File:                file,
			Order:               []string{"long"},
			BaseDir:             t.TempDir(),
			Foreground:          false,
			Backoff:             testBackoff(),
			GracefulStopTimeout: 2 * time.Second,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	}

	s1 := mk()
	if err := s1.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s1.States()[0].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("never running: %+v", s1.States())
	}
	if err := s1.StopService("long", true); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s1.Stop(ctx)
	_ = s1.Close()

	marker := filepath.Join(locs.State, "long.stopped")
	if !fileExists(marker) {
		t.Fatalf("marker not written at %s", marker)
	}

	// Second supervisor should skip the service because of the marker.
	s2 := mk()
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("Start s2: %v", err)
	}
	// The service is skipped synchronously during Start, so its state is
	// already Stopped by the time Start returns.
	st := s2.States()[0]
	if st.Status != supervisor.StatusStopped {
		t.Errorf("status = %s, want stopped (marker should skip)", st.Status)
	}
	if st.PID != 0 {
		t.Errorf("pid = %d, want 0 (should not be running)", st.PID)
	}
	_ = s2.Stop(ctx)
}

func TestSupervisorRestartAfterStopErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		st := s.States()
		return len(st) == 1 && st[0].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("never running: %+v", s.States())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Restart("svc"); err == nil {
		t.Fatalf("Restart after Stop: want error, got nil")
	}
	if err := s.StartStopped(); err == nil {
		t.Fatalf("StartStopped after Stop: want error, got nil")
	}
}

// TestSupervisorUnlessStoppedSkipsDependents verifies that when a unless-stopped
// service is skipped at Start because of a persisted stop marker (daemon
// autostart path), its dependents are skipped transitively (not reported as
// failures), the supervisor does NOT record a failure, and the per-service
// logs explain the skip. This is the UX fix for the old behavior where
// dependents died with a misleading "dependency exited before satisfying"
// message and startup exited non-zero.
func TestSupervisorUnlessStoppedSkipsDependents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	locs := testLocations(t)
	if err := locs.MkdirAll(); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Pre-create the stop marker so the api service is skipped at Start.
	apiMarker := filepath.Join(locs.State, "api.stopped")
	if err := os.WriteFile(apiMarker, []byte("stopped\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	dir := t.TempDir()
	dependentStart := filepath.Join(dir, "worker-started")
	crasherStart := filepath.Join(dir, "crasher-started")
	file := fileWith(map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "sh", Restart: config.RestartUnlessStopped,
			WorkingDir: dir,
		},
		"worker": {
			Command: "touch " + dependentStart + "; sleep 30", Shell: "sh",
			Restart: config.RestartNo, WorkingDir: dir,
			DependsOn: config.DependsOn{
				Entries: map[string]config.DependsOnEntry{
					"api": {Condition: config.ConditionServiceStarted},
				},
				Order: []string{"api"},
			},
		},
		"crasher": {
			Command: "touch " + crasherStart + "; sleep 30", Shell: "sh",
			Restart: config.RestartNo, WorkingDir: dir,
			DependsOn: config.DependsOn{
				Entries: map[string]config.DependsOnEntry{
					"api": {Condition: config.ConditionServiceStarted},
				},
				Order: []string{"api"},
			},
		},
	})
	s, err := supervisor.New(supervisor.Options{
		Locations:           locs,
		File:                file,
		Order:               []string{"api", "worker", "crasher"},
		BaseDir:             dir,
		Foreground:          false,
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
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

	// All three services should be Stopped: api was skipped by the marker,
	// worker/crasher are skipped transitively. None should be running.
	if !waitFor(t, 2*time.Second, func() bool {
		for _, st := range s.States() {
			if st.Status != supervisor.StatusStopped {
				return false
			}
			if st.PID != 0 {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("services not all stopped: %+v", s.States())
	}

	// An explicit stop is not a failure: the supervisor must not record one.
	if s.Failed() {
		t.Errorf("Failed() = true, want false (skipped stops are not failures)")
	}

	// Dependents must never have launched.
	if fileExists(dependentStart) {
		t.Errorf("worker started marker exists; worker launched despite stopped api")
	}
	if fileExists(crasherStart) {
		t.Errorf("crasher started marker exists; crasher launched despite stopped api")
	}

	// Logs must explain the skip rather than blaming api for "exiting".
	apiLog, _ := s.LogPath("api")
	if data := string(mustReadFile(t, apiLog)); !strings.Contains(data, "skipping stopped service api") {
		t.Errorf("api log missing skip notice: %q", data)
	}
	workerLog, _ := s.LogPath("worker")
	if data := string(mustReadFile(t, workerLog)); !strings.Contains(data, "skipped: dependency \"api\" is stopped") {
		t.Errorf("worker log missing transitive-skip notice: %q", data)
	}
}

// TestSupervisorRestart verifies Restart stops a running service and relaunches it.
func TestSupervisorRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"loop": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s := newSupervisor(t, file, []string{"loop"})
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
	pidBefore := s.States()[0].PID

	if err := s.Restart("loop"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()[0]
		return st.Status == supervisor.StatusRunning && st.PID != pidBefore && st.PID != 0
	}) {
		st := s.States()[0]
		t.Fatalf("restart did not produce a new running pid: before=%d after=%+v", pidBefore, st)
	}
}

func fileExists(path string) bool {
	_, err := exec.Command("test", "-e", path).Output()
	return err == nil
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	return waitForFile(t, path, 2*time.Second)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, err := readFile(path)
		if err == nil {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("reading %s: %v", path, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readFile(path string) ([]byte, error) {
	return exec.Command("cat", path).Output()
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}
