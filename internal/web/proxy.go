package web

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// dialAndSend dials the daemon socket, sends req, reads one response, and
// closes the connection.
func (s *Server) dialAndSend(req protocol.Request) (protocol.Response, error) {
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		return protocol.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := protocol.WriteFrame(conn, req); err != nil {
		return protocol.Response{}, err
	}
	var resp protocol.Response
	if err := protocol.ReadFrame(conn, &resp); err != nil {
		return protocol.Response{}, err
	}
	return resp, nil
}

// proxyDispatch routes a browser WS message to the daemon via the control
// protocol, translating between the browser JSON format and wire frames.
func (s *Server) proxyDispatch(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker, ptys *ptyManager) {
	switch req.Type {
	case "list_projects":
		s.proxyListProjects(c, ctx)
	case "list_services":
		s.proxyListServices(c, ctx, req)
	case "start_project":
		s.proxyStartProject(c, ctx, req)
	case "stop_project":
		s.proxyStopProject(c, ctx, req)
	case "restart_service":
		s.proxyRestartService(c, ctx, req)
	case "stop_service":
		s.proxyStopService(c, ctx, req)
	case "kill_service":
		s.proxyKillService(c, ctx, req)
	case "subscribe_logs":
		s.proxySubscribeLogs(c, ctx, req, subs)
	case "unsubscribe_logs":
		s.handleUnsubscribeLogs(req, subs)
	case "spawn_terminal":
		s.handleSpawnTerminal(c, ctx, req, ptys)
	case "terminal_input":
		_ = ptys.write(req.ID, req.Data)
	case "terminal_resize":
		_ = ptys.resize(req.ID, req.Cols, req.Rows)
	case "close_terminal":
		ptys.closeSession(req.ID)
	case "list_actions":
		s.proxyListActions(c, ctx, req)
	case "run_action":
		s.proxyRunAction(c, ctx, req)
	default:
		s.sendError(c, ctx, "unknown message type: "+req.Type)
	}
}

func (s *Server) proxyListProjects(c *websocket.Conn, ctx context.Context) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindListProjects})
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		s.sendError(c, ctx, resp.Error)
		return
	}
	data, _ := json.Marshal(resp.Projects)
	s.send(c, ctx, wsResponse{Type: "projects", Data: data})
}

func (s *Server) proxyListServices(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindList, Project: req.Project})
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		s.sendError(c, ctx, resp.Error)
		return
	}
	data, _ := json.Marshal(resp.States)
	s.send(c, ctx, wsResponse{Type: "services", Project: req.Project, Data: data})
}

func (s *Server) proxyStartProject(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	if req.ConfigPath == "" && req.Project != "" {
		resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindListProjects})
		if err == nil && resp.Kind != protocol.KindError {
			for _, p := range resp.Projects {
				if p.Name == req.Project {
					req.ConfigPath = p.ConfigPath
					break
				}
			}
		}
	}
	if req.ConfigPath == "" {
		s.sendError(c, ctx, "config_path is required")
		return
	}
	resp, err := s.dialAndSend(protocol.Request{
		Kind:          protocol.KindStartProject,
		ConfigPath:    req.ConfigPath,
		EnvFile:       req.EnvFile,
		RemoveOrphans: req.RemoveOrphans,
	})
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	if resp.Kind == protocol.KindError {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: resp.Error})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) proxyStopProject(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindStopProject, Project: req.Project})
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	if resp.Kind == protocol.KindError {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: resp.Error})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) proxyRestartService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindRestart, Project: req.Project, Service: req.Service})
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	if resp.Kind == protocol.KindError {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: resp.Error})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) proxyStopService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindStopService, Project: req.Project, Service: req.Service})
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	if resp.Kind == protocol.KindError {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: resp.Error})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) proxyKillService(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindKillService, Project: req.Project, Service: req.Service, Signal: req.Signal})
	if err != nil {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: err.Error()})
		return
	}
	if resp.Kind == protocol.KindError {
		s.send(c, ctx, wsResponse{Type: "result", Ok: false, Error: resp.Error})
		return
	}
	s.send(c, ctx, wsResponse{Type: "result", Ok: true})
}

func (s *Server) proxySubscribeLogs(c *websocket.Conn, ctx context.Context, req *wsRequest, subs *subTracker) {
	key := req.Project + "/" + req.Service
	subs.cancel(key)

	subCtx, cancel := context.WithCancel(ctx)
	subs.add(key, cancel)

	go func() {
		conn, err := net.Dial("unix", s.socketPath)
		if err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}
		defer func() { _ = conn.Close() }()

		// Unblock ReadFrame when the subscription is cancelled.
		go func() {
			<-subCtx.Done()
			_ = conn.Close()
		}()

		if err := protocol.WriteFrame(conn, protocol.Request{
			Kind:    protocol.KindLogs,
			Project: req.Project,
			Service: req.Service,
			Follow:  true,
			Tail:    protocol.DefaultLogTail,
		}); err != nil {
			s.send(c, subCtx, wsResponse{Type: "error", Error: err.Error()})
			return
		}

		for {
			select {
			case <-subCtx.Done():
				return
			default:
			}

			var resp protocol.Response
			if err := protocol.ReadFrame(conn, &resp); err != nil {
				return
			}
			switch resp.Kind {
			case protocol.KindLogLine:
				s.send(c, subCtx, wsResponse{
					Type:    "log_line",
					Project: resp.Project,
					Service: resp.Service,
					Line:    resp.Line,
				})
			case protocol.KindLogContent:
				for _, line := range strings.Split(strings.TrimRight(resp.Content, "\n"), "\n") {
					select {
					case <-subCtx.Done():
						return
					default:
					}
					s.send(c, subCtx, wsResponse{
						Type:    "log_line",
						Project: resp.Project,
						Service: resp.Service,
						Line:    line,
					})
				}
			case protocol.KindLogRotated:
				s.send(c, subCtx, wsResponse{
					Type:    "log_rotated",
					Project: resp.Project,
					Service: resp.Service,
				})
			case protocol.KindDone:
				return
			case protocol.KindError:
				s.sendError(c, subCtx, resp.Error)
				return
			}
		}
	}()
}

func (s *Server) proxyListActions(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	resp, err := s.dialAndSend(protocol.Request{Kind: protocol.KindListActions, Project: req.Project})
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		s.sendError(c, ctx, resp.Error)
		return
	}
	data, _ := json.Marshal(resp.Actions)
	s.send(c, ctx, wsResponse{Type: "actions", Project: req.Project, Data: data})
}

func (s *Server) proxyRunAction(c *websocket.Conn, ctx context.Context, req *wsRequest) {
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}
	defer func() { _ = conn.Close() }()

	if err := protocol.WriteFrame(conn, protocol.Request{
		Kind:    protocol.KindRunAction,
		Project: req.Project,
		Action:  req.Action,
		Args:    req.Args,
	}); err != nil {
		s.sendError(c, ctx, err.Error())
		return
	}

	for {
		var resp protocol.Response
		if err := protocol.ReadFrame(conn, &resp); err != nil {
			s.sendError(c, ctx, err.Error())
			return
		}
		switch resp.Kind {
		case protocol.KindLogLine:
			s.send(c, ctx, wsResponse{
				Type:    "log_line",
				Project: req.Project,
				Service: req.Action,
				Line:    resp.Line,
			})
		case protocol.KindDone:
			s.send(c, ctx, wsResponse{
				Type:     "action_done",
				Project:  req.Project,
				Action:   req.Action,
				Ok:       true,
				ExitCode: resp.ActionExitCode,
			})
			return
		case protocol.KindError:
			s.send(c, ctx, wsResponse{
				Type:    "action_done",
				Project: req.Project,
				Action:  req.Action,
				Ok:      false,
				Error:   resp.Error,
			})
			return
		}
	}
}
