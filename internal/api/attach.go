package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/runner"
	"github.com/blesswinsamuel/devyard/internal/sessions"
)

// Attach bridges a bidirectional stream (the CLI, over HTTP/2) to an
// interactive session. Browsers use the equivalent websocket endpoint.
func (s *Server) Attach(ctx context.Context, stream *connect.BidiStream[pb.AttachRequest, pb.AttachResponse]) error {
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	if open == nil || open.Target == nil {
		return invalid("first message must be open")
	}
	t := open.Target
	sess, err := s.Sessions.Open(ctx, sessions.Target{Kind: t.Kind, Project: t.Project, Name: t.Name, SessionID: t.SessionId}, int(open.Cols), int(open.Rows))
	if err != nil {
		return toConnect(err)
	}
	defer func() { _ = sess.Detach() }()
	if err := stream.Send(&pb.AttachResponse{Msg: &pb.AttachResponse_Ready{Ready: &pb.AttachReady{SessionId: sess.ID, Tty: sess.TTY}}}); err != nil {
		return err
	}

	inputDone := make(chan error, 1) // buffered: the reader may outlive us
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				// The client went away: detach so Read below returns.
				_ = sess.Detach()
				inputDone <- err
				return
			}
			switch m := msg.Msg.(type) {
			case *pb.AttachRequest_Input:
				_ = sess.Write(m.Input)
			case *pb.AttachRequest_Resize:
				_ = sess.Resize(int(m.Resize.Cols), int(m.Resize.Rows))
			case *pb.AttachRequest_CloseStdin:
				_ = sess.CloseStdin()
			case *pb.AttachRequest_Close:
				_ = sess.Kill(context.WithoutCancel(ctx))
			}
		}
	}()

	for {
		out, err := sess.Read()
		if err != nil {
			var exit *runner.ExitError
			if errors.As(err, &exit) {
				return stream.Send(&pb.AttachResponse{Msg: &pb.AttachResponse_Exit{Exit: &pb.AttachExit{
					ExitCode: int32(exit.Status.ExitCode),
					Message:  exitMessage(exit.Status),
				}}})
			}
			return nil
		}
		if err := stream.Send(&pb.AttachResponse{Msg: &pb.AttachResponse_Output{Output: out}}); err != nil {
			return err
		}
	}
}

func exitMessage(st runner.Status) string {
	switch {
	case st.Stopped:
		return "stopped"
	case st.Signal != "":
		return "killed by " + st.Signal
	case st.Lost:
		return "runner lost"
	case st.Error != "":
		return st.Error
	}
	return ""
}
