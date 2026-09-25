package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	devyardv1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
	"github.com/blesswinsamuel/devyard/internal/protocol"
)

// Client is a typed ConnectRPC client connecting over Unix domain sockets.
type Client struct {
	socket     string
	httpClient *http.Client
	rpcClient  devyardv1connect.DaemonServiceClient
}

// Dial connects to the supervisor's control socket over HTTP/2.
func Dial(socket string) (*Client, error) {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	httpClient := &http.Client{
		Transport: transport,
	}
	rpcClient := devyardv1connect.NewDaemonServiceClient(httpClient, "http://localhost")
	return &Client{
		socket:     socket,
		httpClient: httpClient,
		rpcClient:  rpcClient,
	}, nil
}

// WaitForSocket polls until a live daemon is serving the Unix socket at path
// or the timeout elapses, giving the daemonized child a moment to bind before
// we dial. It dials the socket rather than stat'ing the file: a socket file
// can outlive its daemon (a crash leaves the file behind, and during a
// restart the old daemon's socket lingers until it exits).
func WaitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		client, err := Dial(path)
		if err == nil {
			_, err := client.DaemonStatus()
			_ = client.Close()
			if err == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("control: timeout waiting for socket %s", path)
}

// Close closes idle connections.
func (c *Client) Close() error {
	if c == nil || c.httpClient == nil {
		return nil
	}
	c.httpClient.CloseIdleConnections()
	return nil
}

// RPCClient returns the underlying ConnectRPC DaemonServiceClient.
func (c *Client) RPCClient() devyardv1connect.DaemonServiceClient {
	return c.rpcClient
}

func unwrapError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return errors.New(connectErr.Message())
	}
	return err
}

// List sends a ListServices request for the given project.
func (c *Client) List(project string) ([]*protocol.ServiceState, error) {
	resp, err := c.rpcClient.ListServices(context.Background(), connect.NewRequest(&devyardv1.ListServicesRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.States, nil
}

// ListProjects sends a ListProjects request and returns all known projects.
func (c *Client) ListProjects() ([]*protocol.ProjectInfo, error) {
	resp, err := c.rpcClient.ListProjects(context.Background(), connect.NewRequest(&devyardv1.ListProjectsRequest{}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Projects, nil
}

// StartProject starts a project from configPath.
func (c *Client) StartProject(configPath string, build bool, envFile string, removeOrphans ...bool) error {
	var ro *bool
	if len(removeOrphans) > 0 {
		ro = &removeOrphans[0]
	}
	_, err := c.rpcClient.StartProject(context.Background(), connect.NewRequest(&devyardv1.StartProjectRequest{
		ConfigPath:    configPath,
		Build:         build,
		EnvFile:       envFile,
		RemoveOrphans: ro,
	}))
	return unwrapError(err)
}

// StopProject stops a project's services.
func (c *Client) StopProject(project string) error {
	_, err := c.rpcClient.StopProject(context.Background(), connect.NewRequest(&devyardv1.StopProjectRequest{
		Project: project,
	}))
	return unwrapError(err)
}

// RemoveProject stops and removes a project from the daemon.
func (c *Client) RemoveProject(project string) error {
	_, err := c.rpcClient.RemoveProject(context.Background(), connect.NewRequest(&devyardv1.RemoveProjectRequest{
		Project: project,
	}))
	return unwrapError(err)
}

// StopDaemon stops all projects and shuts down the daemon.
func (c *Client) StopDaemon() error {
	_, err := c.rpcClient.StopDaemon(context.Background(), connect.NewRequest(&devyardv1.StopDaemonRequest{}))
	return unwrapError(err)
}

// RestartDaemon restarts the daemon process. If restartServices is true,
// all managed project services are stopped and restarted as well. Returns the
// replacement daemon's pid (0 when unknown).
func (c *Client) RestartDaemon(restartServices bool) (int32, error) {
	resp, err := c.rpcClient.RestartDaemon(context.Background(), connect.NewRequest(&devyardv1.RestartDaemonRequest{
		RestartServices: restartServices,
	}))
	if err != nil {
		return 0, unwrapError(err)
	}
	return resp.Msg.Pid, nil
}

// DaemonStatus returns daemon metrics and status.
func (c *Client) DaemonStatus() (*protocol.DaemonInfo, error) {
	resp, err := c.rpcClient.DaemonStatus(context.Background(), connect.NewRequest(&devyardv1.DaemonStatusRequest{}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Info, nil
}

// GetGlobalConfig returns the user-level global config with defaults filled in.
func (c *Client) GetGlobalConfig() (*protocol.GlobalConfig, error) {
	resp, err := c.rpcClient.GetGlobalConfig(context.Background(), connect.NewRequest(&devyardv1.GetGlobalConfigRequest{}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Config, nil
}

// UpdateGlobalConfig validates and persists the global config.
func (c *Client) UpdateGlobalConfig(cfg *protocol.GlobalConfig) error {
	_, err := c.rpcClient.UpdateGlobalConfig(context.Background(), connect.NewRequest(&devyardv1.UpdateGlobalConfigRequest{
		Config: cfg,
	}))
	return unwrapError(err)
}

// StartService starts one service in a project.
func (c *Client) StartService(project, service string) error {
	_, err := c.rpcClient.StartService(context.Background(), connect.NewRequest(&devyardv1.StartServiceRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// StopService stops one service in a project.
func (c *Client) StopService(project, service string) error {
	_, err := c.rpcClient.StopService(context.Background(), connect.NewRequest(&devyardv1.StopServiceRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// KillService signals one service (or all services) with signal.
func (c *Client) KillService(project, service, signal string) error {
	_, err := c.rpcClient.KillService(context.Background(), connect.NewRequest(&devyardv1.KillServiceRequest{
		Project: project,
		Service: service,
		Signal:  signal,
	}))
	return unwrapError(err)
}

// Restart restarts one service (or all services) in a project.
func (c *Client) Restart(project, service string) error {
	_, err := c.rpcClient.Restart(context.Background(), connect.NewRequest(&devyardv1.RestartRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// Top samples CPU and memory usage.
func (c *Client) Top(project, service string) ([]*protocol.ServiceStat, error) {
	resp, err := c.rpcClient.Top(context.Background(), connect.NewRequest(&devyardv1.TopRequest{
		Project: project,
		Service: service,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Stats, nil
}

// ListPorts returns listening sockets for a project (or all projects).
func (c *Client) ListPorts(project string) ([]*protocol.PortBinding, error) {
	resp, err := c.rpcClient.ListPorts(context.Background(), connect.NewRequest(&devyardv1.ListPortsRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Ports, nil
}

// Logs streams logs from a service.
func (c *Client) Logs(project, service string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.LogsCtx(context.Background(), project, service, follow, previous, tail, onLine, onRotate...)
}

// LogsCtx streams logs with a caller-provided context.
func (c *Client) LogsCtx(ctx context.Context, project, service string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.streamLogs(ctx, &devyardv1.LogsRequest{
		Project:  project,
		Service:  service,
		Follow:   follow,
		Previous: previous,
		Tail:     int32(tail),
	}, onLine, onRotate...)
}

// TaskLogs streams logs from a task.
func (c *Client) TaskLogs(project, task string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.TaskLogsCtx(context.Background(), project, task, follow, previous, tail, onLine, onRotate...)
}

// TaskLogsCtx streams task logs with a caller-provided context.
func (c *Client) TaskLogsCtx(ctx context.Context, project, task string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.streamLogs(ctx, &devyardv1.LogsRequest{
		Project:  project,
		Task:     task,
		Follow:   follow,
		Previous: previous,
		Tail:     int32(tail),
	}, onLine, onRotate...)
}

func (c *Client) streamLogs(ctx context.Context, req *devyardv1.LogsRequest, onLine func(string), onRotate ...func()) error {
	stream, err := c.rpcClient.Logs(ctx, connect.NewRequest(req))
	if err != nil {
		return unwrapError(err)
	}
	defer func() { _ = stream.Close() }()

	for stream.Receive() {
		chunk := stream.Msg()
		if chunk.Rotated && len(onRotate) > 0 && onRotate[0] != nil {
			onRotate[0]()
		}
		if chunk.Content != "" {
			lines := strings.Split(strings.TrimSuffix(chunk.Content, "\n"), "\n")
			for _, line := range lines {
				if line != "" && onLine != nil {
					onLine(line)
				}
			}
		}
		for _, line := range chunk.Lines {
			if onLine != nil {
				onLine(line)
			}
		}
	}
	return unwrapError(stream.Err())
}

// ListTasks returns tasks with metadata and runtime states for a project.
func (c *Client) ListTasks(project string) ([]*protocol.TaskState, error) {
	resp, err := c.rpcClient.ListTasks(context.Background(), connect.NewRequest(&devyardv1.ListTasksRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Tasks, nil
}

// RunTask executes a task and streams output lines.
func (c *Client) RunTask(ctx context.Context, project, task string, args []string, onLine func(string)) (int, error) {
	stream, err := c.rpcClient.RunTask(ctx, connect.NewRequest(&devyardv1.RunTaskRequest{
		Project: project,
		Task:    task,
		Args:    args,
	}))
	if err != nil {
		return 1, unwrapError(err)
	}
	defer func() { _ = stream.Close() }()

	exitCode := 0
	for stream.Receive() {
		chunk := stream.Msg()
		if chunk.Line != "" && onLine != nil {
			onLine(chunk.Line)
		}
		if chunk.ExitCode != nil {
			exitCode = int(*chunk.ExitCode)
		}
	}
	if err := stream.Err(); err != nil {
		return exitCode, unwrapError(err)
	}
	return exitCode, nil
}

// StopTask stops a running task in a project.
func (c *Client) StopTask(project, task string) error {
	_, err := c.rpcClient.StopTask(context.Background(), connect.NewRequest(&devyardv1.StopTaskRequest{
		Project: project,
		Task:    task,
	}))
	return unwrapError(err)
}

// GitLog returns the commit log, branches, tags, and stashes for a project.
func (c *Client) GitLog(project string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	resp, err := c.rpcClient.GitLog(context.Background(), connect.NewRequest(&devyardv1.GitLogRequest{
		Project: project,
	}))
	if err != nil {
		return nil, nil, nil, nil, unwrapError(err)
	}
	return resp.Msg.Commits, resp.Msg.Branches, resp.Msg.Tags, resp.Msg.Stashes, nil
}

// GitDiff returns diff metadata for a commit.
func (c *Client) GitDiff(project, hash, path string, contextLines ...int) (*protocol.GitDiffResult, error) {
	var ctxLines int32 = 3
	if len(contextLines) > 0 && contextLines[0] > 0 {
		ctxLines = int32(contextLines[0])
	}
	resp, err := c.rpcClient.GitDiff(context.Background(), connect.NewRequest(&devyardv1.GitDiffRequest{
		Project:      project,
		Hash:         hash,
		Path:         path,
		ContextLines: ctxLines,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Result, nil
}

// GitCommit creates a new git commit.
func (c *Client) GitCommit(project, message string) error {
	_, err := c.rpcClient.GitCommit(context.Background(), connect.NewRequest(&devyardv1.GitCommitRequest{
		Project: project,
		Message: message,
	}))
	return unwrapError(err)
}

// GitStage stages or unstages files.
func (c *Client) GitStage(project, path string, stageAll, unstage bool) error {
	_, err := c.rpcClient.GitStage(context.Background(), connect.NewRequest(&devyardv1.GitStageRequest{
		Project:  project,
		Path:     path,
		StageAll: stageAll,
		Unstage:  unstage,
	}))
	return unwrapError(err)
}

// GitPush pushes the current branch to its upstream remote.
func (c *Client) GitPush(project string) (string, error) {
	resp, err := c.rpcClient.GitPush(context.Background(), connect.NewRequest(&devyardv1.GitPushRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// GitPull pulls changes from the upstream remote.
func (c *Client) GitPull(project string) (string, error) {
	resp, err := c.rpcClient.GitPull(context.Background(), connect.NewRequest(&devyardv1.GitPullRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// GitFetch fetches remote refs.
func (c *Client) GitFetch(project string) (string, error) {
	resp, err := c.rpcClient.GitFetch(context.Background(), connect.NewRequest(&devyardv1.GitFetchRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// GitStatus gets the working tree and branch status summary.
func (c *Client) GitStatus(project string) (*protocol.GitStatus, error) {
	resp, err := c.rpcClient.GitStatus(context.Background(), connect.NewRequest(&devyardv1.GitStatusRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Status, nil
}

// SubscribeEvents streams real-time state and git event changes.
func (c *Client) SubscribeEvents(ctx context.Context, onEvent func(*protocol.Event)) error {
	stream, err := c.rpcClient.SubscribeEvents(ctx, connect.NewRequest(&devyardv1.SubscribeEventsRequest{}))
	if err != nil {
		return unwrapError(err)
	}
	defer func() { _ = stream.Close() }()

	for stream.Receive() {
		if onEvent != nil {
			onEvent(stream.Msg())
		}
	}
	return unwrapError(stream.Err())
}
