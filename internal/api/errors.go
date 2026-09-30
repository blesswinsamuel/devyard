// Package api implements the DaemonService ConnectRPC handlers on top of the
// engine, the event bus, git tracking and interactive sessions.
package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/gitstate"
	"github.com/blesswinsamuel/devyard/internal/logstore"
	"github.com/blesswinsamuel/devyard/internal/sessions"
)

// toConnect maps typed errors to Connect codes. Clients switch on codes,
// never on messages.
func toConnect(err error) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	code := connect.CodeInternal
	switch {
	case errors.Is(err, engine.ErrNotFound), errors.Is(err, sessions.ErrSessionNotFound), errors.Is(err, logstore.ErrNoRun):
		code = connect.CodeNotFound
	case errors.Is(err, engine.ErrAlreadyExists):
		code = connect.CodeAlreadyExists
	case errors.Is(err, engine.ErrNotRunning), errors.Is(err, engine.ErrAlreadyRunning),
		errors.Is(err, engine.ErrConfig), errors.Is(err, gitstate.ErrBusy):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, engine.ErrInvalid):
		code = connect.CodeInvalidArgument
	case errors.Is(err, engine.ErrShuttingDown):
		code = connect.CodeUnavailable
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return connect.NewError(code, err)
}

func invalid(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}
