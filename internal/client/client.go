// Package client connects to the daemon's control socket.
package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

// Client is a DaemonService client over the daemon's Unix socket.
type Client struct {
	devyardv1connect.DaemonServiceClient
	http *http.Client
}

// Dial returns a client for the socket at path. It does not connect until
// the first call.
func Dial(socket string) *Client {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{
		Protocols: protocols,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	hc := &http.Client{Transport: transport}
	return &Client{
		DaemonServiceClient: devyardv1connect.NewDaemonServiceClient(hc, "http://devyard.sock"),
		http:                hc,
	}
}

// Close releases idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// Ping reports the daemon's info, failing fast when nothing answers.
func (c *Client) Ping(ctx context.Context, timeout time.Duration) (*pb.DaemonInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.GetDaemon(ctx, connect.NewRequest(&pb.GetDaemonRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Info, nil
}

// Code returns the Connect code of err (CodeUnknown for other errors).
func Code(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return connect.CodeUnknown
}

// Message returns the server's message for a Connect error, or err's text.
func Message(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}
