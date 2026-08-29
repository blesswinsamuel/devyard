package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	localcomposev1 "github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1"
	"github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1/localcomposev1connect"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// Client is a typed ConnectRPC client connecting over Unix domain sockets.
type Client struct {
	socket     string
	httpClient *http.Client
	rpcClient  localcomposev1connect.DaemonServiceClient
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
	rpcClient := localcomposev1connect.NewDaemonServiceClient(httpClient, "http://localhost")
	return &Client{
		socket:     socket,
		httpClient: httpClient,
		rpcClient:  rpcClient,
	}, nil
}

// WaitForSocket polls until a Unix socket exists at path or the timeout
// elapses, giving the daemonized child a moment to bind before we dial.
func WaitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
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
func (c *Client) RPCClient() localcomposev1connect.DaemonServiceClient {
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
	resp, err := c.rpcClient.ListServices(context.Background(), connect.NewRequest(&localcomposev1.ListServicesRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.States, nil
}

// ListProjects sends a ListProjects request and returns all known projects.
func (c *Client) ListProjects() ([]*protocol.ProjectInfo, error) {
	resp, err := c.rpcClient.ListProjects(context.Background(), connect.NewRequest(&localcomposev1.ListProjectsRequest{}))
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
	_, err := c.rpcClient.StartProject(context.Background(), connect.NewRequest(&localcomposev1.StartProjectRequest{
		ConfigPath:    configPath,
		Build:         build,
		EnvFile:       envFile,
		RemoveOrphans: ro,
	}))
	return unwrapError(err)
}

// StopProject stops a project's services.
func (c *Client) StopProject(project string) error {
	_, err := c.rpcClient.StopProject(context.Background(), connect.NewRequest(&localcomposev1.StopProjectRequest{
		Project: project,
	}))
	return unwrapError(err)
}

// RemoveProject stops and removes a project from the daemon.
func (c *Client) RemoveProject(project string) error {
	_, err := c.rpcClient.RemoveProject(context.Background(), connect.NewRequest(&localcomposev1.RemoveProjectRequest{
		Project: project,
	}))
	return unwrapError(err)
}

// StopDaemon stops all projects and shuts down the daemon.
func (c *Client) StopDaemon() error {
	_, err := c.rpcClient.StopDaemon(context.Background(), connect.NewRequest(&localcomposev1.StopDaemonRequest{}))
	return unwrapError(err)
}

// RestartDaemon restarts the daemon process. If restartServices is true,
// all managed project services are stopped and restarted as well.
func (c *Client) RestartDaemon(restartServices bool) error {
	_, err := c.rpcClient.RestartDaemon(context.Background(), connect.NewRequest(&localcomposev1.RestartDaemonRequest{
		RestartServices: restartServices,
	}))
	return unwrapError(err)
}

// DaemonStatus returns daemon metrics and status.
func (c *Client) DaemonStatus() (*protocol.DaemonInfo, error) {
	resp, err := c.rpcClient.DaemonStatus(context.Background(), connect.NewRequest(&localcomposev1.DaemonStatusRequest{}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Info, nil
}

// StartService starts one service in a project.
func (c *Client) StartService(project, service string) error {
	_, err := c.rpcClient.StartService(context.Background(), connect.NewRequest(&localcomposev1.StartServiceRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// StopService stops one service in a project.
func (c *Client) StopService(project, service string) error {
	_, err := c.rpcClient.StopService(context.Background(), connect.NewRequest(&localcomposev1.StopServiceRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// KillService signals one service (or all services) with signal.
func (c *Client) KillService(project, service, signal string) error {
	_, err := c.rpcClient.KillService(context.Background(), connect.NewRequest(&localcomposev1.KillServiceRequest{
		Project: project,
		Service: service,
		Signal:  signal,
	}))
	return unwrapError(err)
}

// Restart restarts one service (or all services) in a project.
func (c *Client) Restart(project, service string) error {
	_, err := c.rpcClient.Restart(context.Background(), connect.NewRequest(&localcomposev1.RestartRequest{
		Project: project,
		Service: service,
	}))
	return unwrapError(err)
}

// Top samples CPU and memory usage.
func (c *Client) Top(project, service string) ([]*protocol.ServiceStat, error) {
	resp, err := c.rpcClient.Top(context.Background(), connect.NewRequest(&localcomposev1.TopRequest{
		Project: project,
		Service: service,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Stats, nil
}

// Ports returns listening sockets for a project (or all projects).
func (c *Client) Ports(project string) ([]*protocol.PortBinding, error) {
	resp, err := c.rpcClient.Ports(context.Background(), connect.NewRequest(&localcomposev1.PortsRequest{
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
	return c.streamLogs(ctx, &localcomposev1.LogsRequest{
		Project:  project,
		Service:  service,
		Follow:   follow,
		Previous: previous,
		Tail:     int32(tail),
	}, onLine, onRotate...)
}

// ActionLogs streams logs from an action.
func (c *Client) ActionLogs(project, action string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.ActionLogsCtx(context.Background(), project, action, follow, previous, tail, onLine, onRotate...)
}

// ActionLogsCtx streams action logs with a caller-provided context.
func (c *Client) ActionLogsCtx(ctx context.Context, project, action string, follow, previous bool, tail int, onLine func(string), onRotate ...func()) error {
	return c.streamLogs(ctx, &localcomposev1.LogsRequest{
		Project:  project,
		Action:   action,
		Follow:   follow,
		Previous: previous,
		Tail:     int32(tail),
	}, onLine, onRotate...)
}

func (c *Client) streamLogs(ctx context.Context, req *localcomposev1.LogsRequest, onLine func(string), onRotate ...func()) error {
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

// ListActions returns action definitions for a project.
func (c *Client) ListActions(project string) ([]*protocol.ActionInfo, error) {
	resp, err := c.rpcClient.ListActions(context.Background(), connect.NewRequest(&localcomposev1.ListActionsRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.Actions, nil
}

// ListActionStates returns action runtime states for a project.
func (c *Client) ListActionStates(project string) ([]*protocol.ActionState, error) {
	resp, err := c.rpcClient.ListActionStates(context.Background(), connect.NewRequest(&localcomposev1.ListActionStatesRequest{
		Project: project,
	}))
	if err != nil {
		return nil, unwrapError(err)
	}
	return resp.Msg.States, nil
}

// RunAction executes an action and streams output lines.
func (c *Client) RunAction(ctx context.Context, project, action string, args []string, onLine func(string)) (int, error) {
	stream, err := c.rpcClient.RunAction(ctx, connect.NewRequest(&localcomposev1.RunActionRequest{
		Project: project,
		Action:  action,
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

// GitLog returns the commit log, branches, tags, and stashes for a project.
func (c *Client) GitLog(project string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	resp, err := c.rpcClient.GitLog(context.Background(), connect.NewRequest(&localcomposev1.GitLogRequest{
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
	resp, err := c.rpcClient.GitDiff(context.Background(), connect.NewRequest(&localcomposev1.GitDiffRequest{
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
	_, err := c.rpcClient.GitCommit(context.Background(), connect.NewRequest(&localcomposev1.GitCommitRequest{
		Project: project,
		Message: message,
	}))
	return unwrapError(err)
}

// GitStage stages or unstages files.
func (c *Client) GitStage(project, path string, stageAll, unstage bool) error {
	_, err := c.rpcClient.GitStage(context.Background(), connect.NewRequest(&localcomposev1.GitStageRequest{
		Project:  project,
		Path:     path,
		StageAll: stageAll,
		Unstage:  unstage,
	}))
	return unwrapError(err)
}

// GitPush pushes the current branch to its upstream remote.
func (c *Client) GitPush(project string) (string, error) {
	resp, err := c.rpcClient.GitPush(context.Background(), connect.NewRequest(&localcomposev1.GitPushRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// GitPull pulls changes from the upstream remote.
func (c *Client) GitPull(project string) (string, error) {
	resp, err := c.rpcClient.GitPull(context.Background(), connect.NewRequest(&localcomposev1.GitPullRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// GitFetch fetches remote refs.
func (c *Client) GitFetch(project string) (string, error) {
	resp, err := c.rpcClient.GitFetch(context.Background(), connect.NewRequest(&localcomposev1.GitFetchRequest{
		Project: project,
	}))
	if err != nil {
		return "", unwrapError(err)
	}
	return resp.Msg.Output, nil
}

// SubscribeEvents streams real-time state and git event changes.
func (c *Client) SubscribeEvents(ctx context.Context, onEvent func(*protocol.Event)) error {
	stream, err := c.rpcClient.SubscribeEvents(ctx, connect.NewRequest(&localcomposev1.SubscribeEventsRequest{}))
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
