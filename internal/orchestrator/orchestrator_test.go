package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	if err := d.StartProject(configPath, false, "", true); err != nil {
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
	if len(projects) != 1 || projects[0].Status != "stopped" {
		t.Fatalf("ListProjects after stop = %+v, want [lc-test] with stopped status", projects)
	}

	if err := d.RemoveProject("lc-test"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	projects = d.ListProjects()
	if len(projects) != 0 {
		t.Fatalf("ListProjects after remove = %+v, want empty", projects)
	}
}

func TestStartProjectAlreadyRunning(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-test
services:
  svc:
    command: sleep 30
`)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	defer func() { _ = d.StopProject("lc-test") }()

	// Compose-like: up on an already-running project is a no-op when all
	// services are running.
	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject twice: %v", err)
	}
}

func TestStartProjectAfterStopResumes(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-resume
services:
  svc:
    command: sleep 30
    restart: always
`)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	backend, err := d.ProjectBackend("lc-resume")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		states := backend.States()
		if len(states) == 1 && states[0].Status == "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := d.StopProject("lc-resume"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	// Explicit up must clear the project stopped marker and start again.
	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject after stop: %v", err)
	}
	defer func() { _ = d.StopProject("lc-resume") }()

	backend, err = d.ProjectBackend("lc-resume")
	if err != nil {
		t.Fatalf("ProjectBackend after restart: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		states := backend.States()
		if len(states) == 1 && states[0].Status == "running" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service did not resume after stop+up: %+v", backend.States())
}

func TestStartProjectResumesStoppedServiceWhileRunning(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-partial
services:
  a:
    command: sleep 30
  b:
    command: sleep 30
`)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	defer func() { _ = d.StopProject("lc-partial") }()

	backend, err := d.ProjectBackend("lc-partial")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		states := backend.States()
		if len(states) == 2 && states[0].Status == "running" && states[1].Status == "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := backend.StopService("a"); err != nil {
		t.Fatalf("StopService: %v", err)
	}

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject while running: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		states := backend.States()
		running := 0
		for _, st := range states {
			if st.Status == "running" {
				running++
			}
		}
		if running == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stopped service did not resume on up: %+v", backend.States())
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

	if err := d.StartProject(configPath, false, "", true); err != nil {
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
	if len(projects) != 1 || projects[0].Status != "stopped" {
		t.Fatalf("ListProjects after StopDaemon = %+v, want [lc-test] with stopped status", projects)
	}
}

func TestProjectStatusTransitionsToStopped(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false, "", true); err != nil {
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

	if err := d.StartProject(configPath, false, "", true); err != nil {
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

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d.StopProject("lc-test"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	if !orchestrator.HasProjectStoppedMarker("lc-test") {
		t.Fatalf("expected .stopped marker after StopProject")
	}

	if err := d.StartProject(configPath, false, "", true); err != nil {
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

	// Simulate a prior run that wrote the config-path, then daemon exit
	// without down (no .stopped marker).
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	_ = d1.StopDaemon()

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

func TestAutostartAlwaysHonorsProjectStopped(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-always-stopped
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
    restart: always
`)

	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d1.StopProject("lc-always-stopped"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	if !orchestrator.HasProjectStoppedMarker("lc-always-stopped") {
		t.Fatalf("expected .stopped marker after StopProject")
	}

	// Project .stopped must suppress autostart even for restart: always.
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
	projects := d2.ListProjects()
	if len(projects) != 1 || projects[0].Name != "lc-always-stopped" || projects[0].Status != "stopped" {
		t.Fatalf("ListProjects = %+v, want stopped lc-always-stopped still listed", projects)
	}
}

func TestProjectBackendAfterStop(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-backend
services:
  svc:
    command: sleep 30
`)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d.StopProject("lc-backend"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	// Closed supervisor is retained so ps can still list service states.
	backend, err := d.ProjectBackend("lc-backend")
	if err != nil {
		t.Fatalf("ProjectBackend after stop: %v", err)
	}
	states := backend.States()
	if len(states) != 1 {
		t.Fatalf("States after stop = %+v, want 1 service", states)
	}
	if err := backend.Restart("svc"); err == nil {
		t.Fatalf("Restart after stop: want error, got nil")
	}
	projects := d.ListProjects()
	if len(projects) != 1 || projects[0].Status != "stopped" {
		t.Fatalf("ListProjects after stop = %+v, want stopped project still listed", projects)
	}
}

func TestAutostartUnlessStoppedWithMarker(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-unless
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
`)

	// Simulate a prior run that stopped the project (writes .stopped marker).
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d1.StopProject("lc-unless"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}

	// The .stopped marker should suppress autostart even with no restart policy.
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

func TestAutostartWithoutMarkerStartsAnyPolicy(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-unless2
services:
  svc:
    command: sh -c 'echo hello; sleep 0.2'
`)

	// Simulate a prior run that didn't stop (no .stopped marker). Even with no
	// restart policy the project autostarts on the next daemon boot.
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false, "", true); err != nil {
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

func TestStartServiceOnStoppedProject(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-svcstart
services:
  api:
    command: sleep 30
  worker:
    command: sleep 30
    depends_on:
      api:
        condition: service_started
  other:
    command: sleep 30
`)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		projects := d.ListProjects()
		if len(projects) == 1 && projects[0].Status == "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := d.StopProject("lc-svcstart"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	if !orchestrator.HasProjectStoppedMarker("lc-svcstart") {
		t.Fatalf("expected .stopped marker after StopProject")
	}

	// Lazy start of one service materializes a supervisor that runs just the
	// requested service plus its depends_on chain.
	if err := d.StartService("lc-svcstart", "worker"); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	if orchestrator.HasProjectStoppedMarker("lc-svcstart") {
		t.Fatalf("expected .stopped marker to be removed after StartService")
	}
	backend, err := d.ProjectBackend("lc-svcstart")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		states := backend.States()
		running := map[string]bool{}
		for _, st := range states {
			running[st.Name] = st.Status == "running"
		}
		if running["api"] && running["worker"] && !running["other"] {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	states := backend.States()
	got := map[string]string{}
	for _, st := range states {
		got[st.Name] = st.Status
	}
	if got["api"] != "running" || got["worker"] != "running" {
		t.Fatalf("selected services not running: %+v", states)
	}
	if got["other"] != "stopped" {
		t.Fatalf("unselected service status = %q, want stopped: %+v", got["other"], states)
	}
	_ = d.StopProject("lc-svcstart")
}

func TestStartServiceErrors(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-svcerr
services:
  api:
    command: sleep 30
`)

	if err := d.StartService("lc-svcerr", "api"); err == nil {
		t.Fatalf("StartService for unknown project: want error, got nil")
	}
	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d.StartService("lc-svcerr", "nope"); err == nil {
		t.Fatalf("StartService for unknown service: want error, got nil")
	}
	_ = d.StopProject("lc-svcerr")
}

func TestStartServiceUnknownProject(t *testing.T) {
	d := orchestrator.New()
	if err := d.StartService("nope", "svc"); err == nil {
		t.Fatalf("StartService for unknown project: want error, got nil")
	}
}

func TestStoppedProjectBackendListsServicesAndActions(t *testing.T) {
	setupEnv(t)
	configPath := writeConfig(t, `version: "1"
name: lc-stopped
services:
  web:
    command: sleep 60
    healthcheck:
      test: ["CMD-SHELL", "true"]
  worker:
    command: sleep 60
actions:
  build:
    command: echo build
`)

	// Register the project, stop it explicitly (writes the project .stopped
	// marker), then let the daemon exit, so a fresh daemon's autostart
	// registers it as stopped.
	d1 := orchestrator.New()
	if err := d1.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if err := d1.StopProject("lc-stopped"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	_ = d1.StopDaemon()

	d2 := orchestrator.New()
	started, skipped, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 0 || skipped != 1 {
		t.Fatalf("started=%d skipped=%d, want 0/1", started, skipped)
	}

	backend, err := d2.ProjectBackend("lc-stopped")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}

	// Reads report every configured service as stopped, in start order.
	states := backend.States()
	if len(states) != 2 {
		t.Fatalf("States = %+v, want 2 stopped services", states)
	}
	for _, st := range states {
		if st.Status != "stopped" {
			t.Fatalf("States = %+v, want every service stopped", states)
		}
	}
	if states[0].Name != "web" || states[1].Name != "worker" {
		t.Fatalf("States order = %q,%q, want web,worker", states[0].Name, states[1].Name)
	}
	if !states[0].HasHealth || states[1].HasHealth {
		t.Fatalf("HasHealth = %v/%v, want true/false", states[0].HasHealth, states[1].HasHealth)
	}

	actions := backend.ListActions()
	if len(actions) != 1 || actions[0].Name != "build" {
		t.Fatalf("ListActions = %+v, want [build]", actions)
	}

	// Mutations still fail with the stopped-project error.
	if err := backend.Restart("web"); err == nil {
		t.Fatalf("Restart on stopped project: want error, got nil")
	}
	if err := backend.Stop(context.Background()); err == nil {
		t.Fatalf("Stop on stopped project: want error, got nil")
	}

	// Log paths resolve to the project's state dir so historical logs are readable.
	path, err := backend.LogPath("web")
	if err != nil {
		t.Fatalf("LogPath: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("lc-stopped", "logs", "web.log")) {
		t.Fatalf("LogPath = %q, want web.log under the project logs dir", path)
	}
	if _, err := backend.LogPath("nope"); err == nil {
		t.Fatalf("LogPath for unknown service: want error, got nil")
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

func TestRemoveProject(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)

	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	// Remove project while running (should stop and remove)
	if err := d.RemoveProject("lc-test"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	projects := d.ListProjects()
	if len(projects) != 0 {
		t.Fatalf("ListProjects = %+v, want empty after RemoveProject", projects)
	}
}

func TestListProjectsServiceCounts(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	cfg := `version: "1"
name: lc-counts
services:
  web:
    command: "sleep 60"
  worker:
    command: "sleep 60"
`
	configPath := writeConfig(t, cfg)
	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	projects := d.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("ListProjects = %+v, want 1 project", projects)
	}
	if projects[0].TotalServices != 2 || projects[0].RunningServices != 2 {
		t.Fatalf("Service counts = %d/%d, want 2/2", projects[0].RunningServices, projects[0].TotalServices)
	}

	_ = d.StopProject("lc-counts")
	projects = d.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("ListProjects after stop = %+v, want 1 project", projects)
	}
	if projects[0].RunningServices != 0 || projects[0].TotalServices != 2 {
		t.Fatalf("Stopped service counts = %d/%d, want 0/2", projects[0].RunningServices, projects[0].TotalServices)
	}
}

func TestListProjectsStaleCleanup(t *testing.T) {
	setupEnv(t)
	d := orchestrator.New()
	configPath := writeConfig(t, shortSleepConfig)
	if err := d.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	_ = d.StopProject("lc-test")

	// Delete config file on disk
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("remove config: %v", err)
	}

	projects := d.ListProjects()
	if len(projects) != 0 {
		t.Fatalf("ListProjects = %+v, want 0 (auto-cleaned stale project)", projects)
	}
}

func TestStopDaemonKeepServicesAndAdopt(t *testing.T) {
	setupEnv(t)
	d1 := orchestrator.New()
	configPath := writeConfig(t, `version: "1"
name: lc-adopt-test
services:
  svc:
    command: sleep 30
`)

	if err := d1.StartProject(configPath, false, "", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}

	b1, err := d1.ProjectBackend("lc-adopt-test")
	if err != nil {
		t.Fatalf("ProjectBackend: %v", err)
	}

	// Wait until service is running
	var origPid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		states := b1.States()
		if len(states) > 0 && states[0].Pid > 0 {
			origPid = int(states[0].Pid)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if origPid == 0 {
		t.Fatal("service did not start in time")
	}

	// Stop daemon keeping services alive
	if err := d1.StopDaemonKeepServices(); err != nil {
		t.Fatalf("StopDaemonKeepServices: %v", err)
	}
	select {
	case <-d1.StopCh():
	case <-time.After(time.Second):
		t.Fatal("StopCh not closed")
	}

	// Start a replacement daemon and autostart
	d2 := orchestrator.New()
	started, skipped, err := d2.Autostart()
	if err != nil {
		t.Fatalf("Autostart: %v", err)
	}
	if started != 1 || skipped != 0 {
		t.Fatalf("Autostart started=%d, skipped=%d, want 1/0", started, skipped)
	}

	b2, err := d2.ProjectBackend("lc-adopt-test")
	if err != nil {
		t.Fatalf("ProjectBackend d2: %v", err)
	}

	states := b2.States()
	if len(states) != 1 {
		t.Fatalf("expected 1 service, got %d", len(states))
	}
	if int(states[0].Pid) != origPid {
		t.Fatalf("service PID changed after restart: got %d, want original %d (was not adopted)", states[0].Pid, origPid)
	}
	if states[0].Status != "running" {
		t.Fatalf("adopted service status = %q, want 'running'", states[0].Status)
	}

	// Clean up: stopping d2 kills the adopted process. d1's adoption monitor
	// (still alive in this process) observes the exit up to one poll interval
	// later and writes its final state — wait for that by stopping d1's
	// supervisor too, which blocks until its run loops exit, so nothing
	// writes into the TempDir after the test returns.
	_ = d2.StopDaemon()
	stopDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(stopDeadline) {
		d1States := b1.States()
		if len(d1States) == 0 || d1States[0].Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = d1.StopDaemon()
}
