//go:build unix

package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// readyReport is what the daemon child stub writes so the parent test can
// verify the re-exec actually happened with the right args + a new session.
type readyReport struct {
	PID  int      `json:"pid"`
	SID  int      `json:"sid"`
	PPID int      `json:"ppid"`
	Args []string `json:"args"`
}

// TestMain intercepts the re-exec'd daemon child: when the test binary is
// invoked with --supervisor <project>, it runs a small stub that reports back
// to the parent test over a file and stays alive until signalled. This lets us
// exercise the real Spawn code path (os.Executable + setsid + stdio wiring +
// pidfile) end-to-end without a working control socket.
func TestMain(m *testing.M) {
	if isSupervisorChild(os.Args) {
		runChildStub()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func isSupervisorChild(args []string) bool {
	for i, a := range args {
		if a == daemon.SupervisorFlag && i+1 < len(args) {
			return true
		}
	}
	return false
}

// runChildStub is the "daemonized supervisor" for tests: it writes a readiness
// report, announces itself on stdout (which Spawn wired to supervisor.log),
// then stays alive until the parent creates the done marker or a safety
// timeout elapses.
func runChildStub() {
	pid := os.Getpid()
	ppid := os.Getppid()
	sid, err := syscall.Getsid(pid)
	if err != nil {
		sid = -1
	}
	rep := readyReport{PID: pid, SID: sid, PPID: ppid, Args: append([]string{}, os.Args...)}
	_, _ = fmt.Fprintln(os.Stdout, "daemon-child-ready project="+projectFromArgs(os.Args))

	if path := os.Getenv("LOCAL_COMPOSE_DAEMON_TEST_READY"); path != "" {
		if b, err := json.Marshal(rep); err == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}

	donePath := os.Getenv("LOCAL_COMPOSE_DAEMON_TEST_DONE")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if donePath != "" {
			if _, err := os.Stat(donePath); err == nil {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func projectFromArgs(args []string) string {
	for i, a := range args {
		if a == daemon.SupervisorFlag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func testLocations(t *testing.T) *project.Locations {
	t.Helper()
	dir := t.TempDir()
	return &project.Locations{
		Name:    "test",
		Runtime: filepath.Join(dir, "runtime"),
		State:   filepath.Join(dir, "state"),
		Socket:  filepath.Join(dir, "runtime", "supervisor.sock"),
		Pidfile: filepath.Join(dir, "runtime", "supervisor.pid"),
		LogsDir: filepath.Join(dir, "state", "logs"),
	}
}

// TestSpawnReexecsAsSessionLeader verifies Spawn re-execs the current binary
// with --supervisor <project>, makes the child a new session leader, wires its
// stdio to the supervisor log, and writes the child's pid to the pidfile.
func TestSpawnReexecsAsSessionLeader(t *testing.T) {
	locs := testLocations(t)
	readyPath := filepath.Join(locs.State, "ready.json")
	donePath := filepath.Join(locs.State, "done")
	t.Setenv("LOCAL_COMPOSE_DAEMON_TEST_READY", readyPath)
	t.Setenv("LOCAL_COMPOSE_DAEMON_TEST_DONE", donePath)

	pid, err := daemon.Spawn(daemon.Options{
		Locations:  locs,
		Project:    "myproj",
		ConfigPath: "/tmp/local-compose.yml",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("Spawn returned pid %d", pid)
	}

	var rep readyReport
	if !waitFor(t, 5*time.Second, func() bool {
		b, err := os.ReadFile(readyPath)
		if err != nil || len(b) == 0 {
			return false
		}
		return json.Unmarshal(b, &rep) == nil
	}) {
		t.Fatalf("child never became ready; ready file: read error")
	}

	// Clean up the child once assertions are done.
	t.Cleanup(func() {
		_ = os.WriteFile(donePath, []byte("done"), 0o644)
		if rep.PID > 0 {
			_ = syscall.Kill(rep.PID, syscall.SIGTERM)
		}
	})

	// Pidfile was written with the child's pid and matches what Spawn returned.
	pfPid, err := daemon.ReadPidfile(locs.Pidfile)
	if err != nil {
		t.Fatalf("ReadPidfile: %v", err)
	}
	if pfPid != rep.PID {
		t.Errorf("pidfile pid = %d, child pid = %d", pfPid, rep.PID)
	}
	if pfPid != pid {
		t.Errorf("Spawn returned %d, pidfile %d", pid, pfPid)
	}

	// setsid: the child is its own session leader (sid == pid) and detached
	// from the parent's session.
	parentSid, _ := syscall.Getsid(os.Getpid())
	if rep.SID != rep.PID {
		t.Errorf("child sid = %d, want %d (session leader)", rep.SID, rep.PID)
	}
	if rep.SID == parentSid {
		t.Errorf("child sid = %d == parent sid %d (not detached)", rep.SID, parentSid)
	}

	// Args forwarded verbatim.
	if !hasPair(rep.Args, daemon.SupervisorFlag, "myproj") {
		t.Errorf("args missing %q myproj: %v", daemon.SupervisorFlag, rep.Args)
	}
	if !hasPair(rep.Args, "-f", "/tmp/local-compose.yml") {
		t.Errorf("args missing -f /tmp/local-compose.yml: %v", rep.Args)
	}

	// Stdio wired to supervisor.log: the child's stdout announcement lands there.
	logPath := filepath.Join(locs.State, "supervisor.log")
	if !waitForFileContains(t, logPath, "daemon-child-ready", 3*time.Second) {
		t.Errorf("supervisor.log %s missing child output", logPath)
	}

	// Running sees the live supervisor.
	if live, err := daemon.Running(locs); err != nil {
		t.Fatalf("Running: %v", err)
	} else if live != rep.PID {
		t.Errorf("Running = %d, want %d", live, rep.PID)
	}
	if !daemon.IsAlive(rep.PID) {
		t.Errorf("IsAlive(%d) = false, want true", rep.PID)
	}
}

// TestSpawnValidation covers the cheap error paths that don't fork.
func TestSpawnValidation(t *testing.T) {
	locs := testLocations(t)
	if _, err := daemon.Spawn(daemon.Options{Project: "x"}); err == nil {
		t.Error("expected error for nil Locations")
	}
	if _, err := daemon.Spawn(daemon.Options{Locations: locs, Project: "  "}); err == nil {
		t.Error("expected error for empty Project")
	}
}

// TestRunningNoPidfile returns 0 when no pidfile exists.
func TestRunningNoPidfile(t *testing.T) {
	locs := testLocations(t)
	pid, err := daemon.Running(locs)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if pid != 0 {
		t.Errorf("Running = %d, want 0 with no pidfile", pid)
	}
}

// TestReadPidfileMalformed exercises the parser edge cases.
func TestReadPidfileMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.pid")

	if _, err := daemon.ReadPidfile(path); err == nil {
		t.Error("expected error for missing pidfile")
	}

	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.ReadPidfile(path); err == nil {
		t.Error("expected error for non-numeric pidfile")
	}

	if err := os.WriteFile(path, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.ReadPidfile(path); err == nil {
		t.Error("expected error for empty pidfile")
	}

	// A well-formed pidfile parses and trims whitespace.
	if err := os.WriteFile(path, []byte("  4242  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := daemon.ReadPidfile(path)
	if err != nil {
		t.Fatalf("ReadPidfile: %v", err)
	}
	if got != 4242 {
		t.Errorf("ReadPidfile = %d, want 4242", got)
	}
}

// TestIsAliveStaleProcess: a pid we own and have killed is reported dead.
func TestIsAliveStaleProcess(t *testing.T) {
	// PID 1 is always alive on unix; a very high pid is almost certainly dead.
	if !daemon.IsAlive(1) {
		t.Errorf("IsAlive(1) = false, want true")
	}
	if daemon.IsAlive(2_000_000) {
		t.Errorf("IsAlive(2000000) = true, want false (stale)")
	}
	if daemon.IsAlive(0) || daemon.IsAlive(-1) {
		t.Errorf("IsAlive(0/-1) should be false")
	}
}

// TestRemovePidfileIdempotent removes an absent file without error.
func TestRemovePidfileIdempotent(t *testing.T) {
	locs := testLocations(t)
	if err := daemon.RemovePidfile(locs); err != nil {
		t.Errorf("RemovePidfile on absent file: %v", err)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
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
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForFileContains(t *testing.T, path, needle string, timeout time.Duration) bool {
	t.Helper()
	return waitFor(t, timeout, func() bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		return strings.Contains(string(b), needle)
	})
}
