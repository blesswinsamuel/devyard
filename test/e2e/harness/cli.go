package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultCLITimeout bounds every CLI invocation (scaled) so a wedged daemon
// fails the test instead of hanging it.
const DefaultCLITimeout = 60 * time.Second

// Result is the outcome of one command.
type Result struct {
	Args     []string
	Dir      string
	Stdout   string
	Stderr   string
	Code     int // -1 when the command could not run or timed out
	Err      error
	Duration time.Duration
	TimedOut bool
}

func (r Result) String() string {
	return fmt.Sprintf("$ (cd %s && %s)\nexit=%d dur=%s err=%v timedOut=%v\n--- stdout ---\n%s\n--- stderr ---\n%s",
		r.Dir, strings.Join(r.Args, " "), r.Code, r.Duration.Round(time.Millisecond), r.Err, r.TimedOut, clip(r.Stdout, 8000), clip(r.Stderr, 8000))
}

// Output is stdout followed by stderr.
func (r Result) Output() string { return r.Stdout + r.Stderr }

// MustSucceed fails t unless the command exited 0.
func (r Result) MustSucceed(t TB) Result {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("command failed:\n%s", r)
	}
	return r
}

// MustFail fails t if the command exited 0 (or did not run at all).
func (r Result) MustFail(t TB) Result {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("command unexpectedly succeeded:\n%s", r)
	}
	if r.Code < 0 {
		t.Fatalf("command did not complete:\n%s", r)
	}
	return r
}

// JSON decodes stdout into v, failing t on error.
func (r Result) JSON(t TB, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), v); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, r)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(" + fmt.Sprint(len(s)-n) + " bytes elided)...\n" + s[len(s)-n:]
}

// RunOpts customizes a command.
type RunOpts struct {
	Dir     string        // default: sandbox HOME
	Env     []string      // appended to the sandbox env (later entries win)
	Stdin   io.Reader     // default: /dev/null (not a TTY)
	Timeout time.Duration // unscaled; default DefaultCLITimeout
}

func (sb *Sandbox) devyard() string {
	sb.t.Helper()
	bin, err := DevyardPath()
	if err != nil {
		sb.t.Fatalf("devyard binary unavailable (build failed):\n%v", err)
	}
	return bin
}

// CLI runs `devyard args...` with cwd = the sandbox HOME.
func (sb *Sandbox) CLI(args ...string) Result {
	sb.t.Helper()
	return sb.CLIWith(RunOpts{}, args...)
}

// CLIIn runs `devyard args...` in dir.
func (sb *Sandbox) CLIIn(dir string, args ...string) Result {
	sb.t.Helper()
	return sb.CLIWith(RunOpts{Dir: dir}, args...)
}

// CLIWith runs `devyard args...` with options.
func (sb *Sandbox) CLIWith(o RunOpts, args ...string) Result {
	sb.t.Helper()
	return sb.run(o, sb.devyard(), args...)
}

// Exec runs an arbitrary program inside the sandbox environment.
func (sb *Sandbox) Exec(dir, name string, args ...string) Result {
	sb.t.Helper()
	return sb.run(RunOpts{Dir: dir}, name, args...)
}

// Git runs git in dir with the sandboxed git config and fails t on error.
func (sb *Sandbox) Git(dir string, args ...string) string {
	sb.t.Helper()
	r := sb.run(RunOpts{Dir: dir}, "git", args...)
	r.MustSucceed(sb.t)
	return strings.TrimSpace(r.Stdout)
}

func (sb *Sandbox) command(ctx context.Context, o RunOpts, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = o.Dir
	if cmd.Dir == "" {
		cmd.Dir = sb.Home
	}
	cmd.Env = append(sb.Env(), o.Env...)
	cmd.Stdin = o.Stdin
	// Own process group so a timeout kills the whole CLI tree, and so a
	// Ctrl-C in the developer's terminal doesn't race our cleanup.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (sb *Sandbox) run(o RunOpts, name string, args ...string) Result {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = DefaultCLITimeout
	}
	ctx, cancel := context.WithTimeout(sb.ctx, Scale(timeout))
	defer cancel()
	cmd := sb.command(ctx, o, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	r := Result{
		Args:     append([]string{name}, args...),
		Dir:      cmd.Dir,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		r.Code, r.Err, r.TimedOut = -1, ctx.Err(), true
	case err == nil:
	case errors.As(err, &ee):
		r.Code = ee.ExitCode()
		if r.Code < 0 {
			r.Err = err
		}
	default:
		r.Code, r.Err = -1, err
	}
	return r
}

// syncBuffer is a goroutine-safe growing buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Proc is a background command (e.g. `start -f`, `logs -f`).
type Proc struct {
	sb     *Sandbox
	cmd    *exec.Cmd
	stdout syncBuffer
	stderr syncBuffer
	done   chan struct{}
	result Result
	cancel context.CancelFunc
}

// CLIStart starts `devyard args...` in the background. It is killed at
// cleanup if still running.
func (sb *Sandbox) CLIStart(o RunOpts, args ...string) *Proc {
	sb.t.Helper()
	return sb.Start(o, sb.devyard(), args...)
}

// Start runs any program in the background inside the sandbox environment.
func (sb *Sandbox) Start(o RunOpts, name string, args ...string) *Proc {
	sb.t.Helper()
	ctx, cancel := context.WithCancel(sb.ctx)
	p := &Proc{sb: sb, done: make(chan struct{}), cancel: cancel}
	p.cmd = sb.command(ctx, o, name, args...)
	p.cmd.Stdout = &p.stdout
	p.cmd.Stderr = &p.stderr
	start := time.Now()
	if err := p.cmd.Start(); err != nil {
		cancel()
		sb.t.Fatalf("start %s: %v", name, err)
	}
	go func() {
		err := p.cmd.Wait()
		r := Result{Args: append([]string{name}, args...), Dir: p.cmd.Dir, Stdout: p.stdout.String(), Stderr: p.stderr.String(), Duration: time.Since(start)}
		var ee *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &ee):
			r.Code = ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				r.Code = 128 + int(ws.Signal())
			}
		default:
			r.Code, r.Err = -1, err
		}
		p.result = r
		close(p.done)
	}()
	sb.t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			<-p.done
		}
		cancel()
	})
	return p
}

// Pid of the background process.
func (p *Proc) Pid() int { return p.cmd.Process.Pid }

// Stdout captured so far.
func (p *Proc) Stdout() string { return p.stdout.String() }

// Stderr captured so far.
func (p *Proc) Stderr() string { return p.stderr.String() }

// Signal sends sig to the process (not its group).
func (p *Proc) Signal(sig syscall.Signal) { _ = p.cmd.Process.Signal(sig) }

// Exited reports whether the process has exited.
func (p *Proc) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// WaitOutput waits until stdout+stderr contains substr.
func (p *Proc) WaitOutput(t TB, substr string) {
	t.Helper()
	Eventually(t, fmt.Sprintf("output contains %q", substr), func(c *C) {
		out := p.Stdout() + p.Stderr()
		if !strings.Contains(out, substr) {
			c.Errorf("not yet; output so far:\n%s", clip(out, 4000))
		}
	})
}

// Wait waits for exit within d (scaled) and returns the result.
func (p *Proc) Wait(t TB, d time.Duration) Result {
	t.Helper()
	select {
	case <-p.done:
		return p.result
	case <-time.After(Scale(d)):
		t.Fatalf("process %v did not exit within %s\nstdout:\n%s\nstderr:\n%s", p.cmd.Args, Scale(d), clip(p.Stdout(), 4000), clip(p.Stderr(), 4000))
		return Result{}
	}
}

// Kill SIGKILLs the process group.
func (p *Proc) Kill() {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
}
