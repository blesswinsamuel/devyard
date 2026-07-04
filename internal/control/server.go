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
// server can be driven by a fake in tests, and so CLI/TUI/web clients that
// only import the Client don't transitively pull the supervisor package.
type Backend interface {
	// States returns a snapshot of every service, in start order.
	States() []protocol.ServiceState
	// Stop gracefully stops every service. Used by `down`.
	Stop(ctx context.Context) error
	// Restart stops and relaunches one service by name.
	Restart(name string) error
	// LogPath returns the absolute path of a service's log file.
	LogPath(name string) (string, error)
}

// Server is the Unix-socket control server. It accepts connections from
// CLI/TUI/web clients, dispatches each request to the Backend, and streams
// responses back as length-prefixed JSON frames. Each connection serves a
// single request; streaming requests (Logs follow) hold the connection until
// the stream ends or the client disconnects.
type Server struct {
	socket  string
	backend Backend

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
func NewServer(socket string, backend Backend) *Server {
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
		s.handleList(w)
	case protocol.KindStop:
		s.handleStop(ctx, w)
	case protocol.KindRestart:
		s.handleRestart(w, req)
	case protocol.KindLogs:
		s.handleLogs(w, req)
	default:
		_ = writeError(w, fmt.Sprintf("unknown request kind %q", req.Kind))
	}
}

func (s *Server) handleList(w io.Writer) {
	states := s.backend.States()
	if err := protocol.WriteFrame(w, protocol.Response{
		Kind:   protocol.KindStates,
		States: states,
	}); err != nil {
		// Connection dropped mid-write; nothing more to do.
		_ = err
	}
}

func (s *Server) handleStop(ctx context.Context, w io.Writer) {
	if err := s.backend.Stop(ctx); err != nil {
		_ = writeError(w, err.Error())
		return
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleRestart(w io.Writer, req protocol.Request) {
	if req.Service != "" {
		if err := s.backend.Restart(req.Service); err != nil {
			_ = writeError(w, err.Error())
			return
		}
		_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
		return
	}
	// Restart all: iterate the current snapshot in start order.
	for _, st := range s.backend.States() {
		if err := s.backend.Restart(st.Name); err != nil {
			_ = writeError(w, fmt.Sprintf("restart %s: %v", st.Name, err))
			return
		}
	}
	_ = protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

func (s *Server) handleLogs(w io.Writer, req protocol.Request) {
	if req.Service == "" {
		_ = writeError(w, "logs: service is required")
		return
	}
	path, err := s.backend.LogPath(req.Service)
	if err != nil {
		_ = writeError(w, err.Error())
		return
	}
	if err := streamLogs(w, path, req.Follow, s.stopCh); err != nil {
		_ = writeError(w, err.Error())
	}
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

// streamLogs reads the existing content of path, emitting one KindLogLine
// frame per line, then (if follow) tails the file for new lines until the
// client disconnects (write error) or stop is closed. A final KindDone frame
// is sent when the stream ends cleanly. Truncated/rotated files are handled
// by rewinding to offset 0 when the read offset is past the file size.
func streamLogs(w io.Writer, path string, follow bool, stop <-chan struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if err := tailFile(w, f, follow, stop); err != nil {
		return err
	}
	return protocol.WriteFrame(w, protocol.Response{Kind: protocol.KindDone})
}

// tailFile writes existing file content as log-line frames and optionally
// keeps tailing for appended content. Incomplete trailing bytes (no newline)
// are withheld until a newline arrives, so each frame is a complete line.
func tailFile(w io.Writer, f *os.File, follow bool, stop <-chan struct{}) error {
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
				Kind: protocol.KindLogLine,
				Line: line,
			}); err != nil {
				return err
			}
		}
	}

	// Drain current content.
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

	if !follow {
		// Emit a trailing partial line (no newline) so non-follow `logs`
		// doesn't silently drop the last line a service wrote.
		if len(leftover) > 0 {
			if err := protocol.WriteFrame(w, protocol.Response{
				Kind: protocol.KindLogLine,
				Line: string(leftover),
			}); err != nil {
				return err
			}
		}
		return nil
	}

	// Follow: poll for new content until stop or the writer errors.
	ticker := time.NewTicker(followPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
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
