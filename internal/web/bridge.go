package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// call performs a single request/response round trip against the daemon on a
// short-lived connection.
func (s *Server) call(req protocol.Request) (protocol.Response, error) {
	c, err := control.Dial(s.socketPath)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("web: cannot reach daemon: %w", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Send(req); err != nil {
		return protocol.Response{}, fmt.Errorf("web: daemon request failed: %w", err)
	}
	return c.Recv()
}

// fail sends a bare error message to the browser.
func fail(cl *client, msg string) {
	cl.enqueue(wsResponse{Type: "error", Error: msg})
}

// wireError forwards a control-protocol KindError frame verbatim.
func wireError(cl *client, respType string, project string, text string) {
	cl.enqueue(wsResponse{Type: respType, Project: project, Ok: false, Error: text})
}

// marshal marshals v, which for this package's payload types cannot fail.
func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// --- queries ---

func (s *Server) handleListProjects(cl *client) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindListProjects})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "projects", Data: mustMarshal(resp.Projects)})
}

func (s *Server) handleListServices(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindList, Project: req.Project})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "services", Project: req.Project, Data: mustMarshal(resp.States)})
}

func (s *Server) handleListPorts(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindListPorts, Project: req.Project})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "ports", Project: req.Project, Data: mustMarshal(resp.Ports)})
}

func (s *Server) handleListActions(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindListActions, Project: req.Project})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "actions", Project: req.Project, Data: mustMarshal(resp.Actions)})
}

func (s *Server) handleListActionStates(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindListActionStates, Project: req.Project})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "action_states", Project: req.Project, Data: mustMarshal(resp.ActionStates)})
}

func (s *Server) handleDaemonStatus(cl *client) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindDaemonStatus})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "daemon_status", Data: mustMarshal(resp.DaemonInfo)})
}

// --- lifecycle mutations ---

// requestResult proxies a mutation and answers with respType carrying ok /
// error, matching the browser's result-message contract.
func (s *Server) requestResult(cl *client, req protocol.Request, respType string) {
	resp, err := s.call(req)
	switch {
	case err != nil:
		wireError(cl, respType, req.Project, err.Error())
	case resp.Kind == protocol.KindError:
		wireError(cl, respType, req.Project, resp.Error)
	default:
		cl.enqueue(wsResponse{Type: respType, Project: req.Project, Ok: true})
	}
}

func (s *Server) handleStartProject(cl *client, req *wsRequest) {
	configPath := req.ConfigPath
	if configPath == "" && req.Project != "" {
		resp, err := s.call(protocol.Request{Kind: protocol.KindListProjects})
		if err == nil && resp.Kind == protocol.KindProjects {
			for _, p := range resp.Projects {
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
	s.requestResult(cl, protocol.Request{
		Kind:          protocol.KindStartProject,
		ConfigPath:    configPath,
		EnvFile:       req.EnvFile,
		RemoveOrphans: req.RemoveOrphans,
	}, "result")
}

func (s *Server) handleStopProject(cl *client, req *wsRequest) {
	s.requestResult(cl, protocol.Request{Kind: protocol.KindStopProject, Project: req.Project}, "result")
}

func (s *Server) handleServiceOp(cl *client, req *wsRequest, kind protocol.RequestKind, respType string) {
	s.requestResult(cl, protocol.Request{
		Kind:    kind,
		Project: req.Project,
		Service: req.Service,
	}, respType)
}

func (s *Server) handleKillService(cl *client, req *wsRequest) {
	s.requestResult(cl, protocol.Request{
		Kind:    protocol.KindKillService,
		Project: req.Project,
		Service: req.Service,
		Signal:  req.Signal,
	}, "result")
}

func (s *Server) handleRestartDaemon(cl *client) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindRestartDaemon})
	if err != nil {
		fail(cl, err.Error())
		return
	}
	if resp.Kind == protocol.KindError {
		fail(cl, resp.Error)
		return
	}
	cl.enqueue(wsResponse{Type: "done", Ok: true})
}

// --- actions ---

// runAction starts an action in the background. Output lines are drained and
// dropped here; the browser watches the action's output through its own
// subscribe_action_logs stream tailing the same log file.
func (s *Server) runAction(cl *client, req *wsRequest) {
	go func() {
		c, err := control.Dial(s.socketPath)
		if err != nil {
			cl.enqueue(wsResponse{Type: "action_done", Project: req.Project, Action: req.Action, Error: err.Error()})
			return
		}
		defer func() { _ = c.Close() }()
		runDone := make(chan struct{})
		defer close(runDone)
		go func() {
			select {
			case <-cl.ctx.Done():
				_ = c.Close()
			case <-runDone:
			}
		}()
		code, err := c.RunAction(req.Project, req.Action, req.Args, nil)
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
	resp, err := s.call(protocol.Request{Kind: protocol.KindGitLog, Project: req.Project})
	if err != nil {
		s.gitFail(cl, "git_commits", req.Project, err)
		return
	}
	if resp.Kind == protocol.KindError {
		s.gitFail(cl, "git_commits", req.Project, errors.New(resp.Error))
		return
	}
	payload := map[string]any{
		"commits":  resp.GitCommits,
		"branches": resp.GitBranches,
		"tags":     resp.GitTags,
		"stashes":  resp.GitStashes,
	}
	cl.enqueue(wsResponse{Type: "git_commits", Project: req.Project, Ok: true, Data: mustMarshal(payload)})
}

func (s *Server) handleGitDiff(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{
		Kind:         protocol.KindGitDiff,
		Project:      req.Project,
		Hash:         req.Hash,
		Path:         req.Path,
		ContextLines: req.ContextLines,
	})
	if err != nil {
		s.gitFail(cl, "git_diff", req.Project, err)
		return
	}
	if resp.Kind == protocol.KindError {
		s.gitFail(cl, "git_diff", req.Project, errors.New(resp.Error))
		return
	}
	cl.enqueue(wsResponse{Type: "git_diff", Project: req.Project, Ok: true, Data: mustMarshal(resp.GitDiff)})
}

func (s *Server) handleGitCommit(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{Kind: protocol.KindGitCommit, Project: req.Project, Message: req.Message})
	if err != nil {
		s.gitFail(cl, "git_commit_result", req.Project, err)
		return
	}
	if resp.Kind == protocol.KindError {
		s.gitFail(cl, "git_commit_result", req.Project, errors.New(resp.Error))
		return
	}
	cl.enqueue(wsResponse{Type: "git_commit_result", Project: req.Project, Ok: true})
}

func (s *Server) handleGitStage(cl *client, req *wsRequest) {
	resp, err := s.call(protocol.Request{
		Kind:     protocol.KindGitStage,
		Project:  req.Project,
		Path:     req.Path,
		StageAll: req.StageAll,
		Unstage:  req.Unstage,
	})
	if err != nil {
		s.gitFail(cl, "git_stage_result", req.Project, err)
		return
	}
	if resp.Kind == protocol.KindError {
		s.gitFail(cl, "git_stage_result", req.Project, errors.New(resp.Error))
		return
	}
	cl.enqueue(wsResponse{Type: "git_stage_result", Project: req.Project, Ok: true})
}

func (s *Server) handleGitRemote(cl *client, req *wsRequest, kind protocol.RequestKind) {
	resp, err := s.call(protocol.Request{Kind: kind, Project: req.Project})
	if err != nil {
		s.gitFail(cl, "git_remote_result", req.Project, err)
		return
	}
	if resp.Kind == protocol.KindError {
		s.gitFail(cl, "git_remote_result", req.Project, errors.New(resp.Error))
		return
	}
	cl.enqueue(wsResponse{Type: "git_remote_result", Project: req.Project, Ok: true, Line: resp.GitOutput})
}

// --- log streaming ---

// logTarget identifies one log stream: exactly one of service and action is set.
type logTarget struct {
	project string
	service string
	action  string
}

// logSubKey namespaces subscription keys so a service and an action sharing a
// name don't cancel each other's streams.
func logSubKey(t logTarget) string {
	if t.action != "" {
		return "a:" + t.project + "/" + t.action
	}
	return t.project + "/" + t.service
}

// streamLogs opens a dedicated connection to the daemon, follows the target's
// log (or drains it when previous is set), and forwards each frame to the
// browser until the subscription is cancelled or the client disconnects.
func (s *Server) streamLogs(cl *client, subCtx context.Context, t logTarget, previous bool) {
	c, err := control.Dial(s.socketPath)
	if err != nil {
		fail(cl, fmt.Sprintf("web: cannot reach daemon: %v", err))
		return
	}
	defer func() { _ = c.Close() }()

	// Unblock Recv when the subscription is cancelled or the client leaves.
	streamDone := make(chan struct{})
	defer close(streamDone)
	go func() {
		select {
		case <-subCtx.Done():
			_ = c.Close()
		case <-streamDone:
		}
	}()

	err = c.Send(protocol.Request{
		Kind:     protocol.KindLogs,
		Project:  t.project,
		Service:  t.service,
		Action:   t.action,
		Follow:   !previous,
		Previous: previous,
		Tail:     protocol.DefaultLogTail,
	})
	if err != nil {
		fail(cl, err.Error())
		return
	}

	tag := func(resp protocol.Response) wsResponse {
		out := wsResponse{
			Type:    "log_line",
			Project: resp.Project,
			Prev:    previous,
			Line:    resp.Line,
		}
		if t.action != "" {
			// The daemon labels action frames with the target name in Service.
			out.Action = resp.Service
		} else {
			out.Service = resp.Service
		}
		return out
	}

	for {
		resp, err := c.Recv()
		if err != nil {
			return // includes cancellation via conn close
		}
		switch resp.Kind {
		case protocol.KindLogContent:
			if resp.Content == "" {
				continue
			}
			for _, line := range strings.Split(strings.TrimRight(resp.Content, "\n"), "\n") {
				msg := tag(resp)
				msg.Line = line
				cl.enqueue(msg)
			}
		case protocol.KindLogLine:
			cl.enqueue(tag(resp))
		case protocol.KindLogRotated:
			msg := wsResponse{Type: "log_rotated", Project: resp.Project}
			if t.action != "" {
				msg.Action = resp.Service
			} else {
				msg.Service = resp.Service
			}
			cl.enqueue(msg)
		case protocol.KindDone:
			return
		case protocol.KindError:
			fail(cl, resp.Error)
			return
		}
	}
}

// startLogSubscription cancels any existing stream for the key and spawns a
// fresh one bound to subCtx.
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

// projectDir resolves the working directory for a terminal from the project's
// config path. Unknown projects are an error rather than falling back to the
// web process's cwd.
func (s *Server) projectDir(project string) (string, error) {
	if project == "" {
		return "", errors.New("web: project is required to spawn a terminal")
	}
	resp, err := s.call(protocol.Request{Kind: protocol.KindListProjects})
	if err != nil {
		return "", err
	}
	if resp.Kind == protocol.KindError {
		return "", errors.New(resp.Error)
	}
	for _, p := range resp.Projects {
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
