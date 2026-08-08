// Package web serves the HTTP and WebSocket browser frontend. It is a fourth
// frontend over the same control protocol used by the CLI: the WS
// server dispatches JSON messages to the daemon's MultiBackend, bridging
// project/service management and log streaming to the browser.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

	// Connected WS clients, guarded by clientsMu.
	clientsMu sync.Mutex
	clients   map[*wsClient]struct{}
}

// wsClient tracks one active WebSocket connection for broadcast purposes.
type wsClient struct {
	conn *websocket.Conn
	ctx  context.Context
}

// NewServer creates a web server bound to addr. The daemon's MultiBackend is
// used for all WS dispatch (in-process, no socket dialing).
func NewServer(addr string, backend control.MultiBackend) *Server {
	return &Server{addr: addr, backend: backend, clients: make(map[*wsClient]struct{})}
}

// NewProxyServer creates a web server that proxies WS messages to the daemon
// over the given Unix socket, without holding an in-process MultiBackend.
func NewProxyServer(addr, socketPath string) *Server {
	return &Server{addr: addr, socketPath: socketPath, clients: make(map[*wsClient]struct{})}
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

	if s.backend != nil {
		s.backend.SetOnStateChange(func(project string, state protocol.ServiceState) {
			data, _ := json.Marshal(state)
			s.broadcast(wsResponse{
				Type:    "state_changed",
				Project: project,
				Service: state.Name,
				Data:    data,
			})
		})
		s.backend.SetOnActionStateChange(func(project string, state protocol.ActionState) {
			data, _ := json.Marshal(state)
			s.broadcast(wsResponse{
				Type:    "action_state_changed",
				Project: project,
				Action:  state.Name,
				Data:    data,
			})
		})
		s.backend.SetOnGitChange(func(project string) {
			s.broadcast(wsResponse{
				Type:    "git_changed",
				Project: project,
			})
		})
	}

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

// wsResponse is a JSON message from the daemon to the browser.
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

// handleWS upgrades to WebSocket and runs the JSON message loop.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost", "127.0.0.1"},
	})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()

	ctx := r.Context()
	s.addClient(c, ctx)
	defer s.removeClient(c)

	// Track active log subscriptions so we can cancel them on disconnect.
	subs := newSubTracker()
	defer subs.closeAll()

	ptys := newPTYManager()
	defer ptys.closeAll()

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
			s.proxyDispatch(c, ctx, &req, subs, ptys)
		} else {
			s.dispatchWS(c, ctx, &req, subs, ptys)
		}
	}
}

func (s *Server) dispatchWS(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker, ptys *ptyManager) {
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
	case "start_service":
		s.handleStartService(c, ctx, req)
	case "kill_service":
		s.handleKillService(c, ctx, req)
	case "subscribe_logs":
		s.handleSubscribeLogs(c, ctx, req, subs)
	case "unsubscribe_logs":
		s.handleUnsubscribeLogs(req, subs)
	case "subscribe_action_logs":
		s.handleSubscribeActionLogs(c, ctx, req, subs)
	case "unsubscribe_action_logs":
		s.handleUnsubscribeActionLogs(req, subs)
	case "spawn_terminal":
		s.handleSpawnTerminal(c, ctx, req, ptys)
	case "terminal_input":
		_ = ptys.write(req.ID, req.Data)
	case "terminal_resize":
		_ = ptys.resize(req.ID, req.Cols, req.Rows)
	case "close_terminal":
		ptys.closeSession(req.ID)
	case "list_actions":
		s.handleListActions(c, ctx, req)
	case "list_action_states":
		s.handleListActionStates(c, ctx, req)
	case "run_action":
		s.handleRunAction(c, ctx, req)
	case "git_log":
		s.handleGitLog(c, ctx, req)
	case "git_diff":
		s.handleGitDiff(c, ctx, req)
	case "git_commit":
		s.handleGitCommit(c, ctx, req)
	case "git_stage":
		s.handleGitStage(c, ctx, req)
	case "daemon_status":
		s.handleDaemonStatus(c, ctx)
	case "restart_daemon":
		s.handleRestartDaemon(c, ctx)
	default:
		s.sendError(c, ctx, "unknown message type: "+req.Type)
	}
}

func (s *Server) resolveProjectDir(project string) string {
	if project == "" {
		dir, _ := os.Getwd()
		return dir
	}
	var configPath string
	if s.backend != nil {
		for _, p := range s.backend.ListProjects() {
			if p.Name == project {
				configPath = p.ConfigPath
				break
			}
		}
	} else if s.socketPath != "" {
		resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindListProjects})
		if err == nil && resp.Kind != protocol.KindError {
			for _, p := range resp.Projects {
				if p.Name == project {
					configPath = p.ConfigPath
					break
				}
			}
		}
	}
	if configPath != "" {
		return filepath.Dir(configPath)
	}
	dir, _ := os.Getwd()
	return dir
}

func (s *Server) handleSpawnTerminal(c *websocket.Conn, ctx context.Context, req *wsRequest, ptys *ptyManager) {
	dir := s.resolveProjectDir(req.Project)
	err := ptys.spawn(ctx, req.ID, dir, req.Cols, req.Rows,
		func(output string) {
			s.send(c, ctx, wsResponse{Type: "terminal_output", ID: req.ID, Output: output})
		},
		func() {
			s.send(c, ctx, wsResponse{Type: "terminal_exit", ID: req.ID})
		},
	)
	if err != nil {
		s.sendError(c, ctx, err.Error())
	}
}

func (s *Server) handleListProjects(c *websocket.Conn, ctx context.Context) {
	projects := s.backend.ListProjects()
	data, _ := json.Marshal(projects)
	s.send(c, ctx, wsResponse{Type: "projects", Data: data})
}

func (s *Server) handleDaemonStatus(c *websocket.Conn, ctx context.Context) {
	info, err := s.backend.DaemonStatus()
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	data, _ := json.Marshal(info)
	s.send(c, ctx, wsResponse{Type: "daemon_status", Data: data})
}

func (s *Server) handleRestartDaemon(c *websocket.Conn, ctx context.Context) {
	if err := s.backend.RestartDaemon(); err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	s.send(c, ctx, wsResponse{Type: "done", Ok: true})
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
	if req.ConfigPath == "" && req.Project != "" {
		for _, p := range s.backend.ListProjects() {
			if p.Name == req.Project {
				req.ConfigPath = p.ConfigPath
				break
			}
		}
	}
	if req.ConfigPath == "" {
		s.sendError(c, ctx, "config_path is required")
		return
	}
	removeOrphans := true
	if req.RemoveOrphans != nil {
		removeOrphans = *req.RemoveOrphans
	}
	if err := s.backend.StartProject(req.ConfigPath, false, req.EnvFile, removeOrphans); err != nil {
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

func (s *Server) handleStartService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if err := b.StartService(req.Service); err != nil {
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

func (s *Server) handleListActions(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	actions := b.ListActions()
	data, _ := json.Marshal(actions)
	s.send(c, ctx, wsResponse{Type: "actions", Project: req.Project, Data: data})
}

func (s *Server) handleListActionStates(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	b, err := s.backend.ProjectBackend(req.Project)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	states := b.ActionStates()
	data, _ := json.Marshal(states)
	s.send(c, ctx, wsResponse{Type: "action_states", Project: req.Project, Data: data})
}

func (s *Server) handleGitLog(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	commits, branches, tags, stashes, err := s.backend.GitLog(req.Project)
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "git_commits", Project: req.Project, Ok: false, Error: err.Error()})
		return
	}
	payload := map[string]any{
		"commits":  commits,
		"branches": branches,
		"tags":     tags,
		"stashes":  stashes,
	}
	data, _ := json.Marshal(payload)
	s.send(c, ctx, wsResponse{Type: "git_commits", Project: req.Project, Ok: true, Data: data})
}

func (s *Server) handleGitDiff(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	diffRes, err := s.backend.GitDiff(req.Project, req.Hash, req.ContextLines)
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "git_diff", Project: req.Project, Ok: false, Error: err.Error()})
		return
	}
	data, _ := json.Marshal(diffRes)
	s.send(c, ctx, wsResponse{Type: "git_diff", Project: req.Project, Ok: true, Data: data})
}

func (s *Server) handleGitCommit(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	if err := s.backend.GitCommit(req.Project, req.Message); err != nil {
		s.send(c, ctx, wsResponse{Type: "git_commit_result", Project: req.Project, Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "git_commit_result", Project: req.Project, Ok: true})
}

func (s *Server) handleGitStage(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	if err := s.backend.GitStage(req.Project, req.Path, req.StageAll, req.Unstage); err != nil {
		s.send(c, ctx, wsResponse{Type: "git_stage_result", Project: req.Project, Ok: false, Error: err.Error()})
		return
	}
	s.send(c, ctx, wsResponse{Type: "git_stage_result", Project: req.Project, Ok: true})
}

func (s *Server) handleRunAction(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	go func() {
		b, err := s.backend.ProjectBackend(req.Project)
		if err != nil {
			s.sendError(c, ctx, err.Error())
			return
		}
		// Output is captured to the action's log file by the supervisor; the
		// browser follows it through the subscribe_action_logs stream (also
		// initiated by the UI), so nothing is streamed over this message.
		code, err := b.RunAction(ctx, req.Action, req.Args, io.Discard)
		if err != nil {
			s.send(c, ctx, wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Ok: false, Error: err.Error()})
			return
		}
		s.send(c, ctx, wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Ok: true, ExitCode: &code})
	}()
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
		var path string
		if req.Previous {
			path, err = b.PreviousLogPath(req.Service)
		} else {
			path, err = b.LogPath(req.Service)
		}
		if err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		// A previous run is a completed run, so it is never followed and never
		// rotates: stream its content once and stop.
		streamLogFile(c, subCtx, path, req.Project, req.Service, "", req.Previous, !req.Previous)
	}()
}

func (s *Server) handleUnsubscribeLogs(req *wsRequest, subs *subTracker) {
	key := req.Project + "/" + req.Service
	subs.cancel(key)
}

// actionSubKey is the subscription-tracker key for an action log stream. It is
// namespaced separately from service log streams so a service and an action
// sharing a name don't cancel each other's subscriptions.
func actionSubKey(project, action string) string {
	return "a:" + project + "/" + action
}

// handleSubscribeActionLogs follows an action's log file, waiting for it to
// appear on the first run. It mirrors handleSubscribeLogs but targets the
// action's log and tags each streamed line with the action name so the browser
// routes it to the action pane rather than a service pane.
func (s *Server) handleSubscribeActionLogs(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker) {
	key := actionSubKey(req.Project, req.Action)

	subs.cancel(key)

	subCtx, cancel := context.WithCancel(ctx)
	subs.add(key, cancel)

	go func() {
		b, err := s.backend.ProjectBackend(req.Project)
		if err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		var path string
		if req.Previous {
			// A previous run implies the action already ran, so the log exists
			// and no waiter is needed.
			path, err = b.ActionPreviousLogPath(req.Action)
		} else {
			path, err = control.WaitForActionLog(subCtx, b, req.Action)
		}
		if err != nil {
			// The subscription was replaced or the connection closed; there is
			// nothing to report.
			if subCtx.Err() != nil {
				return
			}
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		streamLogFile(c, subCtx, path, req.Project, "", req.Action, req.Previous, !req.Previous)
	}()
}

func (s *Server) handleUnsubscribeActionLogs(req *wsRequest, subs *subTracker) {
	subs.cancel(actionSubKey(req.Project, req.Action))
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

func (s *Server) addClient(c *websocket.Conn, ctx context.Context) {
	s.clientsMu.Lock()
	s.clients[&wsClient{conn: c, ctx: ctx}] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(c *websocket.Conn) {
	s.clientsMu.Lock()
	for cl := range s.clients {
		if cl.conn == c {
			delete(s.clients, cl)
			break
		}
	}
	s.clientsMu.Unlock()
}

// broadcast sends a WS response to every connected client. Clients that fail
// to write are silently removed.
func (s *Server) broadcast(resp wsResponse) {
	data, _ := json.Marshal(resp)
	s.clientsMu.Lock()
	var dead []*wsClient
	for cl := range s.clients {
		writeCtx, cancel := context.WithTimeout(cl.ctx, writeTimeout)
		if err := cl.conn.Write(writeCtx, websocket.MessageText, data); err != nil {
			dead = append(dead, cl)
		}
		cancel()
	}
	for _, cl := range dead {
		delete(s.clients, cl)
	}
	s.clientsMu.Unlock()
}

const writeTimeout = 5 * time.Second

// streamLogFile reads existing log content (last DefaultLogTail lines) and,
// when follow is true, tails for new lines, sending log_line WS messages for
// each line. It returns when the context is cancelled (unsubscribe or
// disconnect) or, when follow is false, after the file's content is fully
// streamed. Exactly one of service and action is non-empty: service logs are
// tagged with the service name, action logs with the action name, so the
// browser can route each stream to the right pane. previous marks a previous
// run's completed log (the current file's ".prev.log"); every line carries
// that flag so the browser can render it distinctly.
func streamLogFile(c *websocket.Conn, ctx context.Context, path, project, service, action string, previous, follow bool) {
	f, err := os.Open(path)
	if err != nil {
		resp, _ := json.Marshal(wsResponse{
			Type:    "error",
			Project: project,
			Service: service,
			Action:  action,
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
			Action:  action,
			Prev:    previous,
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
			Action:  action,
		})
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		if err := c.Write(writeCtx, websocket.MessageText, resp); err != nil {
			cancel() // connection closed
		}
	}

	flush := func(chunk []byte) {
		leftover = append(leftover, chunk...)
		for {
			i := indexByte(leftover, '\n')
			if i < 0 {
				return
			}
			line := string(leftover[:i])
			leftover = append([]byte(nil), leftover[i+1:]...)
			sendLine(line)
		}
	}

	// Drain tailed history (same window as the control protocol).
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

	// A previous run has already ended; stream its stored content and stop
	// rather than following (nothing is live and the file never rotates).
	if !follow {
		return
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
