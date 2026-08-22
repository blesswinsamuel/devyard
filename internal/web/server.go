// Package web serves the embedded SPA and a WebSocket endpoint (/ws) for the
// browser frontend. It is a thin bridge between browser JSON messages and the
// daemon's control socket: requests are translated to control-protocol
// frames, daemon events are fanned out to connected browsers, and interactive
// terminals are spawned locally. No process supervision happens here.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
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
// embedded SPA at / and bridges /ws traffic to the daemon over its Unix
// control socket.
type Server struct {
	addr       string
	socketPath string

	mu       sync.Mutex
	listener net.Listener
	closed   bool
	done     chan struct{}
	events   *control.Client // live event-subscription connection, closed on shutdown

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

// ListenAndServe starts serving HTTP and the daemon event fan-out. It returns
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

	go s.runEvents()

	mux := http.NewServeMux()
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

// Close stops accepting connections, tears down every active WS client, and
// unblocks the event subscription. It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.listener
	ev := s.events
	close(s.done)
	s.mu.Unlock()

	if ev != nil {
		_ = ev.Close()
	}
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

// --- WebSocket message types ---

// wsRequest is a JSON message from the browser.
type wsRequest struct {
	Type          string   `json:"type"`
	Project       string   `json:"project,omitempty"`
	Service       string   `json:"service,omitempty"`
	Action        string   `json:"action,omitempty"`
	Args          []string `json:"args,omitempty"`
	Signal        string   `json:"signal,omitempty"`
	ConfigPath    string   `json:"config_path,omitempty"`
	EnvFile       string   `json:"env_file,omitempty"`
	RemoveOrphans *bool    `json:"remove_orphans,omitempty"`
	Previous      bool     `json:"prev,omitempty"`
	Hash          string   `json:"hash,omitempty"`
	Path          string   `json:"path,omitempty"`
	Message       string   `json:"message,omitempty"`
	Unstage       bool     `json:"unstage,omitempty"`
	StageAll      bool     `json:"stage_all,omitempty"`
	ContextLines  int      `json:"context_lines,omitempty"`

	// Terminal/PTY fields
	ID   string `json:"id,omitempty"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// wsResponse is a JSON message to the browser.
type wsResponse struct {
	Type     string          `json:"type"`
	Project  string          `json:"project,omitempty"`
	Service  string          `json:"service,omitempty"`
	Action   string          `json:"action,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Line     string          `json:"line,omitempty"`
	Prev     bool            `json:"prev,omitempty"`
	Ok       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	ExitCode *int            `json:"exit_code,omitempty"`

	// Terminal/PTY fields
	ID     string `json:"id,omitempty"`
	Output string `json:"output,omitempty"`
}

// --- per-client plumbing ---

// client tracks one browser WebSocket connection. Every message sent to the
// browser goes through sendCh so ordering is guaranteed and no goroutine ever
// blocks on a dead peer: enqueues are non-blocking, and the single writer
// goroutine drops the whole client when a write fails or the queue overflows.
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

	subs *subTracker
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
		subs: newSubTracker(),
		ptys: newPTYManager(),
	}
	// cancel is invoked by close; keep it referenced there.
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
		// Slow consumer: drop the connection instead of blocking broadcasts.
		cl.dead = true
		go cl.close()
	}
}

func (cl *client) writeLoop() {
	pinger := time.NewTicker(pingInterval)
	defer pinger.Stop()
	for {
		select {
		case resp := <-cl.send:
			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err = cl.conn.Write(wctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				cl.close()
				return
			}
		case <-pinger.C:
			pctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
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
		cl.subs.closeAll()
		cl.ptys.closeAll()
		cl.srv.removeClient(cl)
	})
}

func (s *Server) removeClient(cl *client) {
	s.clientsMu.Lock()
	delete(s.clients, cl)
	s.clientsMu.Unlock()
}

// broadcast sends a message to every connected client. Enqueueing is
// non-blocking, so this never stalls on a slow consumer.
func (s *Server) broadcast(resp wsResponse) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for cl := range s.clients {
		cl.enqueue(resp)
	}
}

// --- connection handling ---

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

// dispatch routes one browser message. Terminal messages are handled locally;
// everything else proxies to the daemon.
func (s *Server) dispatch(cl *client, req *wsRequest) {
	switch req.Type {
	case "list_projects":
		s.handleListProjects(cl)
	case "list_services":
		s.handleListServices(cl, req)
	case "start_project":
		s.handleStartProject(cl, req)
	case "stop_project":
		s.handleStopProject(cl, req)
	case "restart_service":
		s.handleServiceOp(cl, req, protocol.KindRestart, "result")
	case "stop_service":
		s.handleServiceOp(cl, req, protocol.KindStopService, "result")
	case "start_service":
		s.handleServiceOp(cl, req, protocol.KindStartService, "result")
	case "kill_service":
		s.handleKillService(cl, req)
	case "subscribe_logs":
		s.handleSubscribeLogs(cl, req)
	case "unsubscribe_logs":
		cl.subs.cancel(logSubKey(logTarget{project: req.Project, service: req.Service}))
	case "subscribe_action_logs":
		s.handleSubscribeActionLogs(cl, req)
	case "unsubscribe_action_logs":
		cl.subs.cancel(logSubKey(logTarget{project: req.Project, action: req.Action}))
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
	case "list_actions":
		s.handleListActions(cl, req)
	case "list_action_states":
		s.handleListActionStates(cl, req)
	case "run_action":
		s.runAction(cl, req)
	case "git_log":
		s.handleGitLog(cl, req)
	case "git_diff":
		s.handleGitDiff(cl, req)
	case "git_commit":
		s.handleGitCommit(cl, req)
	case "git_stage":
		s.handleGitStage(cl, req)
	case "git_push":
		s.handleGitRemote(cl, req, protocol.KindGitPush)
	case "git_pull":
		s.handleGitRemote(cl, req, protocol.KindGitPull)
	case "git_fetch":
		s.handleGitRemote(cl, req, protocol.KindGitFetch)
	case "daemon_status":
		s.handleDaemonStatus(cl)
	case "restart_daemon":
		s.handleRestartDaemon(cl)
	case "list_ports":
		s.handleListPorts(cl, req)
	default:
		cl.enqueue(wsResponse{Type: "error", Error: "unknown message type: " + req.Type})
	}
}

// --- daemon event fan-out ---

// runEvents keeps a persistent KindSubscribeEvents connection to the daemon,
// rebroadcasting state/action/git changes to every browser. It reconnects
// until the server is closed.
func (s *Server) runEvents() {
	for {
		if s.isClosed() {
			return
		}
		c, err := control.Dial(s.socketPath)
		if err == nil {
			s.setEvents(c)
			err = c.Send(protocol.Request{Kind: protocol.KindSubscribeEvents})
			if err == nil {
				s.pumpEvents(c)
			}
			s.setEvents(nil)
			_ = c.Close()
		}
		if s.isClosed() {
			return
		}
		select {
		case <-s.done:
			return
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) pumpEvents(c *control.Client) {
	for {
		resp, err := c.Recv()
		if err != nil {
			return
		}
		switch resp.Kind {
		case protocol.KindEventStateChanged:
			if resp.SingleState != nil {
				data, _ := json.Marshal(resp.SingleState)
				s.broadcast(wsResponse{
					Type:    "state_changed",
					Project: resp.Project,
					Service: resp.Service,
					Data:    data,
				})
			}
		case protocol.KindEventActionStateChanged:
			if resp.SingleActionState != nil {
				data, _ := json.Marshal(resp.SingleActionState)
				s.broadcast(wsResponse{
					Type:    "action_state_changed",
					Project: resp.Project,
					Action:  resp.Action,
					Data:    data,
				})
			}
		case protocol.KindEventGitChanged:
			s.broadcast(wsResponse{Type: "git_changed", Project: resp.Project})
		}
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) setEvents(c *control.Client) {
	s.mu.Lock()
	s.events = c
	s.mu.Unlock()
}
