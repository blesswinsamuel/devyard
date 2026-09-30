//go:build unix

package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LaunchOptions controls how the runner executable is started.
type LaunchOptions struct {
	// Exe is the runner executable; defaults to os.Executable().
	Exe string
	// Args are passed before nothing else; defaults to []string{Flag}.
	Args []string
	// Env is the runner process's own environment (not the child's);
	// defaults to os.Environ().
	Env []string
}

// Launch starts a runner for spec and waits until its control socket is
// ready. The returned Process can be used to observe and control the run.
func Launch(ctx context.Context, spec Spec, opts LaunchOptions) (*Process, error) {
	exe := opts.Exe
	if exe == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("runner: resolve executable: %w", err)
		}
		exe = self
	}
	args := opts.Args
	if args == nil {
		args = []string{Flag}
	}
	if err := os.MkdirAll(spec.ProcDir, 0o755); err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.Socket), 0o700); err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	logPath := filepath.Join(spec.ProcDir, "runner.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("runner: open runner log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = "/"
	cmd.Env = opts.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Stdin = strings.NewReader(string(specJSON) + "\n")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// A new session: the runner must not receive signals aimed at the
	// daemon's process group or terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Remove any stale status so readiness below reflects this run.
	_ = os.Remove(StatusPath(spec.ProcDir))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: start: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		// Reap the runner when it exits while we are still its parent.
		_ = cmd.Wait()
		close(exited)
	}()

	p := &Process{ProcDir: spec.ProcDir, Socket: spec.Socket}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if st, err := p.Status(ctx); err == nil && st.Run == spec.Run {
			return p, nil
		}
		select {
		case <-exited:
			if st, err := ReadStatus(spec.ProcDir); err == nil && st.Run == spec.Run && st.Exited() {
				// The run finished before we could observe it.
				return p, nil
			}
			return nil, fmt.Errorf("runner: exited during startup: %s", tailFile(logPath))
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("runner: timed out waiting for control socket: %s", tailFile(logPath))
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func tailFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "no runner log"
	}
	s := strings.TrimSpace(string(data))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "no runner output"
	}
	return s
}

// Process is a handle on one runner, identified by its process directory
// and socket. It is valid across daemon restarts: Open rebuilds it from the
// status file.
type Process struct {
	ProcDir string
	Socket  string
}

// Open returns a handle for the run recorded in procDir, if any.
func Open(procDir string) (*Process, Status, error) {
	st, err := ReadStatus(procDir)
	if err != nil {
		return nil, Status{}, err
	}
	return &Process{ProcDir: procDir, Socket: st.Socket}, st, nil
}

// ErrRunnerGone is returned when the runner's socket is unreachable.
var ErrRunnerGone = errors.New("runner: not reachable")

func (p *Process) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", p.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunnerGone, err)
	}
	return conn, nil
}

func (p *Process) call(ctx context.Context, req request) (response, error) {
	conn, err := p.dial(ctx)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if err := writeJSONLine(conn, req); err != nil {
		return response{}, fmt.Errorf("%w: %v", ErrRunnerGone, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, fmt.Errorf("%w: %v", ErrRunnerGone, err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return response{}, fmt.Errorf("runner: bad response: %w", err)
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Status returns the live status from the runner.
func (p *Process) Status(ctx context.Context) (Status, error) {
	resp, err := p.call(ctx, request{Op: "status"})
	if err != nil {
		return Status{}, err
	}
	return *resp.Status, nil
}

// Signal sends sig to the run's process group.
func (p *Process) Signal(ctx context.Context, sig string) error {
	_, err := p.call(ctx, request{Op: "signal", Signal: sig})
	return err
}

// Stop asks the runner to stop the run (SIGTERM, SIGKILL after grace) and
// waits until it has exited.
func (p *Process) Stop(ctx context.Context, grace time.Duration) (Status, error) {
	resp, err := p.call(ctx, request{Op: "stop", GraceMS: grace.Milliseconds()})
	if err != nil {
		if errors.Is(err, ErrRunnerGone) {
			return p.settle()
		}
		return Status{}, err
	}
	return *resp.Status, nil
}

// Wait blocks until the run exits and returns its final status. It survives
// runner restarts of the socket connection and detects runners that died
// without recording a status (Lost).
func (p *Process) Wait(ctx context.Context) (Status, error) {
	for {
		resp, err := p.call(ctx, request{Op: "wait"})
		if err == nil {
			return *resp.Status, nil
		}
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
		if !errors.Is(err, ErrRunnerGone) {
			return Status{}, err
		}
		st, serr := p.settle()
		if serr == nil {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Watch reports every status change of the run to fn (starting with the
// current status) and returns the final status once the run has exited. Like
// Wait it survives connection loss and detects lost runners.
func (p *Process) Watch(ctx context.Context, fn func(Status)) (Status, error) {
	for {
		final, err := p.watchOnce(ctx, fn)
		if err == nil {
			return final, nil
		}
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
		if !errors.Is(err, ErrRunnerGone) {
			return Status{}, err
		}
		if st, serr := p.settle(); serr == nil {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (p *Process) watchOnce(ctx context.Context, fn func(Status)) (Status, error) {
	conn, err := p.dial(ctx)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if err := writeJSONLine(conn, request{Op: "watch"}); err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrRunnerGone, err)
	}
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return Status{}, ctx.Err()
			}
			return Status{}, fmt.Errorf("%w: %v", ErrRunnerGone, err)
		}
		var resp response
		if err := json.Unmarshal(line, &resp); err != nil || resp.Status == nil {
			return Status{}, fmt.Errorf("runner: bad watch response")
		}
		fn(*resp.Status)
		if resp.Status.Exited() {
			return *resp.Status, nil
		}
	}
}

// settle resolves the final status when the runner socket is unreachable:
// either the runner recorded an exit, or it died and the run is lost. In the
// lost case any surviving members of the process group are killed.
func (p *Process) settle() (Status, error) {
	st, err := ReadStatus(p.ProcDir)
	if err != nil {
		return Status{}, err
	}
	if st.Exited() {
		return st, nil
	}
	if Alive(st.RunnerPID) {
		return Status{}, errors.New("runner alive but unreachable")
	}
	if st.PGID > 0 {
		_ = syscall.Kill(-st.PGID, syscall.SIGKILL)
	}
	st.Phase = PhaseExited
	st.Lost = true
	st.ExitCode = -1
	st.PID = 0
	st.PGID = 0
	st.FinishedAt = time.Now()
	return st, nil
}

// Attachment is an interactive connection to a run's input and output.
type Attachment struct {
	conn net.Conn
	br   *bufio.Reader
	// TTY reports whether input is raw terminal input.
	TTY bool
	// Stdin reports whether the process accepts input at all.
	Stdin bool

	wmu sync.Mutex
}

// Attach opens an interactive attachment. Output (starting with a replay of
// recent output) is delivered by Read; the final status arrives as an
// ExitError from Read.
func (p *Process) Attach(ctx context.Context, cols, rows int) (*Attachment, error) {
	conn, err := p.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeJSONLine(conn, request{Op: "attach", Cols: cols, Rows: rows}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrRunnerGone, err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil || !resp.OK {
		_ = conn.Close()
		if err == nil {
			err = errors.New(resp.Error)
		}
		return nil, err
	}
	return &Attachment{conn: conn, br: br, TTY: resp.TTY, Stdin: resp.Stdin}, nil
}

// ExitError is returned by Attachment.Read once the run has exited.
type ExitError struct{ Status Status }

func (e *ExitError) Error() string { return fmt.Sprintf("run exited with code %d", e.Status.ExitCode) }

// Read returns the next output chunk.
func (a *Attachment) Read() ([]byte, error) {
	for {
		typ, payload, err := readFrame(a.br)
		if err != nil {
			return nil, err
		}
		switch typ {
		case frameOutput:
			return payload, nil
		case frameExit:
			var st Status
			_ = json.Unmarshal(payload, &st)
			return nil, &ExitError{Status: st}
		}
	}
}

func (a *Attachment) send(typ byte, payload []byte) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return writeFrame(a.conn, typ, payload)
}

// Write sends input bytes.
func (a *Attachment) Write(p []byte) (int, error) {
	if err := a.send(frameInput, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize changes the terminal size (TTY runs only).
func (a *Attachment) Resize(cols, rows int) error {
	return a.send(frameResize, encodeResize(cols, rows))
}

// CloseStdin closes the process's stdin (non-TTY runs).
func (a *Attachment) CloseStdin() error { return a.send(frameEOF, nil) }

// Close detaches; the run continues.
func (a *Attachment) Close() error { return a.conn.Close() }
