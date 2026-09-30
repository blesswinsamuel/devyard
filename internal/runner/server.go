//go:build unix

package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/blesswinsamuel/devyard/internal/logstore"
)

const (
	defaultStopGrace = 10 * time.Second
	// drainTimeout bounds how long output is drained after the child exits.
	// A grandchild that escaped the process group can hold the pipes open
	// forever; it must not keep the run from finishing.
	drainTimeout = 500 * time.Millisecond
	// replayBytes is the size of the output ring replayed to new attachers.
	replayBytes = 256 << 10
	// lingerTimeout bounds how long the runner waits to deliver the final
	// status to connected waiters before exiting.
	lingerTimeout = 2 * time.Second
)

// Main is the entry point of a runner process. It reads the Spec from stdin
// and supervises the run until it exits.
func Main() error {
	var spec Spec
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		return fmt.Errorf("runner: read spec: %w", err)
	}
	if devnull, err := os.Open(os.DevNull); err == nil {
		_ = unix.Dup2(int(devnull.Fd()), 0)
		_ = devnull.Close()
	}
	_ = os.Chdir("/")
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)

	s, err := newServer(spec)
	if err != nil {
		return err
	}
	return s.run()
}

type server struct {
	spec Spec
	log  *logstore.Writer
	ln   net.Listener

	mu           sync.Mutex
	status       Status
	pgid         int // process group currently running (build or main); 0 when none
	stopping     bool
	stopTimer    *time.Timer
	stopDeadline time.Time
	stdin        io.WriteCloser // non-TTY stdin pipe (nil when not interactive)
	ptmx         *os.File       // TTY master
	clients      map[*attachClient]struct{}
	ring         []byte
	// pendingInput holds input received before the child's stdin exists.
	pendingInput []byte

	done    chan struct{} // closed once the final status is recorded
	changed chan struct{} // closed and replaced on every status change
	waiters sync.WaitGroup
}

func newServer(spec Spec) (*server, error) {
	if spec.StopGrace <= 0 {
		spec.StopGrace = defaultStopGrace
	}
	if spec.Shell == "" {
		spec.Shell = "sh"
	}
	if err := os.MkdirAll(spec.ProcDir, 0o755); err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	w := logstore.Discard()
	if !spec.Ephemeral {
		var err error
		if w, err = logstore.Create(spec.ProcDir, spec.Run); err != nil {
			return nil, err
		}
	}
	_ = os.Remove(spec.Socket)
	ln, err := net.Listen("unix", spec.Socket)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("runner: listen: %w", err)
	}
	_ = os.Chmod(spec.Socket, 0o600)
	s := &server{
		spec:    spec,
		log:     w,
		ln:      ln,
		clients: make(map[*attachClient]struct{}),
		done:    make(chan struct{}),
		changed: make(chan struct{}),
		status: Status{
			Project:   spec.Project,
			Kind:      spec.Kind,
			Name:      spec.Name,
			Run:       spec.Run,
			RunnerPID: os.Getpid(),
			Socket:    spec.Socket,
			TTY:       spec.TTY,
			Hash:      spec.Hash,
			Phase:     PhaseStarting,
			StartedAt: time.Now(),
		},
	}
	if err := writeStatus(spec.ProcDir, s.status); err != nil {
		_ = ln.Close()
		_ = w.Close()
		return nil, fmt.Errorf("runner: write status: %w", err)
	}
	return s, nil
}

func (s *server) run() error {
	go s.acceptLoop()

	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for range term {
			s.requestStop(s.spec.StopGrace)
		}
	}()

	s.execute()

	// Deliver the final status to waiters, then go away.
	lingered := make(chan struct{})
	go func() { s.waiters.Wait(); close(lingered) }()
	select {
	case <-lingered:
	case <-time.After(lingerTimeout):
	}
	_ = s.ln.Close()
	_ = os.Remove(s.spec.Socket)
	_ = s.log.Close()
	return nil
}

// execute runs the optional build and the main command, then records the
// final status.
func (s *server) execute() {
	if b := s.spec.Build; b != nil {
		s.setPhase(PhaseBuilding)
		s.log.Systemf("$ %s  (build)", b.Command)
		code, sig, err := s.runBuild(b)
		switch {
		case s.isStopping():
			s.finish(code, sig, false, true, "")
			return
		case err != nil:
			s.log.Systemf("build could not start: %v", err)
			s.finish(1, "", true, false, err.Error())
			return
		case code != 0:
			s.log.Systemf("build failed with exit code %d", code)
			s.finish(code, sig, true, false, "")
			return
		}
		s.log.Systemf("build finished")
	}
	if s.isStopping() {
		s.finish(0, "", false, true, "")
		return
	}
	code, sig, err := s.runMain()
	if err != nil {
		s.log.Systemf("failed to start: %v", err)
		s.finish(127, "", false, false, err.Error())
		return
	}
	s.finish(code, sig, false, s.isStopping(), "")
}

// notifyLocked wakes status watchers. Callers hold s.mu.
func (s *server) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *server) isStopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping
}

func (s *server) setPhase(phase string) {
	s.mu.Lock()
	s.status.Phase = phase
	st := s.status
	s.mu.Unlock()
	_ = writeStatus(s.spec.ProcDir, st)
	s.mu.Lock()
	s.notifyLocked()
	s.mu.Unlock()
}

// started records a running process group. If a stop was requested before
// the group existed, it is signalled right away.
func (s *server) started(pid int, phase string) {
	s.mu.Lock()
	s.pgid = pid
	s.status.PID = pid
	s.status.PGID = pid
	s.status.Phase = phase
	stopping := s.stopping
	st := s.status
	s.mu.Unlock()
	_ = writeStatus(s.spec.ProcDir, st)
	s.mu.Lock()
	s.notifyLocked()
	s.mu.Unlock()
	if stopping {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
}

// ended runs once the group leader has been reaped. Other members of the
// group (children of a shell wrapper, background jobs) must not outlive the
// run — they would hold ports and pipes — but they deserve the same graceful
// treatment as the leader: they get SIGTERM (unless a stop already sent it)
// and SIGKILL only at the stop deadline or after the grace period. The run
// finishes when the group is empty.
func (s *server) ended() {
	s.mu.Lock()
	pgid := s.pgid
	s.status.PID = 0
	stopping := s.stopping
	deadline := s.stopDeadline
	s.mu.Unlock()
	if pgid > 0 && groupAlive(pgid) {
		if !stopping {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			deadline = time.Now().Add(s.spec.StopGrace)
		}
		for groupAlive(pgid) {
			if !deadline.IsZero() && time.Now().After(deadline) {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				// Bounded: members may linger as zombies until reaped
				// by their new parent.
				killDeadline := time.Now().Add(2 * time.Second)
				for groupAlive(pgid) && time.Now().Before(killDeadline) {
					time.Sleep(20 * time.Millisecond)
				}
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	s.mu.Lock()
	s.pgid = 0
	s.mu.Unlock()
}

// groupAlive reports whether any process is left in the group.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (s *server) runBuild(b *BuildSpec) (int, string, error) {
	shell := b.Shell
	if shell == "" {
		shell = "sh"
	}
	cmd := exec.Command(shell, "-c", b.Command)
	cmd.Dir = b.Dir
	cmd.Env = b.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outR, outW, err := os.Pipe()
	if err != nil {
		return 0, "", err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return 0, "", err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
		return 0, "", err
	}
	_ = outW.Close()
	_ = errW.Close()
	s.started(cmd.Process.Pid, PhaseBuilding)
	pumps := s.startPumps([]*os.File{outR, errR}, []logstore.Stream{logstore.Stdout, logstore.Stderr})
	waitErr := cmd.Wait()
	s.ended()
	s.drain(pumps, outR, errR)
	code, sig := exitStatus(cmd.ProcessState, waitErr)
	return code, sig, nil
}

func (s *server) runMain() (int, string, error) {
	cmd := exec.Command(s.spec.Shell, "-c", s.spec.Command)
	cmd.Dir = s.spec.Dir
	env := s.spec.Env
	if s.spec.TTY && !hasEnv(env, "TERM") {
		env = append(env, "TERM=xterm-256color")
	}
	cmd.Env = env
	s.log.Systemf("$ %s", s.spec.Command)

	if s.spec.TTY {
		cols, rows := s.spec.Cols, s.spec.Rows
		if cols <= 0 || rows <= 0 {
			cols, rows = 120, 40
		}
		// pty.StartWithSize makes the child a session leader with the pty
		// as its controlling terminal. Setpgid must NOT be combined with
		// Setsid (setpgid on a session leader fails with EPERM).
		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		if err != nil {
			return 0, "", err
		}
		s.mu.Lock()
		s.ptmx = ptmx
		s.flushPendingLocked(ptmx)
		s.mu.Unlock()
		s.started(cmd.Process.Pid, PhaseRunning)
		pumps := s.startPumps([]*os.File{ptmx}, []logstore.Stream{logstore.Stdout})
		waitErr := cmd.Wait()
		s.ended()
		s.drain(pumps, ptmx)
		s.mu.Lock()
		s.ptmx = nil
		s.mu.Unlock()
		code, sig := exitStatus(cmd.ProcessState, waitErr)
		return code, sig, nil
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdinR, stdinW *os.File
	if s.spec.Kind != "service" {
		r, w, err := os.Pipe()
		if err != nil {
			return 0, "", err
		}
		stdinR, stdinW = r, w
		cmd.Stdin = stdinR
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return 0, "", err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		return 0, "", err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{outR, outW, errR, errW, stdinR, stdinW} {
			if f != nil {
				_ = f.Close()
			}
		}
		return 0, "", err
	}
	_ = outW.Close()
	_ = errW.Close()
	if stdinR != nil {
		_ = stdinR.Close()
		s.mu.Lock()
		s.stdin = stdinW
		s.flushPendingLocked(stdinW)
		s.mu.Unlock()
	}
	s.started(cmd.Process.Pid, PhaseRunning)
	pumps := s.startPumps([]*os.File{outR, errR}, []logstore.Stream{logstore.Stdout, logstore.Stderr})
	waitErr := cmd.Wait()
	s.ended()
	s.drain(pumps, outR, errR)
	s.mu.Lock()
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	s.mu.Unlock()
	code, sig := exitStatus(cmd.ProcessState, waitErr)
	return code, sig, nil
}

func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if len(kv) > len(key) && kv[:len(key)] == key && kv[len(key)] == '=' {
			return true
		}
	}
	return false
}

func exitStatus(ps *os.ProcessState, err error) (int, string) {
	if ps == nil {
		if err != nil {
			return 1, ""
		}
		return 0, ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), SignalName(ws.Signal())
	}
	return ps.ExitCode(), ""
}

// startPumps copies each file to the log and attached clients.
func (s *server) startPumps(files []*os.File, streams []logstore.Stream) chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Add(1)
		lw := logstore.NewLineWriter(s.log, streams[i])
		go func(f *os.File) {
			defer wg.Done()
			buf := make([]byte, 32<<10)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					chunk := buf[:n]
					_, _ = lw.Write(chunk)
					s.broadcast(chunk)
				}
				if err != nil {
					lw.Flush()
					return
				}
			}
		}(f)
	}
	go func() { wg.Wait(); close(done) }()
	return done
}

// drain waits a bounded time for the pumps to hit EOF, then closes the read
// ends to unblock them.
func (s *server) drain(pumps chan struct{}, files ...*os.File) {
	select {
	case <-pumps:
	case <-time.After(drainTimeout):
		for _, f := range files {
			_ = f.Close()
		}
		<-pumps
	}
	for _, f := range files {
		_ = f.Close()
	}
}

func (s *server) finish(code int, sig string, buildFailed, stopped bool, errMsg string) {
	s.mu.Lock()
	s.status.Phase = PhaseExited
	s.status.PID = 0
	s.status.PGID = 0
	s.status.ExitCode = code
	s.status.Signal = sig
	s.status.BuildFailed = buildFailed
	s.status.Stopped = stopped
	s.status.Error = errMsg
	s.status.FinishedAt = time.Now()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	st := s.status
	clients := make([]*attachClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	switch {
	case stopped:
		s.log.Systemf("stopped (exit code %d)", code)
	case sig != "":
		s.log.Systemf("killed by %s", sig)
	case !buildFailed && errMsg == "":
		s.log.Systemf("exited with code %d", code)
	}
	_ = writeStatus(s.spec.ProcDir, st)
	s.mu.Lock()
	close(s.done)
	s.notifyLocked()
	s.mu.Unlock()
	for _, c := range clients {
		c.sendExit(st)
	}
}

func (s *server) snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// requestStop sends SIGTERM to the running group and SIGKILL after grace.
// Safe to call repeatedly and at any phase.
func (s *server) requestStop(grace time.Duration) {
	if grace <= 0 {
		grace = s.spec.StopGrace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Phase == PhaseExited {
		return
	}
	first := !s.stopping
	s.stopping = true
	if s.pgid > 0 {
		_ = syscall.Kill(-s.pgid, syscall.SIGTERM)
	}
	if first {
		s.stopDeadline = time.Now().Add(grace)
		s.stopTimer = time.AfterFunc(grace, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.pgid > 0 && s.status.Phase != PhaseExited {
				_ = syscall.Kill(-s.pgid, syscall.SIGKILL)
			}
		})
	}
}

func (s *server) signal(sig syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pgid <= 0 {
		return errors.New("process is not running")
	}
	err := syscall.Kill(-s.pgid, sig)
	// macOS reports EPERM for a group that contains an unreaped zombie even
	// though the live members were signalled.
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// --- control socket --------------------------------------------------------

func (s *server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeJSONLine(conn, response{Error: "bad request"})
		_ = conn.Close()
		return
	}
	switch req.Op {
	case "status":
		st := s.snapshot()
		_ = writeJSONLine(conn, response{OK: true, Status: &st})
		_ = conn.Close()
	case "wait":
		s.waiters.Add(1)
		go func() {
			defer s.waiters.Done()
			defer func() { _ = conn.Close() }()
			// Detect a client hanging up while we wait.
			gone := make(chan struct{})
			go func() { _, _ = io.Copy(io.Discard, br); close(gone) }()
			select {
			case <-s.done:
				st := s.snapshot()
				_ = writeJSONLine(conn, response{OK: true, Status: &st})
			case <-gone:
			}
		}()
	case "watch":
		s.waiters.Add(1)
		go func() {
			defer s.waiters.Done()
			defer func() { _ = conn.Close() }()
			gone := make(chan struct{})
			go func() { _, _ = io.Copy(io.Discard, br); close(gone) }()
			var last Status
			first := true
			for {
				s.mu.Lock()
				st := s.status
				changed := s.changed
				s.mu.Unlock()
				if first || st != last {
					if err := writeJSONLine(conn, response{OK: true, Status: &st}); err != nil {
						return
					}
					first = false
					last = st
				}
				if st.Phase == PhaseExited {
					return
				}
				select {
				case <-changed:
				case <-gone:
					return
				}
			}
		}()
	case "stop":
		s.requestStop(time.Duration(req.GraceMS) * time.Millisecond)
		s.waiters.Add(1)
		go func() {
			defer s.waiters.Done()
			defer func() { _ = conn.Close() }()
			<-s.done
			st := s.snapshot()
			_ = writeJSONLine(conn, response{OK: true, Status: &st})
		}()
	case "signal":
		sig, err := ParseSignal(req.Signal)
		if err == nil {
			err = s.signal(sig)
		}
		resp := response{OK: err == nil}
		if err != nil {
			resp.Error = err.Error()
		}
		_ = writeJSONLine(conn, resp)
		_ = conn.Close()
	case "attach":
		s.attach(conn, br, req)
	default:
		_ = writeJSONLine(conn, response{Error: "unknown op " + req.Op})
		_ = conn.Close()
	}
}

// --- attach ----------------------------------------------------------------

type attachClient struct {
	conn net.Conn
	out  chan []byte
	exit chan Status
	once sync.Once
	gone chan struct{}
}

func (c *attachClient) sendExit(st Status) {
	select {
	case c.exit <- st:
	default:
	}
}

func (c *attachClient) close() {
	c.once.Do(func() {
		close(c.gone)
		_ = c.conn.Close()
	})
}

func (s *server) broadcast(chunk []byte) {
	cp := append([]byte(nil), chunk...)
	s.mu.Lock()
	s.ring = append(s.ring, cp...)
	if len(s.ring) > replayBytes {
		s.ring = append([]byte(nil), s.ring[len(s.ring)-replayBytes:]...)
	}
	for c := range s.clients {
		select {
		case c.out <- cp:
		default:
			// Slow client: drop output for it rather than blocking the
			// child. The log file stays complete.
		}
	}
	s.mu.Unlock()
}

func (s *server) attach(conn net.Conn, br *bufio.Reader, req request) {
	c := &attachClient{
		conn: conn,
		out:  make(chan []byte, 512),
		exit: make(chan Status, 1),
		gone: make(chan struct{}),
	}
	s.mu.Lock()
	tty := s.spec.TTY
	acceptsInput := tty || s.spec.Kind != "service"
	replay := append([]byte(nil), s.ring...)
	exited := s.status.Phase == PhaseExited
	st := s.status
	if !exited {
		s.clients[c] = struct{}{}
	}
	s.mu.Unlock()
	if tty && req.Cols > 0 && req.Rows > 0 {
		s.resize(req.Cols, req.Rows)
	}

	if err := writeJSONLine(conn, response{OK: true, TTY: tty, Stdin: acceptsInput, Status: &st}); err != nil {
		s.detach(c)
		return
	}
	if len(replay) > 0 {
		_ = writeFrame(conn, frameOutput, replay)
	}
	if exited {
		data, _ := json.Marshal(st)
		_ = writeFrame(conn, frameExit, data)
		_ = conn.Close()
		return
	}

	// Writer: output and exit frames. It counts as a waiter so the runner
	// does not exit before the final exit frame is delivered.
	s.waiters.Add(1)
	go func() {
		defer s.waiters.Done()
		defer s.detach(c)
		for {
			select {
			case chunk := <-c.out:
				if err := writeFrame(conn, frameOutput, chunk); err != nil {
					return
				}
			case st := <-c.exit:
				// Flush output queued before the exit.
				for {
					select {
					case chunk := <-c.out:
						_ = writeFrame(conn, frameOutput, chunk)
						continue
					default:
					}
					break
				}
				data, _ := json.Marshal(st)
				_ = writeFrame(conn, frameExit, data)
				return
			case <-c.gone:
				return
			}
		}
	}()
	// Reader: input, resize, EOF.
	go func() {
		defer s.detach(c)
		for {
			typ, payload, err := readFrame(br)
			if err != nil {
				return
			}
			switch typ {
			case frameInput:
				s.input(payload)
			case frameResize:
				if cols, rows, err := decodeResize(payload); err == nil {
					s.resize(cols, rows)
				}
			case frameEOF:
				s.closeStdin()
			}
		}
	}()
}

func (s *server) detach(c *attachClient) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
	c.close()
}

func (s *server) input(p []byte) {
	s.mu.Lock()
	var w io.Writer
	switch {
	case s.ptmx != nil:
		w = s.ptmx
	case s.stdin != nil:
		w = s.stdin
	case s.status.Phase != PhaseExited && len(s.pendingInput) < 64<<10:
		// The child has not started yet (building/starting): keep the
		// input until its stdin exists.
		s.pendingInput = append(s.pendingInput, p...)
	}
	s.mu.Unlock()
	if w != nil {
		_, _ = w.Write(p)
	}
}

func (s *server) flushPendingLocked(w io.Writer) {
	if len(s.pendingInput) > 0 {
		_, _ = w.Write(s.pendingInput)
		s.pendingInput = nil
	}
}

func (s *server) closeStdin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
}

func (s *server) resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptmx != nil && cols > 0 && rows > 0 {
		_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}
