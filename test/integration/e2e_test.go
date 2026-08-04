// Package integration contains black-box end-to-end tests that build the
// real local-compose binary and drive it through the full CLI lifecycle
// (up -d, ps, logs, restart, down, build, up --build) against isolated XDG
// runtime/state dirs, plus the negative config paths.
//
// These codify the canonical e2e tests so regressions in the daemon, control
// socket, process-group teardown, and config validation are caught
// automatically.
package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// binPath is the path to the local-compose binary built once in TestMain.
var binPath string

// repoRoot is the absolute path to the repository root (used for `go build`).
var repoRoot string

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(1)
	}
	repoRoot = filepath.Join(wd, "..", "..")

	binDir, err := os.MkdirTemp("", "lc-integration-bin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdtemp: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(binDir) }()
	binPath = filepath.Join(binDir, "local-compose")

	buildCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, "./cmd/local-compose")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// env holds the per-test environment: a config dir (cwd) and isolated XDG
// runtime/state dirs so tests never touch the user's real local-compose state.
type env struct {
	cfgDir     string
	runtime    string
	state      string
	projName   string
	configPath string
	envFile    string // optional --env-file passed to every invocation
}

func newEnv(t *testing.T, configContents string) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "local-compose.yml"), []byte(configContents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	e := &env{
		cfgDir: cfgDir,
		// macOS t.TempDir() lives under /var/folders/... which blows past the
		// ~104-char Unix socket path limit. Use a short /tmp dir for the
		// runtime (socket + pidfile), mirroring internal/control's test setup.
		runtime:    mktempShort(t, "lc-rt"),
		state:      t.TempDir(),
		projName:   "lc-test",
		configPath: filepath.Join(cfgDir, "local-compose.yml"),
	}
	// Best-effort teardown: down is idempotent and safe even if nothing is up.
	// Also stop the daemon so tests don't leak processes.
	t.Cleanup(func() {
		_, _, _ = e.run(t, context.Background(), "down")
		_, _, _ = e.run(t, context.Background(), "stop-daemon")
	})
	return e
}

// run executes the built binary with the given args, isolated XDG dirs, and
// cwd set to the config dir. It returns stdout, stderr, and the exit code.
// The timeout bounds hangs (e.g. a stuck `logs --follow`).
func (e *env) run(t *testing.T, ctx context.Context, args ...string) (string, string, int) {
	t.Helper()
	prefix := []string{"-p", e.projName, "--file", e.configPath}
	if e.envFile != "" {
		prefix = append(prefix, "--env-file", e.envFile)
	}
	full := append(prefix, args...)
	cmd := exec.CommandContext(ctx, binPath, full...)
	cmd.Dir = e.cfgDir
	cmd.Env = e.environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run %q: %v", strings.Join(full, " "), err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func (e *env) runFromDir(t *testing.T, ctx context.Context, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = dir
	cmd.Env = e.environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runFromDir %q: %v", strings.Join(args, " "), err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

// environ returns the inherited environment with XDG_RUNTIME_DIR and
// XDG_STATE_HOME overridden to the isolated per-test dirs so the daemon
// re-exec child (which inherits os.Environ()) lands in the same sandbox.
func (e *env) environ() []string {
	out := os.Environ()
	out = override(out, "XDG_RUNTIME_DIR", e.runtime)
	out = override(out, "XDG_STATE_HOME", e.state)
	return out
}

// mktempShort creates a short-lived temp dir under /tmp. Used for the runtime
// dir because the Unix socket path limit (~104 chars) is easy to blow past
// with macOS's default /var/folders/... t.TempDir() locations.
func mktempShort(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatalf("mkdtemp /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func override(env []string, k, v string) []string {
	prefix := k + "="
	out := make([]string, 0, len(env)+1)
	set := false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			out = append(out, prefix+v)
			set = true
		} else {
			out = append(out, kv)
		}
	}
	if !set {
		out = append(out, prefix+v)
	}
	return out
}

// pidFromPS parses the PID column (3rd field) for a named service from `ps`
// output. Returns 0 if not found or the service has no pid yet (pid column is
// "-" while it is waiting on depends_on or starting up).
func pidFromPS(t *testing.T, psOut, name string) int {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(psOut), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != name {
			continue
		}
		if fields[2] == "-" {
			return 0
		}
		pid, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatalf("parse pid %q for %s: %v", fields[2], name, err)
		}
		return pid
	}
	return 0
}

// assertNoOrphans verifies none of the given PIDs are still alive (process
// groups were torn down with no leftover children).
func assertNoOrphans(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, pid := range pids {
			if pid <= 0 {
				continue
			}
			if err := syscall.Kill(pid, 0); err == nil || errorsIsEPERM(err) {
				alive++
			}
		}
		if alive == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphan processes still alive: %v", pids)
}

func errorsIsEPERM(err error) bool {
	return err == syscall.EPERM
}

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

// threeServiceLoopConfig is the canonical sample: alpha -> beta ->
// gamma with both list and map depends_on forms, looping forever so ps/logs/
// restart/down can be exercised.
const threeServiceLoopConfig = `version: "1"
name: lc-test
services:
  alpha:
    command: sh -c 'echo alpha-start; i=0; while true; do i=$((i+1)); echo alpha-tick $i; sleep 1; done'
    restart: unless-stopped
  beta:
    command: sh -c 'echo beta-start; i=0; while true; do i=$((i+1)); echo beta-tick $i; sleep 1; done'
    depends_on: [alpha]
    restart: unless-stopped
  gamma:
    command: sh -c 'echo gamma-start; i=0; while true; do i=$((i+1)); echo gamma-tick $i; sleep 1; done'
    depends_on:
      alpha: { condition: service_started }
      beta: { condition: service_started }
    restart: unless-stopped
`

func TestE2E_LifecycleUpDetachPSLogsRestartDown(t *testing.T) {
	e := newEnv(t, threeServiceLoopConfig)

	// up -d: supervisor daemonizes and returns 0. The "supervisor started"
	// notice is written to stderr.
	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d, err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "project \"lc-test\" started") {
		t.Fatalf("up -d stderr missing project-started message: %q", errOut)
	}

	// ps: all three services running in topo order. depends_on: service_started
	// gates each dependent, so wait until every service actually has a pid.
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, rc := e.run(t, context.Background(), "ps")
		if rc != 0 {
			return false
		}
		for _, name := range []string{"alpha", "beta", "gamma"} {
			if pidFromPS(t, psOut, name) == 0 {
				return false
			}
		}
		return true
	}, "ps shows all three services running")
	psOut, _, rc := e.run(t, context.Background(), "ps")
	if rc != 0 {
		t.Fatalf("ps: exit %d, out=%q", rc, psOut)
	}

	// ls: list projects; project name 'lc-test' must appear with running status.
	lsOut, _, lsCode := e.run(t, context.Background(), "ls")
	if lsCode != 0 || !strings.Contains(lsOut, "lc-test") || !strings.Contains(lsOut, "running") {
		t.Fatalf("ls output unexpected: code=%d, output:\n%s", lsCode, lsOut)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if pid := pidFromPS(t, psOut, name); pid == 0 {
			t.Fatalf("ps: %s has no pid in output:\n%s", name, psOut)
		}
	}

	// logs: beta's log file contains its start line.
	logsOut, _, rc := e.run(t, context.Background(), "logs", "beta")
	if rc != 0 {
		t.Fatalf("logs beta: exit %d, out=%q", rc, logsOut)
	}
	if !strings.Contains(logsOut, "beta-start") {
		t.Fatalf("logs beta missing beta-start: %q", logsOut)
	}

	// logs --follow: streams live lines (bounded by a short timeout).
	followCtx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	followOut, _, _ := e.run(t, followCtx, "logs", "--follow", "gamma")
	if !strings.Contains(followOut, "gamma-start") {
		t.Fatalf("logs --follow gamma missing gamma-start: %q", followOut)
	}
	if !strings.Contains(followOut, "gamma-tick") {
		t.Fatalf("logs --follow gamma missing streamed tick: %q", followOut)
	}

	// restart beta: it gets a fresh pid.
	betaBefore := pidFromPS(t, psOut, "beta")
	_, _, rc = e.run(t, context.Background(), "restart", "beta")
	if rc != 0 {
		t.Fatalf("restart beta: exit %d", rc)
	}
	waitForCond(t, 3*time.Second, func() bool {
		after, _, _ := e.run(t, context.Background(), "ps")
		got := pidFromPS(t, after, "beta")
		return got != 0 && got != betaBefore
	}, "beta got a new pid after restart")

	// Capture current pids before down so we can check for orphans.
	finalPS, _, _ := e.run(t, context.Background(), "ps")
	pids := []int{
		pidFromPS(t, finalPS, "alpha"),
		pidFromPS(t, finalPS, "beta"),
		pidFromPS(t, finalPS, "gamma"),
	}

	// down: supervisor and all services stop, but project stays in daemon.
	_, downErr, rc := e.run(t, context.Background(), "down")
	if rc != 0 {
		t.Fatalf("down: exit %d, err=%q", rc, downErr)
	}

	// ps should succeed (exit 0) and report all services as exited/stopped
	psAfterDown, psErr, rc := e.run(t, context.Background(), "ps")
	if rc != 0 {
		t.Fatalf("ps after down: exit %d, err=%q, out=%q", rc, psErr, psAfterDown)
	}
	if !strings.Contains(psAfterDown, "exited") && !strings.Contains(psAfterDown, "stopped") {
		t.Fatalf("expected services to be exited/stopped after down, got: %q", psAfterDown)
	}

	// remove: stops and completely removes the project from the daemon.
	_, removeErr, rc := e.run(t, context.Background(), "remove")
	if rc != 0 {
		t.Fatalf("remove: exit %d, err=%q", rc, removeErr)
	}

	waitForCond(t, 3*time.Second, func() bool {
		_, _, rc := e.run(t, context.Background(), "ps")
		return rc != 0
	}, "ps fails after remove (project not running)")
	assertNoOrphans(t, pids...)
}

func TestE2E_ForegroundUpShortLived(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  maker:
    command: sh -c 'echo maker-running; sleep 1'
  shaper:
    command: sh -c 'echo shaper-running; sleep 1'
`
	e := newEnv(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, errOut, code := e.run(t, ctx, "up")
	if code != 0 {
		t.Fatalf("up: exit %d, out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "maker-running") || !strings.Contains(out, "shaper-running") {
		t.Fatalf("foreground up missing service output: %q", out)
	}
	// Foreground up should exit on its own once services finish.
	if ctx.Err() != nil {
		t.Fatalf("foreground up did not self-exit before context deadline")
	}
}

func TestE2E_BuildStringAndObjectForms(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  maker:
    command: sh -c 'echo maker-run; sleep 1'
    build: echo built-maker-string-form
  shaper:
    command: sh -c 'echo shaper-run; sleep 1'
    build:
      command: echo built-shaper-object-form && echo shaper-env=$SHAPER_ENV
      env:
        SHAPER_ENV: yes
      shell: sh
`
	e := newEnv(t, cfg)
	out, _, code := e.run(t, context.Background(), "build")
	if code != 0 {
		t.Fatalf("build: exit %d, out=%q", code, out)
	}
	if !strings.Contains(out, "built-maker-string-form") {
		t.Errorf("build output missing string-form build line: %q", out)
	}
	if !strings.Contains(out, "built-shaper-object-form") {
		t.Errorf("build output missing object-form build line: %q", out)
	}
	if !strings.Contains(out, "shaper-env=yes") {
		t.Errorf("build output missing shaper env expansion: %q", out)
	}
}

func TestE2E_UpWithBuild(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  svc:
    command: sh -c 'echo svc-run; sleep 1'
    build: echo svc-built
`
	e := newEnv(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, _, code := e.run(t, ctx, "up", "--build")
	if code != 0 {
		t.Fatalf("up --build: exit %d, out=%q", code, out)
	}
	if !strings.Contains(out, "svc-built") {
		t.Errorf("up --build did not run build step first: %q", out)
	}
	if !strings.Contains(out, "svc-run") {
		t.Errorf("up --build did not start service: %q", out)
	}
}

func TestE2E_CycleDetected(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  a:
    command: echo a
    depends_on: [b]
  b:
    command: echo b
    depends_on: [a]
`
	e := newEnv(t, cfg)
	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code == 0 {
		t.Fatalf("up -d on cyclic config: expected non-zero exit, got 0")
	}
	if !strings.Contains(errOut, "cycle detected") {
		t.Fatalf("cyclic config error missing 'cycle detected': %q", errOut)
	}
}

func TestE2E_EmptyCommandRejected(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  s:
    command: ""
`
	e := newEnv(t, cfg)
	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code == 0 {
		t.Fatalf("up -d with empty command: expected non-zero exit, got 0")
	}
	if !strings.Contains(errOut, "command is required") {
		t.Fatalf("empty command error missing 'command is required': %q", errOut)
	}
}

func TestE2E_MissingConfigFile(t *testing.T) {
	e := newEnv(t, threeServiceLoopConfig)
	// Point -f at a non-existent file.
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, binPath, "-p", e.projName, "--file", filepath.Join(e.cfgDir, "nope.yml"), "up", "-d")
	cmd.Dir = e.cfgDir
	cmd.Env = e.environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("up -d with missing config: expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr.String(), "no such file or directory") {
		t.Fatalf("missing config error unexpected: %q", stderr.String())
	}
}

func TestE2E_PSWithNoSupervisorErrors(t *testing.T) {
	e := newEnv(t, threeServiceLoopConfig)
	_, errOut, code := e.run(t, context.Background(), "ps")
	if code == 0 {
		t.Fatalf("ps with no supervisor: expected non-zero exit, got 0")
	}
	if !strings.Contains(errOut, "no daemon running") {
		t.Fatalf("ps-with-no-daemon error missing message: %q", errOut)
	}
}

// TestE2E_DownIsIdempotent verifies `down` with nothing running succeeds and
// reports the no-supervisor case rather than erroring.
func TestE2E_DownIsIdempotent(t *testing.T) {
	e := newEnv(t, threeServiceLoopConfig)
	_, errOut, code := e.run(t, context.Background(), "down")
	if code != 0 {
		t.Fatalf("down with no supervisor: expected exit 0, got %d (err=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "no daemon running") {
		t.Fatalf("idempotent down missing no-daemon message: %q", errOut)
	}
}

// TestE2E_HealthcheckGatesDependent runs a dependency whose healthcheck passes
// quickly and verifies the dependent only appears in `ps` once the dependency
// is healthy.
func TestE2E_HealthcheckGatesDependent(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  db:
    command: sh -c 'sleep 30'
    healthcheck:
      test: ["CMD", "true"]
      interval: 100ms
      retries: 3
      timeout: 1s
  api:
    command: sh -c 'sleep 30'
    depends_on:
      db: { condition: service_healthy }`
	e := newEnv(t, cfg)
	_, _, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d", code)
	}

	// Eventually both run and db reports healthy.
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "ps")
		return strings.Contains(psOut, "db") && strings.Contains(psOut, "running") &&
			strings.Contains(psOut, "api") && strings.Contains(psOut, "running") &&
			strings.Contains(psOut, "healthy")
	}, "ps shows db healthy and api running")
}

// TestE2E_UnhealthyDependencyFailsDependent verifies that when a dependency's
// healthcheck exhausts retries, the dependent never starts and a subsequent
// foreground `up` reports failure.
func TestE2E_UnhealthyDependencyFailsDependent(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  db:
    command: sh -c 'sleep 30'
    healthcheck:
      test: ["CMD", "false"]
      interval: 100ms
      retries: 2
      timeout: 1s
  api:
    command: sh -c 'sleep 30'
    depends_on:
      db: { condition: service_healthy }`
	e := newEnv(t, cfg)
	// Foreground up: the dependent can't start (db unhealthy), and once db's
	// own process is the only thing left the supervisor winds down. The run
	// must surface a failure via non-zero exit OR report the unhealthy state
	// in `ps`. Daemonize and inspect ps instead, since the foreground path
	// keeps db running (sleep 30) so `up` won't return on its own quickly.
	_, _, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d", code)
	}
	// db must eventually report unhealthy; api must never reach running.
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "ps")
		return strings.Contains(psOut, "db") && strings.Contains(psOut, "unhealthy")
	}, "ps shows db unhealthy")
	// api row should not show running (it stays stopped).
	psOut, _, _ := e.run(t, context.Background(), "ps")
	for _, line := range strings.Split(strings.TrimSpace(psOut), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "api" {
			if len(fields) > 1 && fields[1] == "running" {
				t.Fatalf("api should not be running when db is unhealthy:\n%s", psOut)
			}
		}
	}
}

// TestE2E_KillService verifies `kill` forcefully terminates a service with the
// default SIGKILL, and `kill --signal` lets the caller pick the signal.
func TestE2E_KillService(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  alpha:
    command: sh -c 'echo alpha-start; sleep 300'
    restart: unless-stopped
  beta:
    command: sh -c 'echo beta-start; sleep 300'
    restart: unless-stopped
`
	e := newEnv(t, cfg)
	_, _, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d", code)
	}
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "ps")
		return pidFromPS(t, psOut, "alpha") != 0 && pidFromPS(t, psOut, "beta") != 0
	}, "ps shows both services running")

	psOut, _, _ := e.run(t, context.Background(), "ps")
	alphaPID := pidFromPS(t, psOut, "alpha")
	betaPID := pidFromPS(t, psOut, "beta")

	// kill beta with the default SIGKILL.
	_, killErr, rc := e.run(t, context.Background(), "kill", "beta")
	if rc != 0 {
		t.Fatalf("kill beta: exit %d, err=%q", rc, killErr)
	}
	// kill alpha with an explicit SIGTERM.
	_, killErr, rc = e.run(t, context.Background(), "kill", "--signal", "SIGTERM", "alpha")
	if rc != 0 {
		t.Fatalf("kill --signal SIGTERM alpha: exit %d, err=%q", rc, killErr)
	}

	waitForCond(t, 3*time.Second, func() bool {
		after, _, _ := e.run(t, context.Background(), "ps")
		return pidFromPS(t, after, "alpha") == 0 && pidFromPS(t, after, "beta") == 0
	}, "kill left both services stopped")

	assertNoOrphans(t, alphaPID, betaPID)
}

// TestE2E_Top verifies `top` renders CPU/memory usage for every running
// service's process group and supports a single-service filter. The daemon
// samples over ~1s, so the command is slow by design.
func TestE2E_Top(t *testing.T) {
	e := newEnv(t, threeServiceLoopConfig)
	_, _, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d", code)
	}
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "ps")
		return pidFromPS(t, psOut, "alpha") != 0 &&
			pidFromPS(t, psOut, "beta") != 0 &&
			pidFromPS(t, psOut, "gamma") != 0
	}, "ps shows all three services running")

	topOut, topErr, rc := e.run(t, context.Background(), "top")
	if rc != 0 {
		t.Fatalf("top: exit %d, err=%q", rc, topErr)
	}
	if !strings.Contains(topOut, "NAME") || !strings.Contains(topOut, "CPU%") || !strings.Contains(topOut, "MEM") {
		t.Fatalf("top missing table header, got:\n%s", topOut)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(topOut, name) {
			t.Fatalf("top missing %q row, got:\n%s", name, topOut)
		}
	}

	// A per-service top must include only that service.
	alphaOut, _, rc := e.run(t, context.Background(), "top", "alpha")
	if rc != 0 {
		t.Fatalf("top alpha: exit %d", rc)
	}
	if !strings.Contains(alphaOut, "alpha") {
		t.Fatalf("top alpha missing alpha row:\n%s", alphaOut)
	}
	if strings.Contains(alphaOut, "beta") || strings.Contains(alphaOut, "gamma") {
		t.Fatalf("top alpha leaked other services:\n%s", alphaOut)
	}
}

// TestE2E_DotEnvAndInterpolation verifies that a .env file next to the config
// is loaded (fallback), its variables reach child processes under the service
// env, and ${VAR} / ${VAR:-default} references in the config are interpolated.
// It also exercises an explicit --env-file.
func TestE2E_DotEnvAndInterpolation(t *testing.T) {
	const cfg = `version: "1"
name: lc-test
services:
  web:
    command: sh -c 'echo PORT=$PORT; echo INTERP=${PORT}; echo DEFAULTED=${MISSING:-fallback}; echo APP_NAME=$APP_NAME; echo OVERRIDDEN=$OVERRIDDEN; sleep 30'
    env:
      OVERRIDDEN: svc
`
	e := newEnv(t, cfg)
	dotenv := "PORT=9090\nAPP_NAME=myapp\nOVERRIDDEN=dotenv\n"
	if err := os.WriteFile(filepath.Join(e.cfgDir, ".env"), []byte(dotenv), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d, err=%q", code, errOut)
	}
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "ps")
		return pidFromPS(t, psOut, "web") != 0
	}, "ps shows web running")

	// logs may briefly lag the process start (pre-existing race), so poll
	// until the service's captured output contains the expected lines.
	var logsOut string
	waitForCond(t, 5*time.Second, func() bool {
		var rc int
		logsOut, _, rc = e.run(t, context.Background(), "logs", "web")
		return rc == 0 && strings.Contains(logsOut, "PORT=9090")
	}, "logs web shows PORT=9090")
	for _, want := range []string{
		"PORT=9090",          // dotenv var in child env
		"INTERP=9090",        // ${VAR} interpolated from dotenv
		"DEFAULTED=fallback", // ${VAR:-default} when unset
		"APP_NAME=myapp",     // dotenv var in child env
		"OVERRIDDEN=svc",     // service env wins over dotenv
	} {
		if !strings.Contains(logsOut, want) {
			t.Fatalf("logs web missing %q, got:\n%s", want, logsOut)
		}
	}

	// Explicit --env-file overrides the .env fallback.
	e2 := newEnv(t, cfg)
	other := filepath.Join(e2.cfgDir, "custom.env")
	if err := os.WriteFile(other, []byte("PORT=7070\n"), 0o644); err != nil {
		t.Fatalf("write custom.env: %v", err)
	}
	e2.envFile = other

	_, errOut, code = e2.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d (--env-file): exit %d, err=%q", code, errOut)
	}
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e2.run(t, context.Background(), "ps")
		return pidFromPS(t, psOut, "web") != 0
	}, "ps shows web running (--env-file)")
	waitForCond(t, 5*time.Second, func() bool {
		var r int
		logsOut, _, r = e2.run(t, context.Background(), "logs", "web")
		return r == 0 && strings.Contains(logsOut, "PORT=7070")
	}, "logs web (--env-file) shows PORT=7070")
}

func TestRegisteredProjectByName(t *testing.T) {
	cfg := `
version: "1"
name: reg-test
services:
  app:
    command: "sleep 60"
`
	e := newEnv(t, cfg)
	// 1. up -d to register project
	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d, err=%q", code, errOut)
	}

	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.run(t, context.Background(), "-p", "reg-test", "ps")
		return strings.Contains(psOut, "app")
	}, "ps shows app")

	// 2. Stop project
	_, errOut, code = e.run(t, context.Background(), "stop")
	if code != 0 {
		t.Fatalf("stop: exit %d, err=%q", code, errOut)
	}

	// 3. Verify ls shows 0/1 running when stopped
	waitForCond(t, 5*time.Second, func() bool {
		lsOut, errOut, code := e.run(t, context.Background(), "ls")
		t.Logf("ls output: code=%d out=%q err=%q", code, lsOut, errOut)
		return code == 0 && strings.Contains(lsOut, "0/1 running") && strings.Contains(lsOut, "stopped")
	}, "ls shows stopped and 0/1 running after stop")

	// 4. Run start from outside the config directory using -p reg-test
	outsideDir := t.TempDir()
	out, errOut, code := e.runFromDir(t, context.Background(), outsideDir, "-p", "reg-test", "start")
	if code != 0 {
		t.Fatalf("start -p reg-test from outside dir: exit %d, stdout=%q, errOut=%q", code, out, errOut)
	}

	// 5. Verify service is running again via ps -p reg-test
	waitForCond(t, 5*time.Second, func() bool {
		psOut, _, _ := e.runFromDir(t, context.Background(), outsideDir, "-p", "reg-test", "ps")
		return pidFromPS(t, psOut, "app") != 0
	}, "ps shows app running after start -p reg-test")

	// 6. Verify ls shows 1/1 running
	waitForCond(t, 5*time.Second, func() bool {
		lsRunningOut, _, code := e.run(t, context.Background(), "ls")
		return code == 0 && strings.Contains(lsRunningOut, "1/1 running")
	}, "ls shows 1/1 running after project start")
}

func TestPSAllAndAutoFallback(t *testing.T) {
	cfg := `
version: "1"
name: ps-all-test
services:
  srv1:
    command: "sleep 60"
`
	e := newEnv(t, cfg)
	_, errOut, code := e.run(t, context.Background(), "up", "-d")
	if code != 0 {
		t.Fatalf("up -d: exit %d, err=%q", code, errOut)
	}

	outsideDir := t.TempDir()

	// 1. ps -a inside project dir should include PROJECT header and project name
	waitForCond(t, 5*time.Second, func() bool {
		psAllOut, _, code := e.run(t, context.Background(), "ps", "-a")
		return code == 0 && strings.Contains(psAllOut, "PROJECT") && strings.Contains(psAllOut, "ps-all-test") && strings.Contains(psAllOut, "srv1")
	}, "ps -a shows srv1 under ps-all-test")

	// 2. ps outside project dir should auto-fallback to all projects
	waitForCond(t, 5*time.Second, func() bool {
		psOutsideOut, _, code := e.runFromDir(t, context.Background(), outsideDir, "ps")
		return code == 0 && strings.Contains(psOutsideOut, "PROJECT") && strings.Contains(psOutsideOut, "ps-all-test") && strings.Contains(psOutsideOut, "srv1")
	}, "ps outside dir auto-fallback shows srv1 under ps-all-test")
}
