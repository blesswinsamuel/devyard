package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c" //nolint:staticcheck // h2c is standard for cleartext HTTP/2 on unix sockets

	localcomposev1 "github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1"
	"github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1/localcomposev1connect"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// Server is the Unix-socket control server powered by ConnectRPC.
type Server struct {
	socket  string
	backend MultiBackend

	mu          sync.Mutex
	ln          net.Listener
	httpServer  *http.Server
	closed      bool
	subscribers map[chan *localcomposev1.Event]struct{}

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewServer creates a control server that will listen on socket when
// ListenAndServe is called.
func NewServer(socket string, backend MultiBackend) *Server {
	srv := &Server{
		socket:      socket,
		backend:     backend,
		subscribers: make(map[chan *localcomposev1.Event]struct{}),
		stopCh:      make(chan struct{}),
	}
	if backend != nil {
		backend.SetOnStateChange(func(project string, state *protocol.ServiceState) {
			srv.broadcastEvent(&localcomposev1.Event{
				Event: &localcomposev1.Event_ServiceStateChanged{
					ServiceStateChanged: &localcomposev1.ServiceStateChangedEvent{
						Project: project,
						State:   state,
					},
				},
			})
		})
		backend.SetOnTaskStateChange(func(project string, state *protocol.TaskState) {
			srv.broadcastEvent(&localcomposev1.Event{
				Event: &localcomposev1.Event_TaskStateChanged{
					TaskStateChanged: &localcomposev1.TaskStateChangedEvent{
						Project: project,
						State:   state,
					},
				},
			})
		})
		backend.SetOnGitChange(func(project string) {
			srv.broadcastEvent(&localcomposev1.Event{
				Event: &localcomposev1.Event_GitChanged{
					GitChanged: &localcomposev1.GitChangedEvent{
						Project: project,
					},
				},
			})
		})
		backend.SetOnProjectsChange(func() {
			srv.broadcastEvent(&localcomposev1.Event{
				Event: &localcomposev1.Event_ProjectsChanged{
					ProjectsChanged: &localcomposev1.ProjectsChangedEvent{},
				},
			})
		})
	}
	return srv
}

func (s *Server) broadcastEvent(event *localcomposev1.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subscribers {
		select {
		case sub <- event:
		default:
		}
	}
}

// ListenAndServe starts serving ConnectRPC over HTTP/2 on the Unix socket.
func (s *Server) ListenAndServe() error {
	_ = os.Remove(s.socket)
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("control: listen %s: %w", s.socket, err)
	}

	mux := http.NewServeMux()
	path, handler := localcomposev1connect.NewDaemonServiceHandler(s)
	mux.Handle(path, handler)

	//nolint:staticcheck // h2c is standard for cleartext HTTP/2 on unix sockets
	httpServer := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return errors.New("server is closed")
	}
	s.ln = ln
	s.httpServer = httpServer
	s.mu.Unlock()

	slog.Info("control server listening", "socket", s.socket)

	go func() {
		_ = httpServer.Serve(ln)
	}()
	return nil
}

// Addr returns the Unix socket path.
func (s *Server) Addr() string {
	return s.socket
}

// Socket returns the Unix socket path.
func (s *Server) Socket() string {
	return s.socket
}

// Close closes the server listener and cancels all active streams.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.stopOnce.Do(func() { close(s.stopCh) })

	var err error
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		err = s.httpServer.Shutdown(ctx)
		cancel()
	} else if s.ln != nil {
		err = s.ln.Close()
	}

	for sub := range s.subscribers {
		close(sub)
	}
	s.subscribers = make(map[chan *localcomposev1.Event]struct{})
	s.mu.Unlock()

	_ = os.Remove(s.socket)
	return err
}

// Handler returns the HTTP handler path and handler for mounting on external HTTP servers (e.g. web UI server).
func (s *Server) Handler() (string, http.Handler) {
	return localcomposev1connect.NewDaemonServiceHandler(s)
}

// -----------------------------------------------------------------------------
// DaemonServiceHandler Implementation
// -----------------------------------------------------------------------------

func (s *Server) ListProjects(ctx context.Context, req *connect.Request[localcomposev1.ListProjectsRequest]) (*connect.Response[localcomposev1.ListProjectsResponse], error) {
	projects := s.backend.ListProjects()
	return connect.NewResponse(&localcomposev1.ListProjectsResponse{
		Projects: projects,
	}), nil
}

func (s *Server) StartProject(ctx context.Context, req *connect.Request[localcomposev1.StartProjectRequest]) (*connect.Response[localcomposev1.StartProjectResponse], error) {
	configPath := req.Msg.ConfigPath
	if configPath == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("start_project: config_path is required"))
	}
	removeOrphans := true
	if req.Msg.RemoveOrphans != nil {
		removeOrphans = *req.Msg.RemoveOrphans
	}
	if err := s.backend.StartProject(configPath, req.Msg.Build, req.Msg.EnvFile, removeOrphans); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StartProjectResponse{}), nil
}

func (s *Server) StopProject(ctx context.Context, req *connect.Request[localcomposev1.StopProjectRequest]) (*connect.Response[localcomposev1.StopProjectResponse], error) {
	if err := s.backend.StopProject(req.Msg.Project); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StopProjectResponse{}), nil
}

func (s *Server) RemoveProject(ctx context.Context, req *connect.Request[localcomposev1.RemoveProjectRequest]) (*connect.Response[localcomposev1.RemoveProjectResponse], error) {
	if err := s.backend.RemoveProject(req.Msg.Project); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.RemoveProjectResponse{}), nil
}

func (s *Server) DaemonStatus(ctx context.Context, req *connect.Request[localcomposev1.DaemonStatusRequest]) (*connect.Response[localcomposev1.DaemonStatusResponse], error) {
	info, err := s.backend.DaemonStatus()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.DaemonStatusResponse{
		Info: info,
	}), nil
}

func (s *Server) StopDaemon(ctx context.Context, req *connect.Request[localcomposev1.StopDaemonRequest]) (*connect.Response[localcomposev1.StopDaemonResponse], error) {
	if err := s.backend.StopDaemon(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StopDaemonResponse{}), nil
}

func (s *Server) RestartDaemon(ctx context.Context, req *connect.Request[localcomposev1.RestartDaemonRequest]) (*connect.Response[localcomposev1.RestartDaemonResponse], error) {
	pid, err := s.backend.RestartDaemon(req.Msg.RestartServices)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.RestartDaemonResponse{Pid: pid}), nil
}

func (s *Server) ListServices(ctx context.Context, req *connect.Request[localcomposev1.ListServicesRequest]) (*connect.Response[localcomposev1.ListServicesResponse], error) {
	states, err := s.backend.ListServices(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&localcomposev1.ListServicesResponse{
		States: states,
	}), nil
}

func (s *Server) StartService(ctx context.Context, req *connect.Request[localcomposev1.StartServiceRequest]) (*connect.Response[localcomposev1.StartServiceResponse], error) {
	if req.Msg.Service == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("service is required"))
	}
	if err := s.backend.StartService(req.Msg.Project, req.Msg.Service); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StartServiceResponse{}), nil
}

func (s *Server) StopService(ctx context.Context, req *connect.Request[localcomposev1.StopServiceRequest]) (*connect.Response[localcomposev1.StopServiceResponse], error) {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if req.Msg.Service == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("service is required"))
	}
	if err := b.StopService(req.Msg.Service); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StopServiceResponse{}), nil
}

func (s *Server) KillService(ctx context.Context, req *connect.Request[localcomposev1.KillServiceRequest]) (*connect.Response[localcomposev1.KillServiceResponse], error) {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	sig := req.Msg.Signal
	if sig == "" {
		sig = "SIGKILL"
	}
	if req.Msg.Service == "" {
		for _, st := range b.States() {
			_ = b.KillService(st.Name, sig)
		}
	} else {
		if err := b.KillService(req.Msg.Service, sig); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&localcomposev1.KillServiceResponse{}), nil
}

func (s *Server) Restart(ctx context.Context, req *connect.Request[localcomposev1.RestartRequest]) (*connect.Response[localcomposev1.RestartResponse], error) {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if req.Msg.Service == "" {
		for _, st := range b.States() {
			if err := b.Restart(st.Name); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
		}
	} else {
		if err := b.Restart(req.Msg.Service); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&localcomposev1.RestartResponse{}), nil
}

func (s *Server) Top(ctx context.Context, req *connect.Request[localcomposev1.TopRequest]) (*connect.Response[localcomposev1.TopResponse], error) {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	stats, err := b.Top(req.Msg.Service)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.TopResponse{
		Stats: stats,
	}), nil
}

func (s *Server) ListPorts(ctx context.Context, req *connect.Request[localcomposev1.ListPortsRequest]) (*connect.Response[localcomposev1.ListPortsResponse], error) {
	ports, err := s.backend.ListPorts(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.ListPortsResponse{
		Ports: ports,
	}), nil
}

func (s *Server) ListTasks(ctx context.Context, req *connect.Request[localcomposev1.ListTasksRequest]) (*connect.Response[localcomposev1.ListTasksResponse], error) {
	tasks, err := s.backend.ListTasks(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&localcomposev1.ListTasksResponse{
		Tasks: tasks,
	}), nil
}

func (s *Server) Logs(ctx context.Context, req *connect.Request[localcomposev1.LogsRequest], stream *connect.ServerStream[localcomposev1.LogChunk]) error {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}

	target := req.Msg.Service
	if target == "" {
		target = req.Msg.Task
	}
	if target == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("service or task is required"))
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var path string
	switch {
	case req.Msg.Task != "" && req.Msg.Previous:
		path, err = b.TaskPreviousLogPath(req.Msg.Task)
	case req.Msg.Task != "" && req.Msg.Follow:
		path, err = WaitForTaskLog(waitCtx, b, req.Msg.Task)
	case req.Msg.Task != "":
		path, err = b.TaskLogPath(req.Msg.Task)
	case req.Msg.Previous:
		path, err = b.PreviousLogPath(req.Msg.Service)
	default:
		path, err = b.LogPath(req.Msg.Service)
	}
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if req.Msg.Previous {
		if _, err := os.Stat(path); err != nil {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("logs: no previous run for %q", target))
		}
	}

	return streamLogsConnect(ctx, path, req.Msg.Follow, !req.Msg.Previous, int(req.Msg.Tail), s.stopCh, req.Msg.Project, req.Msg.Service, req.Msg.Task, stream)
}

const followPollInterval = 100 * time.Millisecond

func streamLogsConnect(ctx context.Context, path string, follow, trackRotation bool, tail int, stop <-chan struct{}, project, service, task string, stream *connect.ServerStream[localcomposev1.LogChunk]) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	content, err := ReadLogHistory(f, tail)
	if err != nil {
		return err
	}
	if err := stream.Send(&localcomposev1.LogChunk{
		Project: project,
		Service: service,
		Task:    task,
		Content: string(content),
	}); err != nil {
		return err
	}

	if !follow {
		return nil
	}

	var leftover []byte
	buf := make([]byte, 4096)

	flush := func(chunk []byte) error {
		data := append(leftover, chunk...)
		leftover = leftover[:0]
		var lines []string
		for {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				leftover = append(leftover, data...)
				break
			}
			line := string(data[:i])
			data = data[i+1:]
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			return stream.Send(&localcomposev1.LogChunk{
				Project: project,
				Service: service,
				Task:    task,
				Lines:   lines,
			})
		}
		return nil
	}

	ticker := time.NewTicker(followPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
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
					if err := stream.Send(&localcomposev1.LogChunk{
						Project: project,
						Service: service,
						Task:    task,
						Rotated: true,
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

func WaitForTaskLog(ctx context.Context, b Backend, task string) (string, error) {
	for {
		path, err := b.TaskLogPath(task)
		if err == nil {
			return path, nil
		}
		running := false
		for _, st := range b.ListTasks() {
			if st.Name == task && (st.Status == "running" || st.Status == "starting") {
				running = true
				break
			}
		}
		if !running {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("task %q log did not appear: %w", task, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type taskConnectWriter struct {
	project string
	task    string
	stream  *connect.ServerStream[localcomposev1.TaskOutputChunk]
	buf     bytes.Buffer
	mu      sync.Mutex
}

func (l *taskConnectWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	l.buf.Write(p)
	for {
		line, err := l.buf.ReadString('\n')
		if err != nil {
			l.buf.WriteString(line)
			break
		}
		line = strings.TrimRight(line, "\r\n")
		_ = l.stream.Send(&localcomposev1.TaskOutputChunk{
			Project: l.project,
			Task:    l.task,
			Line:    line,
		})
	}
	return n, nil
}

func (l *taskConnectWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buf.Len() > 0 {
		line := strings.TrimRight(l.buf.String(), "\r\n")
		l.buf.Reset()
		if line != "" {
			_ = l.stream.Send(&localcomposev1.TaskOutputChunk{
				Project: l.project,
				Task:    l.task,
				Line:    line,
			})
		}
	}
}

func (s *Server) RunTask(ctx context.Context, req *connect.Request[localcomposev1.RunTaskRequest], stream *connect.ServerStream[localcomposev1.TaskOutputChunk]) error {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	writer := &taskConnectWriter{
		project: req.Msg.Project,
		task:    req.Msg.Task,
		stream:  stream,
	}
	exitCode, err := b.RunTask(ctx, req.Msg.Task, req.Msg.Args, writer)
	writer.Flush()
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	ec := int32(exitCode)
	return stream.Send(&localcomposev1.TaskOutputChunk{
		Project:  req.Msg.Project,
		Task:     req.Msg.Task,
		ExitCode: &ec,
	})
}

func (s *Server) StopTask(ctx context.Context, req *connect.Request[localcomposev1.StopTaskRequest]) (*connect.Response[localcomposev1.StopTaskResponse], error) {
	b, err := s.backend.ProjectBackend(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if req.Msg.Task == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task is required"))
	}
	if err := b.StopTask(req.Msg.Task); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.StopTaskResponse{}), nil
}

func (s *Server) GitLog(ctx context.Context, req *connect.Request[localcomposev1.GitLogRequest]) (*connect.Response[localcomposev1.GitLogResponse], error) {
	commits, branches, tags, stashes, err := s.backend.GitLog(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitLogResponse{
		Commits:  commits,
		Branches: branches,
		Tags:     tags,
		Stashes:  stashes,
	}), nil
}

func (s *Server) GitDiff(ctx context.Context, req *connect.Request[localcomposev1.GitDiffRequest]) (*connect.Response[localcomposev1.GitDiffResponse], error) {
	diffRes, err := s.backend.GitDiff(req.Msg.Project, req.Msg.Hash, req.Msg.Path, int(req.Msg.ContextLines))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitDiffResponse{
		Result: diffRes,
	}), nil
}

func (s *Server) GitCommit(ctx context.Context, req *connect.Request[localcomposev1.GitCommitRequest]) (*connect.Response[localcomposev1.GitCommitResponse], error) {
	if err := s.backend.GitCommit(req.Msg.Project, req.Msg.Message); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitCommitResponse{}), nil
}

func (s *Server) GitStage(ctx context.Context, req *connect.Request[localcomposev1.GitStageRequest]) (*connect.Response[localcomposev1.GitStageResponse], error) {
	if err := s.backend.GitStage(req.Msg.Project, req.Msg.Path, req.Msg.StageAll, req.Msg.Unstage); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitStageResponse{}), nil
}

func (s *Server) GitPush(ctx context.Context, req *connect.Request[localcomposev1.GitPushRequest]) (*connect.Response[localcomposev1.GitPushResponse], error) {
	output, err := s.backend.GitPush(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitPushResponse{
		Output: output,
	}), nil
}

func (s *Server) GitPull(ctx context.Context, req *connect.Request[localcomposev1.GitPullRequest]) (*connect.Response[localcomposev1.GitPullResponse], error) {
	output, err := s.backend.GitPull(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitPullResponse{
		Output: output,
	}), nil
}

func (s *Server) GitFetch(ctx context.Context, req *connect.Request[localcomposev1.GitFetchRequest]) (*connect.Response[localcomposev1.GitFetchResponse], error) {
	output, err := s.backend.GitFetch(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitFetchResponse{
		Output: output,
	}), nil
}

func (s *Server) GitStatus(ctx context.Context, req *connect.Request[localcomposev1.GitStatusRequest]) (*connect.Response[localcomposev1.GitStatusResponse], error) {
	status, err := s.backend.GitStatus(req.Msg.Project)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&localcomposev1.GitStatusResponse{
		Status: status,
	}), nil
}

func (s *Server) SubscribeEvents(ctx context.Context, req *connect.Request[localcomposev1.SubscribeEventsRequest], stream *connect.ServerStream[localcomposev1.Event]) error {
	// Immediately send a heartbeat event to flush HTTP response headers to the client.
	if err := stream.Send(&localcomposev1.Event{
		Event: &localcomposev1.Event_Heartbeat{
			Heartbeat: &localcomposev1.HeartbeatEvent{},
		},
	}); err != nil {
		return err
	}

	ch := make(chan *localcomposev1.Event, 64)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.stopCh:
			return nil
		case <-ticker.C:
			if err := stream.Send(&localcomposev1.Event{
				Event: &localcomposev1.Event_Heartbeat{
					Heartbeat: &localcomposev1.HeartbeatEvent{},
				},
			}); err != nil {
				return err
			}
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}
