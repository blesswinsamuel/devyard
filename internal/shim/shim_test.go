//go:build unix

package shim_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/shim"
)

func TestShimRunBasic(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "svc.log")
	statePath := filepath.Join(dir, "svc.state.json")

	cfg := &shim.Config{
		Project:   "test-proj",
		Service:   "test-svc",
		Command:   "echo 'hello world'",
		LogPath:   logPath,
		StatePath: statePath,
	}

	if err := shim.Run(cfg); err != nil {
		t.Fatalf("shim.Run: %v", err)
	}

	// Verify log file
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines, got: %q", string(logData))
	}
	if !strings.Contains(lines[0], "$ echo 'hello world'") {
		t.Errorf("first line does not contain command: %q", lines[0])
	}
	if !strings.Contains(lines[1], "hello world") {
		t.Errorf("second line does not contain output: %q", lines[1])
	}

	// Verify timestamp format on output line
	parts := strings.SplitN(lines[1], " ", 2)
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		t.Errorf("invalid RFC3339Nano timestamp %q: %v", parts[0], err)
	}

	// Verify state file
	state, err := shim.LoadState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Status != "exited" {
		t.Errorf("state.Status = %q, want 'exited'", state.Status)
	}
	if state.ExitCode != 0 {
		t.Errorf("state.ExitCode = %d, want 0", state.ExitCode)
	}
}

func TestShimRunNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "svc.log")
	statePath := filepath.Join(dir, "svc.state.json")

	cfg := &shim.Config{
		Project:   "test-proj",
		Service:   "test-svc",
		Command:   "exit 42",
		LogPath:   logPath,
		StatePath: statePath,
	}

	if err := shim.Run(cfg); err != nil {
		t.Fatalf("shim.Run: %v", err)
	}

	state, err := shim.LoadState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Status != "exited" {
		t.Errorf("state.Status = %q, want 'exited'", state.Status)
	}
	if state.ExitCode != 42 {
		t.Errorf("state.ExitCode = %d, want 42", state.ExitCode)
	}
}
