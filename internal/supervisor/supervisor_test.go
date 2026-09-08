package supervisor_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
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
	if !strings.Contains(data, "CLICOLOR=1") || !strings.Contains(data, "CLICOLOR_FORCE=1") {
		t.Errorf("log = %q, want CLICOLOR=1 and CLICOLOR_FORCE=1", data)
	}
	// TERM comes from the parent environment when set (defaults only fill
	// missing keys); just require that some TERM value reached the child.
	if !strings.Contains(data, "TERM=") {
		t.Errorf("log = %q, want TERM to be present", data)
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
	if !strings.Contains(string(data), "$ echo hello-echoer") {
		t.Errorf("log = %q, want it to contain $ echo hello-echoer", string(data))
	}
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

// TestSupervisorLogRotationOnRestart verifies that each successful spawn
// rotates the service's log: the current file holds only the latest run while
// the immediately preceding run is preserved in <name>.prev.log, and older
// runs are dropped.
func TestSupervisorLogRotationOnRestart(t *testing.T) {
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

	cur, err := s.LogPath("flaky")
	if err != nil {
		t.Fatal(err)
	}
	prev, err := s.PreviousLogPath("flaky")
	if err != nil {
		t.Fatal(err)
	}

	curData := string(mustReadFile(t, cur))
	if !strings.Contains(curData, "attempt 3") {
		t.Errorf("current log missing attempt 3: %q", curData)
	}
	for _, stale := range []string{"attempt 1", "attempt 2"} {
		if strings.Contains(curData, stale) {
			t.Errorf("current log contains %q from an older run: %q", stale, curData)
		}
	}

	prevData := string(mustReadFile(t, prev))
	if !strings.Contains(prevData, "attempt 2") {
		t.Errorf("previous log missing attempt 2: %q", prevData)
	}
	for _, gone := range []string{"attempt 1", "attempt 3"} {
		if strings.Contains(prevData, gone) {
			t.Errorf("previous log contains %q from a non-previous run: %q", gone, prevData)
		}
	}
}

// TestSupervisorLogRotationPreservesPreviousOnSpawnFailure verifies that a
// spawn failure does not rotate away the previous run's log: the current file
// keeps the old run alongside the failure notice until a spawn actually
// succeeds, at which point the whole file moves to <name>.prev.log intact.
func TestSupervisorLogRotationPreservesPreviousOnSpawnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	locs := testLocations(t)
	dir := t.TempDir()

	first := fileWith(map[string]config.Service{
		"svc": {Command: "echo first-run", Shell: "sh", Restart: config.RestartNo, WorkingDir: dir},
	})
	s1, err := supervisor.New(supervisor.Options{
		Locations:           locs,
		File:                first,
		Order:               []string{"svc"},
		BaseDir:             dir,
		Foreground:          false,
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s1.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s1.Wait()
	_ = s1.Close()

	// A second session whose shell can never exec: the spawn itself fails, so
	// it must not clobber the previous run's log (rotation only happens once a
	// spawn succeeds, which never occurs here).
	second := fileWith(map[string]config.Service{
		"svc": {Command: "echo nope", Shell: "/definitely/not/a/real/shell", Restart: config.RestartOnFailure, WorkingDir: dir},
	})
	s2, err := supervisor.New(supervisor.Options{
		Locations:           locs,
		File:                second,
		Order:               []string{"svc"},
		BaseDir:             dir,
		Foreground:          false,
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s2.Wait()

	cur, _ := s2.LogPath("svc")
	prev, _ := s2.PreviousLogPath("svc")

	// The first run's output must still be intact in the current file (no
	// successful spawn has rotated it away yet).
	curData := string(mustReadFile(t, cur))
	if !strings.Contains(curData, "first-run") {
		t.Errorf("current log lost first-run after failed spawn: %q", curData)
	}
	// The failed spawn notice must be recorded alongside it.
	if !strings.Contains(curData, "failed to start") {
		t.Errorf("current log missing failed-to-start notice: %q", curData)
	}
	// No previous-run file is created without a successful spawn.
	if fileExists(prev) {
		t.Errorf("previous log %s exists but no run ever completed", prev)
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

	if err := s.StopService("sleeper"); err != nil {
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

// TestSupervisorSelectedStart verifies a supervisor materialized with a
// Selected subset launches only those services, leaves the rest in
// StatusStopped without recording a failure, and that StartService resumes a
// skipped service in place.
func TestSupervisorSelectedStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	locs := testLocations(t)
	file := fileWith(map[string]config.Service{
		"long":  {Command: "sleep 30", Shell: "sh", Restart: config.RestartAlways},
		"other": {Command: "sleep 30", Shell: "sh", Restart: config.RestartNo},
	})
	s, err := supervisor.New(supervisor.Options{
		Locations:           locs,
		File:                file,
		Order:               []string{"long", "other"},
		BaseDir:             t.TempDir(),
		Foreground:          false,
		Backoff:             testBackoff(),
		GracefulStopTimeout: 2 * time.Second,
		Selected:            []string{"long"},
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
		st := s.States()
		return len(st) == 2 && st[0].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("selected service never running: %+v", s.States())
	}
	// Unselected service stays stopped without launching or failing.
	st := s.States()[1]
	if st.Status != supervisor.StatusStopped {
		t.Errorf("other status = %s, want stopped", st.Status)
	}
	if st.PID != 0 {
		t.Errorf("other pid = %d, want 0 (not running)", st.PID)
	}
	if s.Failed() {
		t.Errorf("Failed() = true, want false (unselected stops are not failures)")
	}

	// Resuming the skipped service starts it in place.
	if err := s.StartService("other"); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return s.States()[1].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("other never running after StartService: %+v", s.States())
	}

	// StartService on an already-running service is a no-op.
	if err := s.StartService("long"); err != nil {
		t.Fatalf("StartService on running service: %v", err)
	}
}

// TestSupervisorSelectedSkipsDependents verifies that when a supervisor is
// materialized with a Selected set, services outside the set are left stopped
// (not reported as failures) and their logs explain the skip. Selected services
// and their dependencies run normally.
func TestSupervisorSelectedSkipsDependents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	locs := testLocations(t)
	if err := locs.MkdirAll(); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dir := t.TempDir()
	workerStart := filepath.Join(dir, "worker-started")
	crasherStart := filepath.Join(dir, "crasher-started")
	file := fileWith(map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "sh", Restart: config.RestartNo,
			WorkingDir: dir,
		},
		"worker": {
			Command: "touch " + workerStart + "; sleep 30", Shell: "sh",
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
		Selected:            []string{"api", "worker"},
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

	// api and worker (selected) run; crasher stays stopped.
	if !waitFor(t, 2*time.Second, func() bool {
		st := s.States()
		return st[0].Status == supervisor.StatusRunning && st[1].Status == supervisor.StatusRunning
	}) {
		t.Fatalf("selected services not running: %+v", s.States())
	}
	crasher := s.States()[2]
	if crasher.Status != supervisor.StatusStopped {
		t.Errorf("crasher status = %s, want stopped", crasher.Status)
	}
	if crasher.PID != 0 {
		t.Errorf("crasher pid = %d, want 0", crasher.PID)
	}

	// An unselected service is not a failure: the supervisor must not record one.
	if s.Failed() {
		t.Errorf("Failed() = true, want false (skipped stops are not failures)")
	}

	// Selected services launched; unselected one must never have launched.
	waitForFile(t, workerStart, 2*time.Second)
	if fileExists(crasherStart) {
		t.Errorf("crasher started marker exists; crasher launched despite being unselected")
	}

	// The skipped service's log must explain the skip.
	crasherLog, _ := s.LogPath("crasher")
	if data := string(mustReadFile(t, crasherLog)); !strings.Contains(data, "skipping service crasher") {
		t.Errorf("crasher log missing skip notice: %q", data)
	}
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

func TestSupervisorRunAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Actions = map[string]config.Action{
		"echo-test": {
			Spec: config.ActionSpec{
				Command: "echo hello action",
				Shell:   "sh",
			},
		},
	}
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() { _ = s.Close() })

	var buf bytes.Buffer
	code, err := s.RunAction(context.Background(), "echo-test", []string{"extra"}, &buf)
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "hello action extra") {
		t.Errorf("buf = %q, want 'hello action extra'", buf.String())
	}
}

func TestSupervisorStopAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Actions = map[string]config.Action{
		"sleeper": {
			Spec: config.ActionSpec{
				Command: "echo started; sleep 30",
				Shell:   "sh",
			},
		},
		"echo-test": {
			Spec: config.ActionSpec{
				Command: "echo hello action",
				Shell:   "sh",
			},
		},
	}
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() { _ = s.Close() })

	type runResult struct {
		code int
		err  error
	}
	runDone := make(chan runResult, 1)
	var buf bytes.Buffer
	go func() {
		code, err := s.RunAction(context.Background(), "sleeper", nil, &buf)
		runDone <- runResult{code, err}
	}()

	if !waitForActionStatus(t, s, "sleeper", "running", 5*time.Second) {
		t.Fatalf("action never reached running state")
	}

	// A second concurrent run must be rejected while the first is running.
	if _, err := s.RunAction(context.Background(), "sleeper", nil, nil); err == nil {
		t.Errorf("RunAction while running: expected error, got nil")
	}

	if err := s.StopAction("sleeper"); err != nil {
		t.Fatalf("StopAction: %v", err)
	}

	select {
	case res := <-runDone:
		if res.err != nil {
			t.Fatalf("RunAction: %v", res.err)
		}
		if res.code == 0 {
			t.Errorf("exit code = %d, want non-zero after stop", res.code)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("RunAction did not return after StopAction")
	}

	if !waitForActionStatus(t, s, "sleeper", "exited", 5*time.Second) {
		t.Fatalf("action never reached exited state")
	}

	// Stopping again after the run finished is an error.
	if err := s.StopAction("sleeper"); err == nil {
		t.Errorf("StopAction on finished action: expected error, got nil")
	}

	// The runtime accepts a fresh run after a stop.
	var buf2 bytes.Buffer
	code, err := s.RunAction(context.Background(), "echo-test", nil, &buf2)
	if err != nil {
		t.Fatalf("RunAction after stop: %v", err)
	}
	if code != 0 || !strings.Contains(buf2.String(), "hello action") {
		t.Errorf("RunAction after stop: code=%d buf=%q", code, buf2.String())
	}
}

func TestSupervisorStopActionNotRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Actions = map[string]config.Action{
		"echo-test": {
			Spec: config.ActionSpec{Command: "echo hello action", Shell: "sh"},
		},
	}
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() { _ = s.Close() })

	if err := s.StopAction("nope"); err == nil {
		t.Errorf("StopAction for unknown action: expected error, got nil")
	}
	if err := s.StopAction("echo-test"); err == nil {
		t.Errorf("StopAction for never-run action: expected error, got nil")
	}
}

func TestRunActionContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo service-running", Shell: "sh"},
	})
	file.Actions = map[string]config.Action{
		"sleeper": {
			Spec: config.ActionSpec{
				Command: "echo started; sleep 30",
				Shell:   "sh",
			},
		},
	}
	s := newSupervisor(t, file, []string{"svc"})
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type runResult struct {
		code int
		err  error
	}
	runDone := make(chan runResult, 1)
	var buf bytes.Buffer
	go func() {
		code, err := s.RunAction(ctx, "sleeper", nil, &buf)
		runDone <- runResult{code, err}
	}()

	if !waitForActionStatus(t, s, "sleeper", "running", 5*time.Second) {
		t.Fatalf("action never reached running state")
	}
	cancel()

	select {
	case res := <-runDone:
		if res.err != nil {
			t.Fatalf("RunAction: %v", res.err)
		}
		if res.code == 0 {
			t.Errorf("exit code = %d, want non-zero after context cancel", res.code)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("RunAction did not return after context cancellation")
	}
}

func waitForActionStatus(t *testing.T, s *supervisor.Supervisor, name, status string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, act := range s.ListActions() {
			if act.Name == name && act.Status == status {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
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

func TestReconcileOrphans(t *testing.T) {
	t.Parallel()
	f1 := fileWith(map[string]config.Service{
		"alpha": {Command: "sleep 60"},
		"beta":  {Command: "sleep 60"},
	})
	s := newSupervisor(t, f1, []string{"alpha", "beta"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	if !waitFor(t, 2*time.Second, func() bool { return len(s.States()) == 2 }) {
		t.Fatalf("expected 2 services running")
	}

	// Reconcile removing "beta" with removeOrphans = true
	f2 := fileWith(map[string]config.Service{
		"alpha": {Command: "sleep 60"},
	})
	if err := s.Reconcile(f2, []string{"alpha"}, true); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	states := s.States()
	if len(states) != 1 || states[0].Name != "alpha" {
		t.Fatalf("states after reconcile = %+v, want only alpha", states)
	}
}

func TestSupervisorActionLogs(t *testing.T) {
	t.Parallel()
	f := fileWith(map[string]config.Service{
		"app": {Command: "echo app"},
	})
	f.Actions = map[string]config.Action{
		"migrate": {Spec: config.ActionSpec{Command: "echo action_first_run"}},
	}
	s := newSupervisor(t, f, []string{"app"})

	var buf bytes.Buffer
	code, err := s.RunAction(context.Background(), "migrate", nil, &buf)
	if err != nil || code != 0 {
		t.Fatalf("RunAction: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "$ echo action_first_run") {
		t.Fatalf("RunAction buf = %q, want $ echo action_first_run", buf.String())
	}

	path, err := s.ActionLogPath("migrate")
	if err != nil {
		t.Fatalf("ActionLogPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "$ echo action_first_run") || !strings.Contains(string(data), "action_first_run") {
		t.Fatalf("ActionLogPath content = %q, want $ echo action_first_run", string(data))
	}

	// Run action a second time to test log rotation
	f.Actions["migrate"] = config.Action{Spec: config.ActionSpec{Command: "echo action_second_run"}}
	s.UpdateFile(f)
	buf.Reset()
	code, err = s.RunAction(context.Background(), "migrate", nil, &buf)
	if err != nil || code != 0 {
		t.Fatalf("RunAction 2: code=%d err=%v", code, err)
	}

	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "action_second_run") {
		t.Fatalf("ActionLogPath content after rerun = %q, want action_second_run", string(data))
	}

	prevPath, err := s.ActionPreviousLogPath("migrate")
	if err != nil {
		t.Fatalf("ActionPreviousLogPath: %v", err)
	}
	prevData, err := os.ReadFile(prevPath)
	if err != nil || !strings.Contains(string(prevData), "action_first_run") {
		t.Fatalf("ActionPreviousLogPath content = %q, want action_first_run", string(prevData))
	}
}

func TestOnStateChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	file := fileWith(map[string]config.Service{
		"svc": {Command: "echo hello", Shell: "sh", Restart: config.RestartNo},
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
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) == 0 {
		t.Fatal("OnStateChange never called")
	}
	// Verify we saw at least one running and one stopped/exited transition.
	var sawRunning, sawStopped bool
	for _, tr := range transitions {
		switch tr.Status {
		case string(supervisor.StatusRunning):
			sawRunning = true
		case string(supervisor.StatusStopped), string(supervisor.StatusExited):
			sawStopped = true
		}
	}
	if !sawRunning {
		t.Errorf("never saw StatusRunning in transitions: %v", transitions)
	}
	if !sawStopped {
		t.Errorf("never saw StatusStopped/Exited in transitions: %v", transitions)
	}
}
