// Package integration contains black-box end-to-end tests that build the
// real local-compose binary and drive it through the full CLI lifecycle
// (up -d, ps, logs, restart, down, build, up --build) against isolated XDG
// runtime/state dirs, plus the negative config paths.
//
// These codify the Phase 1 step 9 manual e2e test so regressions in the
// daemon, control socket, process-group teardown, and config validation are
// caught automatically.
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
	t.Cleanup(func() {
		_, _, _ = e.run(t, context.Background(), "down")
	})
	return e
}

// run executes the built binary with the given args, isolated XDG dirs, and
// cwd set to the config dir. It returns stdout, stderr, and the exit code.
// The timeout bounds hangs (e.g. a stuck `logs --follow`).
func (e *env) run(t *testing.T, ctx context.Context, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"-p", e.projName, "-f", e.configPath}, args...)
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
// output. Returns 0 if not found.
func pidFromPS(t *testing.T, psOut, name string) int {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(psOut), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != name {
			continue
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

// threeServiceLoopConfig is the canonical Phase 1 sample: alpha -> beta ->
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
	if !strings.Contains(errOut, "supervisor started") {
		t.Fatalf("up -d stderr missing supervisor-started message: %q", errOut)
	}

	// ps: all three services running in topo order.
	waitForCond(t, 3*time.Second, func() bool {
		psOut, _, rc := e.run(t, context.Background(), "ps")
		return rc == 0 &&
			strings.Contains(psOut, "alpha") && strings.Contains(psOut, "running") &&
			strings.Contains(psOut, "beta") && strings.Contains(psOut, "gamma")
	}, "ps shows all three services running")
	psOut, _, rc := e.run(t, context.Background(), "ps")
	if rc != 0 {
		t.Fatalf("ps: exit %d, out=%q", rc, psOut)
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

	// down: supervisor and all services stop, socket removed.
	_, downErr, rc := e.run(t, context.Background(), "down")
	if rc != 0 {
		t.Fatalf("down: exit %d, err=%q", rc, downErr)
	}
	waitForCond(t, 3*time.Second, func() bool {
		_, _, rc := e.run(t, context.Background(), "ps")
		return rc != 0
	}, "ps fails after down (no supervisor)")
	assertNoOrphans(t, pids...)

	socket := filepath.Join(e.runtime, "local-compose", e.projName, "supervisor.sock")
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket still present after down: stat=%v", err)
	}
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
	cmd := exec.CommandContext(ctx, binPath, "-p", e.projName, "-f", filepath.Join(e.cfgDir, "nope.yml"), "up", "-d")
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
	if !strings.Contains(errOut, "no supervisor running") {
		t.Fatalf("ps-with-no-supervisor error missing message: %q", errOut)
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
	if !strings.Contains(errOut, "no supervisor running") {
		t.Fatalf("idempotent down missing no-supervisor message: %q", errOut)
	}
}
