package harness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Pty is a CLI process attached to a pseudo-terminal, for interactive
// commands (`devyard run`, `devyard attach`).
type Pty struct {
	sb   *Sandbox
	cmd  *exec.Cmd
	f    *os.File
	out  *outputBuffer
	done chan struct{}
	code int
}

// CLIPty runs `devyard args...` under a pty (80x24) in the sandbox HOME.
func (sb *Sandbox) CLIPty(args ...string) *Pty {
	sb.t.Helper()
	return sb.CLIPtyIn(sb.Home, args...)
}

// CLIPtyIn runs `devyard args...` under a pty in dir.
func (sb *Sandbox) CLIPtyIn(dir string, args ...string) *Pty {
	sb.t.Helper()
	ctx, cancel := context.WithCancel(sb.ctx)
	cmd := exec.CommandContext(ctx, sb.devyard(), args...)
	cmd.Dir = dir
	cmd.Env = sb.Env()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		cancel()
		sb.t.Fatalf("start pty: %v", err)
	}
	p := &Pty{sb: sb, cmd: cmd, f: f, out: newOutputBuffer(), done: make(chan struct{})}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				_, _ = p.out.Write(buf[:n])
			}
			if err != nil {
				p.out.close()
				return
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		var ee *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &ee):
			p.code = ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				p.code = 128 + int(ws.Signal())
			}
		default:
			p.code = -1
		}
		close(p.done)
	}()
	sb.t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
			}
		}
		_ = f.Close()
		cancel()
	})
	return p
}

// Expect waits (DefaultWait, scaled) for substr in the output after the last
// match.
func (p *Pty) Expect(t TB, substr string) {
	t.Helper()
	p.ExpectWithin(t, DefaultWait, substr)
}

// ExpectWithin is Expect with an explicit (unscaled) timeout.
func (p *Pty) ExpectWithin(t TB, d time.Duration, substr string) {
	t.Helper()
	if err := p.out.expect(substr, Scale(d)); err != nil {
		t.Fatalf("pty %v: %v", p.cmd.Args, err)
	}
}

// Send writes s to the terminal (use "\r" for Enter).
func (p *Pty) Send(t TB, s string) {
	t.Helper()
	if _, err := p.f.Write([]byte(s)); err != nil {
		t.Fatalf("pty write: %v", err)
	}
}

// SendCtrlC types ^C (the line discipline turns it into SIGINT unless the
// CLI put the terminal in raw mode, in which case the CLI reads 0x03).
func (p *Pty) SendCtrlC(t TB) { t.Helper(); p.Send(t, "\x03") }

// Resize changes the terminal size (SIGWINCH to the foreground group).
func (p *Pty) Resize(t TB, cols, rows uint16) {
	t.Helper()
	if err := pty.Setsize(p.f, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		t.Fatalf("pty resize: %v", err)
	}
}

// Output returns everything read so far.
func (p *Pty) Output() string { return p.out.String() }

// Wait waits for the process to exit and returns its exit code.
func (p *Pty) Wait(t TB, d time.Duration) int {
	t.Helper()
	select {
	case <-p.done:
		return p.code
	case <-time.After(Scale(d)):
		t.Fatalf("pty %v did not exit within %s; output:\n%q", p.cmd.Args, Scale(d), clip(p.Output(), 4000))
		return -1
	}
}

// Exited reports whether the process has exited.
func (p *Pty) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Kill kills the CLI process.
func (p *Pty) Kill() { _ = p.cmd.Process.Kill() }
