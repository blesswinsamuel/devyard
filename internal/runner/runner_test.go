//go:build unix

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/blesswinsamuel/devyard/internal/logstore"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == Flag {
		if err := Main(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("RUNNER_TEST_HELPER") == "escape" {
		// Spawn a grandchild in a new session that keeps our stdout open,
		// then exit: the classic "daemonizing tool" that used to wedge
		// supervision.
		cmd := exec.Command("sleep", "30")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Println("escaped", cmd.Process.Pid)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var seq atomic.Int64

func testSpec(t *testing.T, kind, command string) Spec {
	t.Helper()
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("/tmp", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	return Spec{
		Project:   "p",
		Kind:      kind,
		Name:      "n",
		Run:       seq.Add(1),
		Argv:      []string{"sh", "-c", command},
		Display:   command,
		Dir:       dir,
		Env:       []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir},
		ProcDir:   filepath.Join(dir, "proc"),
		Socket:    filepath.Join(sockDir, "r.sock"),
		StopGrace: 5 * time.Second,
	}
}

func launch(t *testing.T, spec Spec) *Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Launch(context.Background(), spec, LaunchOptions{Exe: exe})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = p.Stop(ctx, 100*time.Millisecond)
	})
	return p
}

func wait(t *testing.T, p *Process, timeout time.Duration) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	st, err := p.Wait(ctx)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	return st
}

func logText(t *testing.T, spec Spec) string {
	t.Helper()
	lines, _, err := logstore.Tail(spec.ProcDir, spec.Run, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Stream.String() + ":" + l.Text + "\n")
	}
	return b.String()
}

func TestRunExitCodeAndLogs(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo hello; echo oops >&2; exit 3")
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 3 || st.Stopped || st.Lost {
		t.Fatalf("status: %+v", st)
	}
	logs := logText(t, spec)
	for _, want := range []string{"system:$ echo hello", "stdout:hello", "stderr:oops", "system:exited with code 3"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestTTYServiceStarts(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", `if [ -t 1 ]; then echo "is a tty"; else echo "not a tty"; fi`)
	spec.TTY = true
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 0 {
		t.Fatalf("status: %+v\n%s", st, logText(t, spec))
	}
	if logs := logText(t, spec); !strings.Contains(logs, "stdout:is a tty") {
		t.Fatalf("expected tty output, got:\n%s", logs)
	}
}

func TestRunnerIsItsOwnSession(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "sleep 30")
	p := launch(t, spec)
	st := waitPhase(t, p, PhaseRunning)
	sid, err := unix.Getsid(st.RunnerPID)
	if err != nil {
		t.Fatal(err)
	}
	if sid != st.RunnerPID {
		t.Fatalf("runner sid = %d, want %d", sid, st.RunnerPID)
	}
	pgid, err := unix.Getpgid(st.PID)
	if err != nil || pgid != st.PID || st.PGID != st.PID {
		t.Fatalf("child pgid = %d (%v), status %+v", pgid, err, st)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", `trap "" TERM; echo ready; while true; do sleep 0.05; done`)
	p := launch(t, spec)
	waitForLog(t, spec, "stdout:ready")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := p.Stop(ctx, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Stopped || st.Signal != "SIGKILL" {
		t.Fatalf("status: %+v", st)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stop took %v", d)
	}
}

func TestSignalDuringStopKillsImmediately(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", `trap "" TERM; echo ready; while true; do sleep 0.05; done`)
	spec.StopGrace = 30 * time.Second
	p := launch(t, spec)
	waitForLog(t, spec, "stdout:ready")
	done := make(chan Status, 1)
	go func() {
		st, _ := p.Stop(context.Background(), 30*time.Second)
		done <- st
	}()
	time.Sleep(200 * time.Millisecond)
	if err := p.Signal(context.Background(), "SIGKILL"); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-done:
		if st.Signal != "SIGKILL" {
			t.Fatalf("status: %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not complete after SIGKILL")
	}
}

func TestEscapedGrandchildDoesNotWedge(t *testing.T) {
	t.Parallel()
	exe, _ := os.Executable()
	spec := testSpec(t, "service", "RUNNER_TEST_HELPER=escape "+exe)
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 0 {
		t.Fatalf("status: %+v\n%s", st, logText(t, spec))
	}
	logs := logText(t, spec)
	var pid int
	for _, line := range strings.Split(logs, "\n") {
		if strings.HasPrefix(line, "stdout:escaped ") {
			_, _ = fmt.Sscanf(line, "stdout:escaped %d", &pid)
		}
	}
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func TestStragglersKilledOnExit(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", `sleep 30 & echo "bg $!"`)
	p := launch(t, spec)
	wait(t, p, 5*time.Second)
	var pid int
	for _, line := range strings.Split(logText(t, spec), "\n") {
		if strings.HasPrefix(line, "stdout:bg ") {
			_, _ = fmt.Sscanf(line, "stdout:bg %d", &pid)
		}
	}
	if pid == 0 {
		t.Fatal("no background pid logged")
	}
	deadline := time.Now().Add(2 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background process %d survived the run", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInteractiveTTYTask(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "task", `printf "Name? "; read name; echo "hello $name"`)
	spec.TTY = true
	p := launch(t, spec)
	a, err := p.Attach(context.Background(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if !a.TTY || !a.Stdin {
		t.Fatalf("attach flags tty=%v stdin=%v", a.TTY, a.Stdin)
	}
	out := readUntil(t, a, "Name? ")
	_ = out
	if _, err := a.Write([]byte("bob\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, a, "hello bob")
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestInteractivePipeTask(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "task", `read x; echo "got $x"; cat; echo "eof"`)
	p := launch(t, spec)
	a, err := p.Attach(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if a.TTY || !a.Stdin {
		t.Fatalf("attach flags tty=%v stdin=%v", a.TTY, a.Stdin)
	}
	_, _ = a.Write([]byte("abc\n"))
	readUntil(t, a, "got abc")
	_ = a.CloseStdin()
	readUntil(t, a, "eof")
	if st := wait(t, p, 5*time.Second); st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestServiceStdinIsNotInteractive(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", `cat; echo "stdin closed"`)
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 0 || !strings.Contains(logText(t, spec), "stdin closed") {
		t.Fatalf("status %+v logs:\n%s", st, logText(t, spec))
	}
}

func TestBuildFailureSkipsCommand(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo should-not-run")
	spec.Build = &BuildSpec{Argv: []string{"sh", "-c", "echo building; exit 4"}, Display: "echo building; exit 4", Dir: spec.Dir, Env: spec.Env}
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if !st.BuildFailed || st.ExitCode != 4 {
		t.Fatalf("status %+v", st)
	}
	logs := logText(t, spec)
	if strings.Contains(logs, "should-not-run") || !strings.Contains(logs, "stdout:building") {
		t.Fatalf("logs:\n%s", logs)
	}
}

func TestBuildThenRun(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "cat artifact")
	spec.Build = &BuildSpec{Argv: []string{"sh", "-c", "echo built > artifact"}, Display: "echo built > artifact", Dir: spec.Dir, Env: spec.Env}
	p := launch(t, spec)
	st := wait(t, p, 5*time.Second)
	if st.ExitCode != 0 || !strings.Contains(logText(t, spec), "stdout:built") {
		t.Fatalf("status %+v logs:\n%s", st, logText(t, spec))
	}
}

func TestStopDuringBuild(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo should-not-run")
	spec.Build = &BuildSpec{Argv: []string{"sh", "-c", "echo building; sleep 30"}, Display: "echo building; sleep 30", Dir: spec.Dir, Env: spec.Env}
	p := launch(t, spec)
	waitForLog(t, spec, "stdout:building")
	st, err := p.Stop(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Stopped || strings.Contains(logText(t, spec), "should-not-run") {
		t.Fatalf("status %+v logs:\n%s", st, logText(t, spec))
	}
}

func TestReopenFromStatusFile(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo ready; sleep 30")
	launch(t, spec)
	waitForLog(t, spec, "stdout:ready")
	p2, st, err := Open(spec.ProcDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseRunning || st.PID == 0 {
		t.Fatalf("status: %+v", st)
	}
	live, err := p2.Status(context.Background())
	if err != nil || live.PID != st.PID {
		t.Fatalf("live: %+v %v", live, err)
	}
}

func TestLostRunnerKillsGroup(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo ready; sleep 30")
	p := launch(t, spec)
	waitForLog(t, spec, "stdout:ready")
	st, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(st.RunnerPID, syscall.SIGKILL)
	final := wait(t, p, 5*time.Second)
	if !final.Lost {
		t.Fatalf("expected lost, got %+v", final)
	}
	deadline := time.Now().Add(2 * time.Second)
	for Alive(st.PID) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d survived lost runner", st.PID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParseSignal(t *testing.T) {
	for in, want := range map[string]syscall.Signal{"": syscall.SIGKILL, "TERM": syscall.SIGTERM, "sighup": syscall.SIGHUP, "9": syscall.SIGKILL} {
		got, err := ParseSignal(in)
		if err != nil || got != want {
			t.Errorf("ParseSignal(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseSignal("NOPE"); err == nil {
		t.Error("expected error")
	}
}

func waitPhase(t *testing.T, p *Process, phase string) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := p.Status(context.Background())
		if err == nil && st.Phase == phase {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase %q never reached (last %+v, %v)", phase, st, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForLog(t *testing.T, spec Spec, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _, err := logstore.Tail(spec.ProcDir, spec.Run, 0, 0)
		if err == nil {
			for _, l := range lines {
				if l.Stream.String()+":"+l.Text == want {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("log line %q never appeared", want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readUntil(t *testing.T, a *Attachment, want string) string {
	t.Helper()
	var got strings.Builder
	done := make(chan error, 1)
	go func() {
		for {
			chunk, err := a.Read()
			if err != nil {
				var exit *ExitError
				if errors.As(err, &exit) && strings.Contains(got.String(), want) {
					done <- nil
					return
				}
				done <- err
				return
			}
			got.Write(chunk)
			if strings.Contains(got.String(), want) {
				done <- nil
				return
			}
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read until %q: %v (got %q)", want, err, got.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
	return got.String()
}

func TestWatchReportsPhases(t *testing.T) {
	t.Parallel()
	spec := testSpec(t, "service", "echo run; sleep 0.2")
	spec.Build = &BuildSpec{Argv: []string{"sh", "-c", "sleep 0.2"}, Display: "sleep 0.2", Dir: spec.Dir, Env: spec.Env}
	p := launch(t, spec)
	var phases []string
	final, err := p.Watch(context.Background(), func(st Status) {
		if len(phases) == 0 || phases[len(phases)-1] != st.Phase {
			phases = append(phases, st.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(phases, ",")
	if !strings.HasSuffix(got, "building,running,exited") || final.ExitCode != 0 {
		t.Fatalf("phases %s final %+v", got, final)
	}
}
