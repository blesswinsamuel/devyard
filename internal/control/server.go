package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// Backend is the surface the control server needs from the supervisor. The
// interface keeps control decoupled from internal/supervisor so the same
// server can be driven by a fake in tests, and so CLI/web clients that only
// import the Client don't transitively pull the supervisor package.
type Backend interface {
	// States returns a snapshot of every service, in start order.
	States() []protocol.ServiceState
	// Stop gracefully stops every service. Used by `down`.
	Stop(ctx context.Context) error
	// StopService stops a single service in place (no restart), used by
	// `stop <service>`. markStopped semantics are owned by the implementation;
	// the supervisor treats it as an explicit stop so unless-stopped does not
	// auto-resume it.
	StopService(name string) error
	// KillService sends signal to a single service's process group without a
	// grace period. signal is a signal name (e.g. "SIGKILL", "SIGTERM"); empty
	// means SIGKILL.
	KillService(name, signal string) error
	// Restart stops and relaunches one service by name.
	Restart(name string) error
	// Top returns a CPU/memory snapshot for one service (or all when name is
	// empty), used by the `top` command. The implementation samples process
	// groups over a short interval and blocks for it.
	Top(name string) ([]protocol.ServiceStat, error)
	// LogPath returns the absolute path of a service's log file.
	LogPath(name string) (string, error)
	// PreviousLogPath returns the absolute path of a service's previous-run
	// log file (the run immediately before the current one).
	PreviousLogPath(name string) (string, error)
}

// Server is the Unix-socket control server. It accepts connections from
// CLI/web clients, dispatches each request to the MultiBackend, and
// streams responses back as length-prefixed JSON frames. Each connection serves
// a single request; streaming requests (Logs follow) hold the connection until
// the stream ends or the client disconnects.
type Server struct {
	socket  string
	backend MultiBackend

	mu     sync.Mutex
	ln     net.Listener
	closed bool

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewServer creates a control server that will listen on socket when
// ListenAndServe is called. The socket's parent directory must already exist
// (call project.Locations.MkdirAll first).
func NewServer(socket string, backend MultiBackend) *Server {
	return &Server{
		socket:  socket,
		backend: backend,
		stopCh:  make(chan struct{}),
	}
}

// ListenAndServe binds the Unix socket and starts accepting connections in a
// background goroutine. It returns once the listener is ready. Call Close to
// stop accepting and wait for in-flight handlers to finish.
func (s *Server) ListenAndServe() error {
	// A stale socket from a crashed previous run would block Listen.
	_ = os.Remove(s.socket)
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("control: listen %s: %w", s.socket, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	s.wg.Add(1)
	go s.acceptLoop(ln)
	return nil
}

// Addr returns the socket path the server is bound to, useful for tests.
func (s *Server) Addr() string { return s.socket }

// Close stops accepting new connections, signals streaming handlers to wind
// down, waits for in-flight handlers to return, and removes the socket file.
// It is safe to call multiple times.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()

	s.stopOnce.Do(func() { close(s.stopCh) })
	var err error
	if ln != nil {
		err = ln.Close()
	}
	s.wg.Wait()
	_ = os.Remove(s.socket)
	return err
}

func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			// Transient accept error: brief backoff then retry.
			time.Sleep(5 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.serveConn(conn)
	}
}

// serveConn handles one request on one connection, then closes it. Streaming
// requests (Logs follow) block here until the stream ends, the client
// disconnects (write error), or the server is closed (stopCh).
func (s *Server) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	var req protocol.Request
	if err := protocol.ReadFrame(conn, &req); err != nil {
		if !errors.Is(err, io.EOF) {
			_ = protocol.WriteFrame(conn, protocol.Response{
				Kind:  protocol.KindError,
				Error: err.Error(),
			})
		}
		return
	}
	s.dispatch(context.Background(), conn, req)
}

// dispatch routes a request to the backend and writes responses. Any error
// not already communicated to the client is sent as a KindError frame.
func (s *Server) dispatch(ctx context.Context, w io.Writer, req protocol.Request) {
	switch req.Kind {
	case protocol.KindList:
		s.handleList(w, req)
	case protocol.KindStop:
		s.handleStop(ctx, w, req)
	case protocol.KindStopService:
		s.handleStopService(w, req)
	case protocol.KindKillService:
		s.handleKillService(w, req)
	case protocol.KindRestart:
		s.handleRestart(w, req)
	case protocol.KindTop:
		s.handleTop(w, req)
	case protocol.KindLogs:
		s.handleLogs(w, req)
	case protocol.KindListProjects:
		s.handleListProjects(w)
	case protocol.KindStartProject:
		s.handleStartProject(w, req)
	case protocol.KindStopProject:
		s.handleStopProject(w, req)
	case protocol.KindRemoveProject:
		s.handleRemoveProject(w, req)
	case protocol.KindStopDaemon:
		s.handleStopDaemon(w)
	default:
		_ = writeError(w, fmt.Sprintf("unknown request kind %q", req.Kind))
	}
}

func (s *Server) handleList(w io.Writer, req protocol.Request) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	states := b.States()
	if err := protocol.WriteFrame(w, protocol.Response{
		Kind:   protocol.KindStates,
		States: states,
	}); err != nil {
		_ = err
	}
}

func (s *Server) handleStop(ctx context.Context, w io.Writer, req protocol.Request) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if err := b.Stop(ctx); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleStopService(w io.Writer, req protocol.Request) {
	if req.Service == "" {
		_ = writeError(w, "stop_service: service is required")
		return
	}
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if err := b.StopService(req.Service); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleKillService(w io.Writer, req protocol.Request) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if req.Service != "" {
		if err := b.KillService(req.Service, req.Signal); err != nil {
			_ = writeError(w, err.Error())
			return
		}
		_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
		return
	}
	// Kill all: iterate the current snapshot in start order.
	for _, st := range b.States() {
		if err := b.KillService(st.Name, req.Signal); err != nil {
			_ = writeError(w, fmt.Sprintf("kill %s: %v", st.Name, err))
			return
		}
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleRestart(w io.Writer, req protocol.Request) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if req.Service != "" {
		if err := b.Restart(req.Service); err != nil {
			_ = writeError(w, err.Error())
			return
		}
		_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
		return
	}
	// Restart all: iterate the current snapshot in start order.
	for _, st := range b.States() {
		if err := b.Restart(st.Name); err != nil {
			_ = writeError(w, fmt.Sprintf("restart %s: %v", st.Name, err))
			return
		}
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleTop(w io.Writer, req protocol.Request) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	stats, err := b.Top(req.Service)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if err := protocol.WriteFrame(w, protocol.Response{
		Kind:  protocol.KindStats,
		Stats: stats,
	}); err != nil {
		_ = err
	}
}

func (s *Server) handleLogs(w io.Writer, req protocol.Request) {
	if req.Service == "" {
		_ = writeError(w, "logs: service is required")
		return
	}
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	var path string
	if req.Previous {
		path, err = b.PreviousLogPath(req.Service)
	} else {
		path, err = b.LogPath(req.Service)
	}
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if req.Previous {
		if _, err := os.Stat(path); err != nil {
			_ = writeError(w, fmt.Sprintf("logs: no previous run for service %q", req.Service))
			return
		}
	}
	// Follow tails the live file, which rotates between spawns; the previous
	// log is a completed run, so rotation tracking is unnecessary for it.
	if err := streamLogs(w, path, req.Follow, !req.Previous, req.Tail, s.stopCh, req.Project, req.Service); err != nil {
		_ = writeError(w, err.Error())
	}
}

func (s *Server) handleListProjects(w io.Writer) {
	projects := s.backend.ListProjects()
	if err := protocol.WriteFrame(w, protocol.Response{
		Kind:     protocol.KindProjects,
		Projects: projects,
	}); err != nil {
		_ = err
	}
}

func (s *Server) handleStartProject(w io.Writer, req protocol.Request) {
	if req.ConfigPath == "" && req.Project != "" {
		for _, p := range s.backend.ListProjects() {
			if p.Name == req.Project {
				req.ConfigPath = p.ConfigPath
				break
			}
		}
	}
	if req.ConfigPath == "" {
		_ = writeError(w, "start_project: config_path is required")
		return
	}
	if err := s.backend.StartProject(req.ConfigPath, req.Build, req.EnvFile); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleStopProject(w io.Writer, req protocol.Request) {
	if req.Project == "" {
		_ = writeError(w, "stop_project: project is required")
		return
	}
	if err := s.backend.StopProject(req.Project); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleRemoveProject(w io.Writer, req protocol.Request) {
	if req.Project == "" {
		_ = writeError(w, "remove_project: project is required")
		return
	}
	if err := s.backend.RemoveProject(req.Project); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleStopDaemon(w io.Writer) {
	if err := s.backend.StopDaemon(); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

// writeError sends a KindError frame, swallowing the error (the connection is
// usually already broken by the time we get here).
func writeError(w io.Writer, msg string) error {
	return protocol.WriteFrame(w, protocol.Response{
		Kind:  protocol.KindError,
		Error: msg,
	})
}

// followPollInterval is how often the follow tailer checks for new log content.
const followPollInterval = 100 * time.Millisecond

// streamLogs reads existing content of path (honoring tail), emitting one
// KindLogContent frame, then (if follow) tails the file for new lines until
// the client disconnects (write error) or stop is closed. A final KindDone
// frame is sent when the stream ends cleanly. Rotated/truncated files are
// handled by reopening or rewinding to offset 0 when the file at path no
// longer matches the open handle (trackRotation) or the read offset is past
// the file size.
func streamLogs(w io.Writer, path string, follow, trackRotation bool, tail int, stop <-chan struct{}, project, service string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if err := tailFile(w, f, path, follow, trackRotation, tail, stop, project, service); err != nil {
		return err
	}
	return protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

// tailFile sends existing file content as a single bulk frame (last `tail`
// lines when tail > 0, else all subject to MaxLogContentBytes) and optionally
// keeps tailing for appended content. During the follow phase, incomplete
// trailing bytes (no newline) are withheld until a newline arrives, so each
// frame is a complete line.
func tailFile(w io.Writer, f *os.File, path string, follow, trackRotation bool, tail int, stop <-chan struct{}, project, service string) error {
	// Close the handle the loop ends up on (possibly a reopened one) so a
	// rotated file isn't leaked when the stream finishes.
	defer func() { _ = f.Close() }()

	// Send history in one frame so the client can render it instantly without
	// a line-by-line scroll animation.
	content, err := ReadLogHistory(f, tail)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(w, protocol.Response{
		Kind:    protocol.KindLogContent,
		Project: project,
		Service: service,
		Content: string(content),
	}); err != nil {
		return err
	}

	if !follow {
		return nil
	}

	// Follow: poll for new content until stop or the writer errors.
	var leftover []byte
	buf := make([]byte, 4096)

	flush := func(chunk []byte) error {
		data := append(leftover, chunk...)
		leftover = leftover[:0]
		for {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				leftover = append(leftover, data...)
				return nil
			}
			line := string(data[:i])
			data = data[i+1:]
			if err := protocol.WriteFrame(w, protocol.Response{
				Kind:    protocol.KindLogLine,
				Project: project,
				Service: service,
				Line:    line,
			}); err != nil {
				return err
			}
		}
	}

	ticker := time.NewTicker(followPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			if trackRotation {
				nf, err := ReopenIfRotated(f, path)
				if err != nil {
					return err
				}
				if nf != f {
					f = nf
					leftover = leftover[:0]
					// The previous run's log ended and a fresh one began; tell
					// frontends with a scrollback buffer to reset their view so
					// they show only the new run.
					if err := protocol.WriteFrame(w, protocol.Response{
						Kind:    protocol.KindLogRotated,
						Project: project,
						Service: service,
					}); err != nil {
						return err
					}
				}
			}
			if rotated, err := rewindIfRotated(f); err != nil {
				return err
			} else if rotated {
				leftover = leftover[:0]
			}
			for {
				n, err := f.Read(buf)
				if n > 0 {
					if e := flush(buf[:n]); e != nil {
						return e
					}
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
		}
	}
}

// ReopenIfRotated reports whether the file at path is no longer the same file
// as f (e.g. the supervisor rotated <name>.log to <name>.prev.log and created
// a fresh <name>.log for the next run) and, if so, returns a handle to the
// new file positioned at the start. Otherwise it returns f unchanged. Shared
// with the web UI's log tailer, which faces the same rotation.
func ReopenIfRotated(f *os.File, path string) (*os.File, error) {
	cur, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, err
	}
	held, err := f.Stat()
	if err != nil {
		return f, err
	}
	if os.SameFile(cur, held) {
		return f, nil
	}
	nf, err := os.Open(path)
	if err != nil {
		return f, err
	}
	_ = f.Close()
	return nf, nil
}

// rewindIfRotated reports whether the file has been truncated/rotated
// (current read offset past end-of-file) and, if so, seeks back to offset 0.
func rewindIfRotated(f *os.File) (bool, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if off > info.Size() {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
