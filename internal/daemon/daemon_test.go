//go:build unix

package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
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
