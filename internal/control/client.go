package control

import (
	"errors"
	"fmt"
	"net"

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
// build is true, pre-start builds are run before starting services.
func (c *Client) StartProject(configPath string, build bool) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStartProject, ConfigPath: configPath, Build: build}); err != nil {
		return err
	}
	return c.awaitDone()
}

// StopProject sends a StopProject request for the named project and waits for
// the daemon to confirm. The project's services are stopped and the project is
// removed from the daemon's map.
func (c *Client) StopProject(project string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStopProject, Project: project}); err != nil {
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

// Restart sends a Restart request for the given project. If service is empty,
// all services in the project are restarted.
func (c *Client) Restart(project, service string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindRestart, Project: project, Service: service}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Logs sends a Logs request for the given project + service and calls onLine
// for each log line received. If follow is true, it blocks until the daemon
// signals Done (e.g. on shutdown) or the connection drops. If follow is false,
// it returns after the existing log content has been streamed.
func (c *Client) Logs(project, service string, follow bool, onLine func(string)) error {
	if onLine == nil {
		onLine = func(string) {}
	}
	if err := c.Send(protocol.Request{Kind: protocol.KindLogs, Project: project, Service: service, Follow: follow}); err != nil {
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
		case protocol.KindDone:
			return nil
		case protocol.KindError:
			return errors.New(resp.Error)
		default:
			return fmt.Errorf("control: unexpected response %q", resp.Kind)
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
