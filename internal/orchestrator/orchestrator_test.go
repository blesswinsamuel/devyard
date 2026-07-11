package orchestrator_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/orchestrator"
)

// setupEnv sets isolated XDG dirs for the test so the orchestrator's
// project.Resolve calls don't touch the user's real state. The runtime dir is
// under /tmp to stay within the Unix socket path limit on macOS.
func setupEnv(t *testing.T) {
	t.Helper()
	rt, err := os.MkdirTemp("/tmp", "lc-orch-rt")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Cleanup(func() { _ = os.RemoveAll(rt) })

	st := t.TempDir()
	t.Setenv("XDG_STATE_HOME", st)
}

// writeConfig writes a local-compose.yml with the given content to a temp dir
// and returns the config path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// shortSleepConfig is a minimal config whose service exits quickly so tests
// don't hang waiting for long-running processes.
const shortSleepConfig = `version: "1"
name: lc-test
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
`

func TestStartAndStopProject(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	projects := d.ListProjects()
	if len(projects) != 1 || projects[0].Name != "lc-test" {
		t.Fatalf("ListProjects = %+v, want [lc-test]", projects)
	}

	b, err := d.ProjectBackend("lc-test")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}
	if b == nil {
		t.Fatalf("ProjectBackend returned nil")
	}

	if err := d.StopProject("lc-test"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	projects = d.ListProjects()
	if len(projects) != 0 {
		t.Fatalf("ListProjects after stop = %+v, want empty", projects)
	}
}

func TestStartProjectAlreadyRunning(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	defer func() { _ = d.StopProject("lc-test") }()

	if err := d.StartProject(configPath, false); err == nil {
		t.Fatalf("StartProject twice: expected error, got nil")
	}
}

func TestStopProjectNotRunning(t *testing.T) {
	d := orchestrator.New()
	if err := d.StopProject("nope"); err == nil {
		t.Fatalf("StopProject for unknown project: expected error, got nil")
	}
}

func TestProjectBackendUnknown(t *testing.T) {
	d := orchestrator.New()
	if _, err := d.ProjectBackend("nope"); err == nil {
		t.Fatalf("ProjectBackend for unknown project: expected error, got nil")
	}
}

func TestStopDaemonStopsAllProjects(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	if err := d.StopDaemon(); err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}

	select {
	case <-d.StopCh():
	default:
		t.Fatalf("StopCh was not closed")
	}

	projects := d.ListProjects()
	if len(projects) != 0 {
		t.Fatalf("ListProjects after StopDaemon = %+v, want empty", projects)
	}
}

func TestProjectStatusTransitionsToStopped(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		projects := d.ListProjects()
		if len(projects) == 1 && projects[0].Status == "stopped" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("project status did not transition to stopped")
}

func TestStopProjectWritesStoppedMarker(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d.StopProject("lc-test"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	if !orchestrator.HasProjectStoppedMarker("lc-test") {
		t.Fatalf("expected .stopped marker after StopProject")
	}
}

func TestStartProjectRemovesStoppedMarker(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d.StopProject("lc-test"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	if !orchestrator.HasProjectStoppedMarker("lc-test") {
		t.Fatalf("expected .stopped marker after StopProject")
	}

	if err := d.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject second time: %v", err)
	}
	if orchestrator.HasProjectStoppedMarker("lc-test") {
		t.Fatalf("expected .stopped marker to be removed after StartProject")
	}
	_ = d.StopProject("lc-test")
}

func TestAutostartAlwaysPolicy(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-always
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
    restart: always
`)

	// Simulate a prior run that wrote the config-path.
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d1.StopProject("lc-always"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	// Remove the .stopped marker so autostart isn't suppressed.
	_ = orchestrator.RemoveProjectStoppedMarker("lc-always")

	// A fresh daemon should autostart this project.
	d2 := orchestrator.New()
	started, skipped, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 1 {
		t.Fatalf("started = %d, want 1", started)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	_ = d2.StopProject("lc-always")
}

func TestAutostartUnlessStoppedWithMarker(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-unless
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
    restart: unless-stopped
`)

	// Simulate a prior run that stopped the project (writes .stopped marker).
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d1.StopProject("lc-unless"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	// The .stopped marker should exist, so autostart should skip it.
	d2 := orchestrator.New()
	started, skipped, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 0 {
		t.Fatalf("started = %d, want 0 (stopped marker)", started)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
}

func TestAutostartUnlessStoppedWithoutMarker(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-unless2
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
    restart: unless-stopped
`)

	// Simulate a prior run that didn't stop (no .stopped marker).
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	// Don't call StopProject — just stop the daemon so the marker isn't written.
	_ = d1.StopDaemon()

	// A fresh daemon should autostart this project (no marker).
	d2 := orchestrator.New()
	started, _, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 1 {
		t.Fatalf("started = %d, want 1 (no stopped marker)", started)
	}
	_ = d2.StopProject("lc-unless2")
}

func TestAutostartNoRestartPolicySkipped(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-norestart
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
`)

	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	_ = d1.StopDaemon()

	d2 := orchestrator.New()
	started, skipped, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 0 {
		t.Fatalf("started = %d, want 0 (restart: no)", started)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
}

func TestAutostartNoProjects(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	started, skipped, err := d.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 0 || skipped != 0 {
		t.Fatalf("started=%d skipped=%d, want 0/0", started, skipped)
	}
}
