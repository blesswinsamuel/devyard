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

// List sends a List request and returns the service snapshot.
func (c *Client) List() ([]protocol.ServiceState, error) {
	if err := c.Send(protocol.Request{Kind: protocol.KindList}); err != nil {
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

// Stop sends a Stop request (down) and waits for the supervisor to confirm.
func (c *Client) Stop() error {
	if err := c.Send(protocol.Request{Kind: protocol.KindStop}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Restart sends a Restart request. If service is empty, all services are
// restarted.
func (c *Client) Restart(service string) error {
	if err := c.Send(protocol.Request{Kind: protocol.KindRestart, Service: service}); err != nil {
		return err
	}
	return c.awaitDone()
}

// Logs sends a Logs request and calls onLine for each log line received. If
// follow is true, it blocks until the supervisor signals Done (e.g. on
// shutdown) or the connection drops. If follow is false, it returns after the
// existing log content has been streamed.
func (c *Client) Logs(service string, follow bool, onLine func(string)) error {
	if onLine == nil {
		onLine = func(string) {}
	}
	if err := c.Send(protocol.Request{Kind: protocol.KindLogs, Service: service, Follow: follow}); err != nil {
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
