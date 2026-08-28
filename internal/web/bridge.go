package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// clientDial connects to the daemon control socket.
func (s *Server) clientDial() (*control.Client, error) {
	c, err := control.Dial(s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("web: cannot reach daemon: %w", err)
	}
	return c, nil
}

// fail sends a bare error message to the browser.
func fail(cl *client, msg string) {
	cl.enqueue(wsResponse{Type: "error", Error: msg})
}

// wireError forwards an error.
func wireError(cl *client, respType string, project string, text string) {
	cl.enqueue(wsResponse{Type: respType, Project: project, Ok: false, Error: text})
}

// mustMarshal marshals v to JSON.
func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// --- queries ---

func (s *Server) handleListProjects(cl *client) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	projects, err := c.ListProjects()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "projects", Data: mustMarshal(projects)})
}

func (s *Server) handleListServices(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	states, err := c.List(req.Project)
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "services", Project: req.Project, Data: mustMarshal(states)})
}

func (s *Server) handleListPorts(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	ports, err := c.Ports(req.Project)
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "ports", Project: req.Project, Data: mustMarshal(ports)})
}

func (s *Server) handleListActions(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	actions, err := c.ListActions(req.Project)
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "actions", Project: req.Project, Data: mustMarshal(actions)})
}

func (s *Server) handleListActionStates(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	states, err := c.ListActionStates(req.Project)
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "action_states", Project: req.Project, Data: mustMarshal(states)})
}

func (s *Server) handleDaemonStatus(cl *client) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	info, err := c.DaemonStatus()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "daemon_status", Data: mustMarshal(info)})
}

// --- lifecycle mutations ---

func (s *Server) handleStartProject(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	configPath := req.ConfigPath
	if configPath == "" && req.Project != "" {
		projects, err := c.ListProjects()
		if err == nil {
			for _, p := range projects {
				if p.Name == req.Project {
					configPath = p.ConfigPath
					break
				}
			}
		}
	}
	if configPath == "" {
		wireError(cl, "result", req.Project, "config_path is required")
		return
	}
	removeOrphans := true
	if req.RemoveOrphans != nil {
		removeOrphans = *req.RemoveOrphans
	}
	if err := c.StartProject(configPath, false, req.EnvFile, removeOrphans); err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "result", Project: req.Project, Ok: true})
}

func (s *Server) handleStopProject(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.StopProject(req.Project); err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "result", Project: req.Project, Ok: true})
}

func (s *Server) handleServiceOp(cl *client, req *wsRequest, op string, respType string) {
	c, err := s.clientDial()
	if err != nil {
		wireError(cl, respType, req.Project, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	switch op {
	case "stop":
		err = c.StopService(req.Project, req.Service)
	case "start":
		err = c.StartService(req.Project, req.Service)
	case "restart":
		err = c.Restart(req.Project, req.Service)
	default:
		err = fmt.Errorf("unknown operation: %s", op)
	}

	if err != nil {
		wireError(cl, respType, req.Project, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: respType, Project: req.Project, Ok: true})
}

func (s *Server) handleKillService(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.KillService(req.Project, req.Service, req.Signal); err != nil {
		wireError(cl, "result", req.Project, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "result", Project: req.Project, Ok: true})
}

func (s *Server) handleRestartDaemon(cl *client) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, err.Error())
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.RestartDaemon(); err != nil {
		fail(cl, err.Error())
		return
	}
	cl.enqueue(wsResponse{Type: "done", Ok: true})
}

// --- actions ---

func (s *Server) runAction(cl *client, req *wsRequest) {
	go func() {
		c, err := s.clientDial()
		if err != nil {
			cl.enqueue(wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Error: err.Error()})
			return
		}
		defer func() { _ = c.Close() }()

		code, err := c.RunAction(cl.ctx, req.Project, req.Action, req.Args, nil)
		if err != nil {
			cl.enqueue(wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Error: err.Error()})
			return
		}
		cl.enqueue(wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Ok: true, ExitCode: &code})
	}()
}

// --- git ---

func (s *Server) gitFail(cl *client, respType string, project string, err error) {
	cl.enqueue(wsResponse{Type: respType, Project: project, Error: err.Error()})
}

func (s *Server) handleGitLog(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		s.gitFail(cl, "git_commits", req.Project, err)
		return
	}
	defer func() { _ = c.Close() }()

	commits, branches, tags, stashes, err := c.GitLog(req.Project)
	if err != nil {
		s.gitFail(cl, "git_commits", req.Project, err)
		return
	}
	payload := map[string]any{
		"commits":  commits,
		"branches": branches,
		"tags":     tags,
		"stashes":  stashes,
	}
	cl.enqueue(wsResponse{Type: "git_commits", Project: req.Project, Ok: true, Data: mustMarshal(payload)})
}

func (s *Server) handleGitDiff(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		s.gitFail(cl, "git_diff", req.Project, err)
		return
	}
	defer func() { _ = c.Close() }()

	diff, err := c.GitDiff(req.Project, req.Hash, req.Path, req.ContextLines)
	if err != nil {
		s.gitFail(cl, "git_diff", req.Project, err)
		return
	}
	cl.enqueue(wsResponse{Type: "git_diff", Project: req.Project, Ok: true, Data: mustMarshal(diff)})
}

func (s *Server) handleGitCommit(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		s.gitFail(cl, "git_commit_result", req.Project, err)
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.GitCommit(req.Project, req.Message); err != nil {
		s.gitFail(cl, "git_commit_result", req.Project, err)
		return
	}
	cl.enqueue(wsResponse{Type: "git_commit_result", Project: req.Project, Ok: true})
}

func (s *Server) handleGitStage(cl *client, req *wsRequest) {
	c, err := s.clientDial()
	if err != nil {
		s.gitFail(cl, "git_stage_result", req.Project, err)
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.GitStage(req.Project, req.Path, req.StageAll, req.Unstage); err != nil {
		s.gitFail(cl, "git_stage_result", req.Project, err)
		return
	}
	cl.enqueue(wsResponse{Type: "git_stage_result", Project: req.Project, Ok: true})
}

func (s *Server) handleGitRemote(cl *client, req *wsRequest, op string) {
	c, err := s.clientDial()
	if err != nil {
		s.gitFail(cl, "git_remote_result", req.Project, err)
		return
	}
	defer func() { _ = c.Close() }()

	var output string
	switch op {
	case "push":
		output, err = c.GitPush(req.Project)
	case "pull":
		output, err = c.GitPull(req.Project)
	case "fetch":
		output, err = c.GitFetch(req.Project)
	}
	if err != nil {
		s.gitFail(cl, "git_remote_result", req.Project, err)
		return
	}
	cl.enqueue(wsResponse{Type: "git_remote_result", Project: req.Project, Ok: true, Line: output})
}

// --- log streaming ---

type logTarget struct {
	project string
	service string
	action  string
}

func logSubKey(t logTarget) string {
	if t.action != "" {
		return "a:" + t.project + "/" + t.action
	}
	return t.project + "/" + t.service
}

func (s *Server) streamLogs(cl *client, subCtx context.Context, t logTarget, previous bool) {
	c, err := s.clientDial()
	if err != nil {
		fail(cl, fmt.Sprintf("web: cannot reach daemon: %v", err))
		return
	}
	defer func() { _ = c.Close() }()

	onRotate := func() {
		msg := wsResponse{Type: "log_rotated", Project: t.project}
		if t.action != "" {
			msg.Action = t.action
		} else {
			msg.Service = t.service
		}
		cl.enqueue(msg)
	}

	onLine := func(line string) {
		out := wsResponse{
			Type:    "log_line",
			Project: t.project,
			Prev:    previous,
			Line:    line,
		}
		if t.action != "" {
			out.Action = t.action
		} else {
			out.Service = t.service
		}
		cl.enqueue(out)
	}

	if t.action != "" {
		err = c.ActionLogsCtx(subCtx, t.project, t.action, !previous, previous, protocol.DefaultLogTail, onLine, onRotate)
	} else {
		err = c.LogsCtx(subCtx, t.project, t.service, !previous, previous, protocol.DefaultLogTail, onLine, onRotate)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fail(cl, err.Error())
	}
}

func (s *Server) startLogSubscription(cl *client, t logTarget, previous bool) {
	key := logSubKey(t)
	cl.subs.cancel(key)

	subCtx, cancel := context.WithCancel(cl.ctx)
	cl.subs.add(key, cancel)

	go s.streamLogs(cl, subCtx, t, previous)
}

func (s *Server) handleSubscribeLogs(cl *client, req *wsRequest) {
	s.startLogSubscription(cl, logTarget{project: req.Project, service: req.Service}, req.Previous)
}

func (s *Server) handleSubscribeActionLogs(cl *client, req *wsRequest) {
	s.startLogSubscription(cl, logTarget{project: req.Project, action: req.Action}, req.Previous)
}

// --- terminals ---

func (s *Server) projectDir(project string) (string, error) {
	if project == "" {
		return "", errors.New("web: project is required to spawn a terminal")
	}
	c, err := s.clientDial()
	if err != nil {
		return "", err
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
		fail(cl, err.Error())
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
		fail(cl, err.Error())
	}
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
	t.subs = make(map[string]context.CancelFunc)
	t.mu.Unlock()
}
