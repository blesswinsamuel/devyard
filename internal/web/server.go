// Package web serves the embedded SPA and provides access to the daemon
// control socket (via ConnectRPC HTTP/2 reverse proxy and a WebSocket endpoint
// for interactive terminal PTY sessions).
package web

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/net/http2"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

//go:embed dist/*
var distFS embed.FS

const (
	writeTimeout   = 5 * time.Second
	pingInterval   = 30 * time.Second
	pingTimeout    = 10 * time.Second
	maxReadLimit   = 1 << 20 // 1 MiB per browser message
	sendBufferSize = 256
)

// Server is the HTTP + WebSocket server for the web UI. It serves the
// embedded SPA at /, proxies ConnectRPC calls to the daemon over its Unix
// control socket, and handles interactive terminal PTY sessions at /ws.
type Server struct {
	addr       string
	socketPath string

	mu       sync.Mutex
	listener net.Listener
	closed   bool
	done     chan struct{}

	clientsMu sync.Mutex
	clients   map[*client]struct{}
}

// NewServer creates a web server bound to addr that forwards browser traffic
// to the daemon listening on socketPath.
func NewServer(addr, socketPath string) *Server {
	return &Server{
		addr:       addr,
		socketPath: socketPath,
		done:       make(chan struct{}),
		clients:    make(map[*client]struct{}),
	}
}

// ListenAndServe starts serving HTTP and ConnectRPC proxying. It returns
// once the listener is ready. Call Close to stop.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("web: server is closed")
	}
	s.listener = ln
	s.mu.Unlock()

	mux := http.NewServeMux()

	// Reverse proxy for direct ConnectRPC calls from browser to Unix socket
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.socketPath)
		},
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "localhost"
		},
		Transport:     transport,
		FlushInterval: -1, // flush streaming RPCs immediately
	}
	mux.Handle("/localcompose.v1.DaemonService/", proxy)

	mux.HandleFunc("/ws", s.handleWS)
	mux.Handle("/", s.spaHandler())
	httpSrv := &http.Server{Handler: mux}
	go func() { _ = httpSrv.Serve(ln) }()
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

// Close stops accepting connections and tears down active terminal sessions.
// It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.listener
	close(s.done)
	s.mu.Unlock()

	var err error
	if ln != nil {
		err = ln.Close()
	}
	s.clientsMu.Lock()
	remaining := make([]*client, 0, len(s.clients))
	for cl := range s.clients {
		remaining = append(remaining, cl)
	}
	s.clientsMu.Unlock()
	for _, cl := range remaining {
		cl.close()
	}
	return err
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

// --- Terminal WebSocket handling ---

type wsRequest struct {
	Type    string `json:"type"`
	Project string `json:"project,omitempty"`
	ID      string `json:"id,omitempty"`
	Data    string `json:"data,omitempty"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
}

type wsResponse struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

type client struct {
	srv  *Server
	conn *websocket.Conn
	ctx  context.Context
	send chan wsResponse
	done chan struct{}

	closeOnce sync.Once
	cancelCtx context.CancelFunc
	mu        sync.Mutex
	dead      bool

	ptys *ptyManager
}

func (s *Server) addClient(conn *websocket.Conn) *client {
	ctx, cancel := context.WithCancel(context.Background())
	cl := &client{
		srv:  s,
		conn: conn,
		ctx:  ctx,
		send: make(chan wsResponse, sendBufferSize),
		done: make(chan struct{}),
		ptys: newPTYManager(),
	}
	cl.cancelCtx = cancel

	s.clientsMu.Lock()
	s.clients[cl] = struct{}{}
	s.clientsMu.Unlock()
	go cl.writeLoop()
	return cl
}

func (cl *client) enqueue(resp wsResponse) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.dead {
		return
	}
	select {
	case cl.send <- resp:
	default:
		go cl.close()
	}
}

func (cl *client) writeLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case resp, ok := <-cl.send:
			if !ok {
				return
			}
			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(cl.ctx, writeTimeout)
			err = cl.conn.Write(ctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				cl.close()
				return
			}
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(cl.ctx, pingTimeout)
			err := cl.conn.Ping(pctx)
			cancel()
			if err != nil {
				cl.close()
				return
			}
		case <-cl.done:
			return
		}
	}
}

func (cl *client) close() {
	cl.closeOnce.Do(func() {
		cl.mu.Lock()
		cl.dead = true
		cl.mu.Unlock()
		close(cl.done)
		if cl.cancelCtx != nil {
			cl.cancelCtx()
		}
		_ = cl.conn.CloseNow()
		cl.ptys.closeAll()
		cl.srv.removeClient(cl)
	})
}

func (s *Server) removeClient(cl *client) {
	s.clientsMu.Lock()
	delete(s.clients, cl)
	s.clientsMu.Unlock()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost", "127.0.0.1"},
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(maxReadLimit)

	cl := s.addClient(conn)
	defer cl.close()

	for {
		_, msg, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var req wsRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			cl.enqueue(wsResponse{Type: "error", Error: "invalid message: " + err.Error()})
			continue
		}
		s.dispatch(cl, &req)
	}
}

func (s *Server) dispatch(cl *client, req *wsRequest) {
	switch req.Type {
	case "spawn_terminal":
		s.handleSpawnTerminal(cl, req)
	case "terminal_input":
		if err := cl.ptys.write(req.ID, req.Data); err != nil {
			cl.enqueue(wsResponse{Type: "error", Error: err.Error()})
		}
	case "terminal_resize":
		if err := cl.ptys.resize(req.ID, req.Cols, req.Rows); err != nil {
			cl.enqueue(wsResponse{Type: "error", Error: err.Error()})
		}
	case "close_terminal":
		cl.ptys.closeSession(req.ID)
	default:
		cl.enqueue(wsResponse{Type: "error", Error: "unknown message type: " + req.Type})
	}
}

func (s *Server) projectDir(project string) (string, error) {
	if project == "" {
		return "", errors.New("web: project is required to spawn a terminal")
	}
	c, err := control.Dial(s.socketPath)
	if err != nil {
		return "", fmt.Errorf("web: cannot reach daemon: %w", err)
	}
	defer func() { _ = c.Close() }()

	projects, err := c.ListProjects()
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Name == project && p.ConfigPath != "" {
			return filepath.Dir(p.ConfigPath), nil
		}
	}
	return "", fmt.Errorf("web: unknown project %q", project)
}

func (s *Server) handleSpawnTerminal(cl *client, req *wsRequest) {
	dir, err := s.projectDir(req.Project)
	if err != nil {
		cl.enqueue(wsResponse{Type: "error", Error: err.Error()})
		return
	}
	id := req.ID
	err = cl.ptys.spawn(dir, id, req.Cols, req.Rows,
		func(output string) {
			cl.enqueue(wsResponse{Type: "terminal_output", ID: id, Output: output})
		},
		func() {
			cl.enqueue(wsResponse{Type: "terminal_exit", ID: id})
		},
	)
	if err != nil {
		cl.enqueue(wsResponse{Type: "error", Error: err.Error()})
	}
}
