//go:build unix

package daemon_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// TestReadPidfileMalformed exercises the parser edge cases.
func TestReadPidfileMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

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

// TestRemovePidfileIfOurs verifies the restart-safe pidfile removal: the old
// daemon must delete the pidfile only while it still names its own pid, never
// the replacement daemon's pid.
func TestRemovePidfileIfOurs(t *testing.T) {
	dir := t.TempDir()
	locs := &project.DaemonLocations{Pidfile: filepath.Join(dir, "daemon.pid")}

	// A pidfile naming a different process (the replacement daemon) survives.
	if err := os.WriteFile(locs.Pidfile, []byte("9999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := daemon.RemovePidfileIfOurs(locs, 1234); err != nil {
		t.Fatalf("RemovePidfileIfOurs (other pid): %v", err)
	}
	if _, err := os.Stat(locs.Pidfile); err != nil {
		t.Errorf("pidfile was removed even though it names a different daemon")
	}

	// A pidfile naming this process is removed.
	if err := daemon.WritePidfile(locs.Pidfile, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := daemon.RemovePidfileIfOurs(locs, os.Getpid()); err != nil {
		t.Fatalf("RemovePidfileIfOurs (own pid): %v", err)
	}
	if _, err := os.Stat(locs.Pidfile); !os.IsNotExist(err) {
		t.Errorf("pidfile still exists after RemovePidfileIfOurs")
	}

	// Degenerate inputs are no-ops, not errors.
	if err := daemon.RemovePidfileIfOurs(locs, 0); err != nil {
		t.Errorf("RemovePidfileIfOurs(pid=0) = %v, want nil", err)
	}
}

func TestIsSocketResponsive(t *testing.T) {
	if daemon.IsSocketResponsive("", 100*time.Millisecond) {
		t.Error("expected false for empty socket path")
	}

	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	if daemon.IsSocketResponsive(sock, 100*time.Millisecond) {
		t.Error("expected false for missing socket")
	}

	// Active listener accepts connections.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if !daemon.IsSocketResponsive(sock, 200*time.Millisecond) {
		t.Error("expected true for listening socket")
	}

	_ = ln.Close()
	if daemon.IsSocketResponsive(sock, 100*time.Millisecond) {
		t.Error("expected false for closed socket file")
	}
}

func TestDaemonRunning(t *testing.T) {
	if pid, err := daemon.DaemonRunning(nil); pid != 0 || err != nil {
		t.Errorf("DaemonRunning(nil) = (%d, %v), want (0, nil)", pid, err)
	}

	dir := t.TempDir()
	locs := &project.DaemonLocations{
		Pidfile: filepath.Join(dir, "daemon.pid"),
		Socket:  filepath.Join(dir, "daemon.sock"),
	}

	// 1. Missing pidfile
	if pid, err := daemon.DaemonRunning(locs); pid != 0 || err != nil {
		t.Errorf("DaemonRunning(missing) = (%d, %v), want (0, nil)", pid, err)
	}

	// 2. Dead PID
	if err := daemon.WritePidfile(locs.Pidfile, 2_000_000); err != nil {
		t.Fatal(err)
	}
	if pid, err := daemon.DaemonRunning(locs); pid != 0 || err != nil {
		t.Errorf("DaemonRunning(dead pid) = (%d, %v), want (0, nil)", pid, err)
	}

	// 3. Alive PID (our own PID), but socket not listening
	if err := daemon.WritePidfile(locs.Pidfile, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if pid, err := daemon.DaemonRunning(locs); pid != 0 || err != nil {
		t.Errorf("DaemonRunning(alive pid, no socket) = (%d, %v), want (0, nil)", pid, err)
	}

	// 4. Alive PID + listening socket
	ln, err := net.Listen("unix", locs.Socket)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	pid, err := daemon.DaemonRunning(locs)
	if err != nil {
		t.Fatalf("DaemonRunning(alive + listening): %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("DaemonRunning = %d, want %d", pid, os.Getpid())
	}
}
