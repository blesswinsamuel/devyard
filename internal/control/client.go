package control

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// Client is a thin control-protocol client. One Client owns one Unix-socket
// connection; callers Dial, send a request, read streaming responses, then
// Close. Methods are not safe for concurrent use on the same Client — a
// control connection serves one request at a time.
type Client struct {
	conn net.Conn
}

// Dial connects to the supervisor's control socket.
func Dial(socket string) (*Client, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("control: dial %s: %w", socket, err)
	}
	return &Client{conn: conn}, nil
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

// Close closes the connection. It is safe to call after a prior error.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Conn returns the underlying connection for callers that need to set
// deadlines or do raw I/O (e.g. the web UI's WebSocket bridge).
func (c *Client) Conn() net.Conn { return c.conn }

// Send writes a request frame.
func (c *Client) Send(req protocol.Request) error {
	return protocol.WriteFrame(c.conn, req)
}

// Recv reads one response frame.
func (c *Client) Recv() (protocol.Response, error) {
	var resp protocol.Response
	if err := protocol.ReadFrame(c.conn, &resp); err != nil {
		return protocol.Response{}, err
	}
	return resp, nil
}

// List sends a List request for the given project and returns the service
// snapshot. An empty project is valid for single-project servers.
func (c *Client) List(project string) ([]protocol.ServiceState, error) {
	if err := c.Send(protocol.Request{Kind: protocol.KindList, Project: project}); err != nil {
		return nil, err
	}
	resp, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if resp.Kind == protocol.KindError {
		return nil, errors.New(resp.Error)
	}
	if resp.Kind != protocol.KindStates {
		return nil, fmt.Errorf("control: unexpected response %q, want %q", resp.Kind, protocol.KindStates)
	}
	return resp.States, nil
}

// ListProjects sends a ListProjects request and returns the project snapshot.
func (c *Client) ListProjects() ([]protocol.ProjectInfo, error) {
	if err := c.Send(protocol.Request{Kind: protocol.KindListProjects}); err != nil {
		return nil, err
	}
	resp, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if resp.Kind == protocol.KindError {
		return nil, errors.New(resp.Error)
	}
	if resp.Kind != protocol.KindProjects {
		return nil, fmt.Errorf("control: unexpected response %q, want %q", resp.Kind, protocol.KindProjects)
	}
	return resp.Projects, nil
}

// StartProject sends a StartProject request with the given config path. If
// build is true, pre-start builds are run before starting services. envFile is
// the absolute path to an env file (empty falls back to .env next to the
// config file). removeOrphans controls orphan service cleanup.
func (c *Client) StartProject(configPath string, build bool, envFile string, removeOrphans bool) error {
	if err := c.Send(protocol.Request{
		Kind:          protocol.KindStartProject,
		ConfigPath:    configPath,
		Build:         build,
		EnvFile:       envFile,
		RemoveOrphans: &removeOrphans,
	}); err != nil {
		return err
	}
	return c.awaitDone()
}

// StopProject sends a StopProject request for the named project and waits for
// the daemon to confirm. The project's services are stopped but the project remains
// in the daemon's list.
func (c *Client) StopProject(project string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStopProject, Project: project}); err != nil {
		return err
	}
	return c.awaitDone()
}

// RemoveProject sends a RemoveProject request for the named project and waits for
// the daemon to confirm. The project is stopped and removed from the daemon.
func (c *Client) RemoveProject(project string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindRemoveProject, Project: project}); err != nil {
		return err
	}
	return c.awaitDone()
}

// StopDaemon sends a StopDaemon request, stopping all projects and shutting
// down the daemon process.
func (c *Client) StopDaemon() error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStopDaemon}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Stop sends a Stop request for the given project (stops all services in that
// project) and waits for confirmation.
func (c *Client) Stop(project string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStop, Project: project}); err != nil {
		return err
	}
	return c.awaitDone()
}

// StopService sends a StopService request for one service in the given project
// and waits for confirmation. The service is stopped in place and not restarted.
func (c *Client) StopService(project, service string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStopService, Project: project, Service: service}); err != nil {
		return err
	}
	return c.awaitDone()
}

// StartService sends a StartService request for one service in the given
// project and waits for confirmation. When the project is running the service
// is resumed in place; when the project is stopped a supervisor is lazily
// materialized that starts just the requested service and its depends_on chain.
func (c *Client) StartService(project, service string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStartService, Project: project, Service: service}); err != nil {
		return err
	}
	return c.awaitDone()
}

// KillService sends a KillService request for one service in the given project
// and waits for confirmation. The service is immediately signalled with the
// given signal name (empty means SIGKILL).
func (c *Client) KillService(project, service, signal string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindKillService, Project: project, Service: service, Signal: signal}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Restart sends a Restart request for the given project. If service is empty,
// all services in the project are restarted.
func (c *Client) Restart(project, service string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindRestart, Project: project, Service: service}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Top sends a Top request for the given project (optionally a single service)
// and returns the CPU/memory snapshot. The daemon samples the service process
// groups over a short interval, so the call blocks for roughly a second.
func (c *Client) Top(project, service string) ([]protocol.ServiceStat, error) {
	if err := c.Send(protocol.Request{Kind: protocol.KindTop, Project: project, Service: service}); err != nil {
		return nil, err
	}
	resp, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if resp.Kind == protocol.KindError {
		return nil, errors.New(resp.Error)
	}
	if resp.Kind != protocol.KindStats {
		return nil, fmt.Errorf("control: unexpected response %q, want %q", resp.Kind, protocol.KindStats)
	}
	return resp.Stats, nil
}

// Logs sends a Logs request for the given project + service and calls onLine
// for each log line received. If previous is true, the immediately preceding
// run's log is streamed instead of the current one. If follow is true, it
// blocks until the daemon signals Done (e.g. on shutdown) or the connection
// drops. If follow is false, it returns after the existing log content has
// been streamed. tail limits history to the last N lines (0 = all, subject to
// the server's byte cap).
func (c *Client) Logs(project, service string, follow, previous bool, tail int, onLine func(string)) error {
	if onLine == nil {
		onLine = func(string) {}
	}
	if err := c.Send(protocol.Request{
		Kind:     protocol.KindLogs,
		Project:  project,
		Service:  service,
		Follow:   follow,
		Previous: previous,
		Tail:     tail,
	}); err != nil {
		return err
	}
	for {
		resp, err := c.Recv()
		if err != nil {
			return err
		}
		switch resp.Kind {
		case protocol.KindLogLine:
			onLine(resp.Line)
		case protocol.KindLogContent:
			for _, line := range strings.Split(strings.TrimRight(resp.Content, "\n"), "\n") {
				onLine(line)
			}
		case protocol.KindLogRotated:
			// A new run started; the terminal keeps appending the fresh run's
			// lines transparently (kubectl-style), so the marker is skipped.
		case protocol.KindDone:
			return nil
		case protocol.KindError:
			return errors.New(resp.Error)
		default:
			return fmt.Errorf("control: unexpected response %q", resp.Kind)
		}
	}
}

// ActionLogs sends a Logs request for the given project + action and calls onLine for each log line.
func (c *Client) ActionLogs(project, action string, follow, previous bool, tail int, onLine func(string)) error {
	if onLine == nil {
		onLine = func(string) {}
	}
	if err := c.Send(protocol.Request{
		Kind:     protocol.KindLogs,
		Project:  project,
		Action:   action,
		Follow:   follow,
		Previous: previous,
		Tail:     tail,
	}); err != nil {
		return err
	}
	for {
		resp, err := c.Recv()
		if err != nil {
			return err
		}
		switch resp.Kind {
		case protocol.KindLogLine:
			onLine(resp.Line)
		case protocol.KindLogContent:
			for _, line := range strings.Split(strings.TrimRight(resp.Content, "\n"), "\n") {
				onLine(line)
			}
		case protocol.KindLogRotated:
		case protocol.KindDone:
			return nil
		case protocol.KindError:
			return errors.New(resp.Error)
		default:
			return fmt.Errorf("control: unexpected response %q", resp.Kind)
		}
	}
}

// ListActions sends a ListActions request and returns the actions defined in the project.
func (c *Client) ListActions(project string) ([]protocol.ActionInfo, error) {
	if err := c.Send(protocol.Request{Kind: protocol.KindListActions, Project: project}); err != nil {
		return nil, err
	}
	resp, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if resp.Kind == protocol.KindError {
		return nil, errors.New(resp.Error)
	}
	if resp.Kind != protocol.KindActions {
		return nil, fmt.Errorf("control: unexpected response %q, want %q", resp.Kind, protocol.KindActions)
	}
	return resp.Actions, nil
}

// RunAction sends a RunAction request for project + action, streaming output lines to onLine.
// Returns the exit code of the action process.
func (c *Client) RunAction(project, action string, args []string, onLine func(string)) (int, error) {
	if onLine == nil {
		onLine = func(string) {}
	}
	if err := c.Send(protocol.Request{
		Kind:    protocol.KindRunAction,
		Project: project,
		Action:  action,
		Args:    args,
	}); err != nil {
		return 1, err
	}
	for {
		resp, err := c.Recv()
		if err != nil {
			return 1, err
		}
		switch resp.Kind {
		case protocol.KindLogLine:
			onLine(resp.Line)
		case protocol.KindDone:
			if resp.ActionExitCode != nil {
				return *resp.ActionExitCode, nil
			}
			return 0, nil
		case protocol.KindError:
			return 1, errors.New(resp.Error)
		default:
			return 1, fmt.Errorf("control: unexpected response %q", resp.Kind)
		}
	}
}

// awaitDone reads frames until a KindDone (success) or KindError arrives.
func (c *Client) awaitDone() error {
	for {
		resp, err := c.Recv()
		if err != nil {
			return err
		}
		switch resp.Kind {
		case protocol.KindDone:
			return nil
		case protocol.KindError:
			return errors.New(resp.Error)
		default:
			return fmt.Errorf("control: unexpected response %q", resp.Kind)
		}
	}
}
