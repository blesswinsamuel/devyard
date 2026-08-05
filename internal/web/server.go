// Package web serves the HTTP and WebSocket browser frontend. It is a fourth
// frontend over the same control protocol used by the CLI and TUI: the WS
// server dispatches JSON messages to the daemon's MultiBackend, bridging
// project/service management and log streaming to the browser.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

//go:embed dist/*
var distFS embed.FS

// Server is the HTTP + WebSocket server for the web UI. It serves the embedded
// SPA at / and a WebSocket endpoint at /ws. In in-process mode (used by the
// daemon) the handler dispatches directly to a MultiBackend. In proxy mode
// (used by the `web` subcommand) it translates WS messages to control-protocol
// frames over a Unix socket.
type Server struct {
	backend    control.MultiBackend // the daemon's MultiBackend (nil in proxy mode)
	socketPath string               // daemon socket path (empty in in-process mode)
	addr       string               // host:port
	listener   net.Listener
	closed     bool
	mu         sync.Mutex
}

// NewServer creates a web server bound to addr. The daemon's MultiBackend is
// used for all WS dispatch (in-process, no socket dialing).
func NewServer(addr string, backend control.MultiBackend) *Server {
	return &Server{addr: addr, backend: backend}
}

// NewProxyServer creates a web server that proxies WS messages to the daemon
// over the given Unix socket, without holding an in-process MultiBackend.
func NewProxyServer(addr, socketPath string) *Server {
	return &Server{addr: addr, socketPath: socketPath}
}

// ListenAndServe starts the HTTP server. It returns once the listener is
// ready. Call Close to stop.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.Handle("/", s.spaHandler())
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// Addr returns the actual listener address (useful when port 0 is used).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

// Close stops the listener.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// spaHandler serves the embedded SPA. Any path that doesn't match a static
// file falls back to index.html (SPA routing).
func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not built", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			// io/fs paths must not start with '/'; Stat the trimmed name.
			name := strings.TrimPrefix(path, "/")
			if _, err := fs.Stat(sub, name); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// --- WebSocket message types ---

// wsRequest is a JSON message from the browser to the daemon.
type wsRequest struct {
	Type       string `json:"type"`
	Project    string `json:"project,omitempty"`
	Service    string `json:"service,omitempty"`
	Signal     string `json:"signal,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	EnvFile    string `json:"env_file,omitempty"`
}

// wsResponse is a JSON message from the daemon to the browser.
type wsResponse struct {
	Type    string          `json:"type"`
	Project string          `json:"project,omitempty"`
	Service string          `json:"service,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Line    string          `json:"line,omitempty"`
	Ok      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
}

// handleWS upgrades to WebSocket and runs the JSON message loop.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost", "127.0.0.1"},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx := r.Context()

	// Track active log subscriptions so we can cancel them on disconnect.
	subs := newSubTracker()
	defer subs.closeAll()

	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req wsRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			s.sendError(c, ctx, "invalid message: "+err.Error())
			continue
		}
		if s.socketPath != "" {
			s.proxyDispatch(c, ctx, &req, subs)
		} else {
			s.dispatchWS(c, ctx, &req, subs)
		}
	}
}

func (s *Server) dispatchWS(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker) {
	switch req.Type {
	case "list_projects":
		s.handleListProjects(c, ctx)
	case "list_services":
		s.handleListServices(c, ctx, req)
	case "start_project":
		s.handleStartProject(c, ctx, req)
	case "stop_project":
		s.handleStopProject(c, ctx, req)
	case "restart_service":
		s.handleRestartService(c, ctx, req)
	case "stop_service":
		s.handleStopService(c, ctx, req)
	case "kill_service":
		s.handleKillService(c, ctx, req)
	case "subscribe_logs":
		s.handleSubscribeLogs(c, ctx, req, subs)
	case "unsubscribe_logs":
		s.handleUnsubscribeLogs(req, subs)
	default:
		s.sendError(c, ctx, "unknown message type: "+req.Type)
	}
}

func (s *Server) handleListProjects(c *websocket.Conn, ctx context.Context) {
	projects := s.backend.ListProjects()
	data, _ := json.Marshal(projects)
	s.send(c, ctx, wsResponse{Type: "projects", Data: data})
}

func (s *Server) handleListServices(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	states := b.States()
	data, _ := json.Marshal(states)
	s.send(c, ctx, wsResponse{Type: "services", Project: req.Project, Data: data})
}

func (s *Server) handleStartProject(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	if req.ConfigPath == "" {
		s.sendError(c, ctx, "config_path is required")
		return
	}
	if err := s.backend.StartProject(req.ConfigPath, false, req.EnvFile); err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) handleStopProject(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	if err := s.backend.StopProject(req.Project); err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) handleRestartService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if err := b.Restart(req.Service); err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) handleStopService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if err := b.StopService(req.Service); err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) handleKillService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if err := b.KillService(req.Service, req.Signal); err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) handleSubscribeLogs(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker) {
	key := req.Project + "/" + req.Service

	// Cancel any existing subscription for this key.
	subs.cancel(key)

	subCtx, cancel := context.WithCancel(ctx)
	subs.add(key, cancel)

	go func() {
		b, err := s.backend.ProjectBackend(req.Project)
		if err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		path, err := b.LogPath(req.Service)
		if err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		streamLogFile(c, subCtx, path, req.Project, req.Service)
	}()
}

func (s *Server) handleUnsubscribeLogs(req *wsRequest, subs *subTracker) {
	key := req.Project + "/" + req.Service
	subs.cancel(key)
}

func (s *Server) send(c *websocket.Conn, ctx context.Context, resp wsResponse) {
	data, _ := json.Marshal(resp)
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	_ = c.Write(writeCtx, websocket.MessageText, data)
}

func (s *Server) sendError(c *websocket.Conn, ctx context.Context, msg string) {
	s.send(c, ctx, wsResponse{Type: "error", Error: msg})
}

const writeTimeout = 5 * time.Second

// streamLogFile reads existing log content (last DefaultLogTail lines) and
// tails for new lines, sending log_line WS messages for each line. It returns
// when the context is cancelled (unsubscribe or disconnect).
func streamLogFile(c *websocket.Conn, ctx context.Context, path, project, service string) {
	f, err := os.Open(path)
	if err != nil {
		resp, _ := json.Marshal(wsResponse{
			Type:    "error",
			Project: project,
			Service: service,
			Error:   err.Error(),
		})
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		_ = c.Write(writeCtx, websocket.MessageText, resp)
		return
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 4096)
	var leftover []byte

	sendLine := func(line string) {
		resp, _ := json.Marshal(wsResponse{
			Type:    "log_line",
			Project: project,
			Service: service,
			Line:    line,
		})
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		if err := c.Write(writeCtx, websocket.MessageText, resp); err != nil {
			cancel() // connection closed
		}
	}

	// sendRotated tells the browser that a new run started (the log was
	// rotated), so the SPA can reset its log view to the fresh run.
	sendRotated := func() {
		resp, _ := json.Marshal(wsResponse{
			Type:    "log_rotated",
			Project: project,
			Service: service,
		})
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		if err := c.Write(writeCtx, websocket.MessageText, resp); err != nil {
			cancel() // connection closed
		}
	}

	flush := func(chunk []byte) {
		data := append(leftover, chunk...)
		leftover = leftover[:0]
		for {
			i := indexByte(data, '\n')
			if i < 0 {
				leftover = append(leftover, data...)
				return
			}
			sendLine(string(data[:i]))
			data = data[i+1:]
		}
	}

	// Drain tailed history (same window as the control protocol / TUI).
	history, err := control.ReadLogHistory(f, protocol.DefaultLogTail)
	if err != nil {
		return
	}
	if len(history) > 0 {
		flush(history)
		if len(leftover) > 0 {
			sendLine(string(leftover))
			leftover = leftover[:0]
		}
	}

	select {
	case <-ctx.Done():
		return
	default:
	}

	// Follow: poll for new content. Reopen the file if it rotates (the
	// supervisor swaps <name>.log for a fresh file on each spawn) so the
	// stream keeps following the live run instead of freezing on the old one.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nf, err := control.ReopenIfRotated(f, path)
			if err != nil {
				return
			}
			if nf != f {
				_ = f.Close()
				f = nf
				leftover = leftover[:0]
				sendRotated()
			}
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				n, err := f.Read(buf)
				if n > 0 {
					flush(buf[:n])
				}
				if err != nil {
					break
				}
			}
		}
	}
}

func indexByte(data []byte, b byte) int {
	for i, c := range data {
		if c == b {
			return i
		}
	}
	return -1
}

// --- subscription tracking ---

type subTracker struct {
	mu   sync.Mutex
	subs map[string]context.CancelFunc
}

func newSubTracker() *subTracker {
	return &subTracker{subs: make(map[string]context.CancelFunc)}
}

func (t *subTracker) add(key string, cancel context.CancelFunc) {
	t.mu.Lock()
	if old, ok := t.subs[key]; ok {
		old()
	}
	t.subs[key] = cancel
	t.mu.Unlock()
}

func (t *subTracker) cancel(key string) {
	t.mu.Lock()
	if cancel, ok := t.subs[key]; ok {
		cancel()
		delete(t.subs, key)
	}
	t.mu.Unlock()
}

func (t *subTracker) closeAll() {
	t.mu.Lock()
	for _, cancel := range t.subs {
		cancel()
	}
	t.subs = nil
	t.mu.Unlock()
}
