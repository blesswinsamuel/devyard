package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/devyard/internal/client"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

func newRunCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <task> [args...]",
		Short: "Run a task in the foreground (interactive when attached to a terminal)",
		Long: "Runs a task defined in devyard.yml. The task runs in the daemon (it keeps running\n" +
			"if this command is interrupted by a lost connection); keystrokes are forwarded to it\n" +
			"and Ctrl-C stops it. The exit code is the task's exit code.",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.ensureDaemon(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.ensureRegistered(ctx, cl)
			if err != nil {
				return err
			}
			if _, err := cl.RunTask(ctx, connect.NewRequest(&pb.RunTaskRequest{Project: id, Task: args[0], Args: args[1:]})); err != nil {
				return err
			}
			code, err := c.attachTask(ctx, cl, id, args[0], true)
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitCodeError{Code: code}
			}
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// ensureRegistered makes sure the project exists in the daemon, registering
// the local config (without starting services) when needed.
func (c *Context) ensureRegistered(ctx context.Context, cl *client.Client) (string, error) {
	if c.Project != "" {
		return c.projectID(ctx, cl)
	}
	lc, err := c.loadLocalConfig()
	if err != nil {
		return "", err
	}
	if p, err := findProject(ctx, cl, lc.File.ID()); err == nil && p.ConfigPath == lc.Path {
		return p.Id, nil
	}
	resp, err := cl.AddProject(ctx, connect.NewRequest(&pb.AddProjectRequest{ConfigPath: lc.Path, EnvFile: lc.EnvFile, Env: captureEnv()}))
	if err != nil {
		return "", err
	}
	return resp.Msg.Project.GetId(), nil
}

func newAttachCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <service|task>",
		Short: "Attach to a running service or task (Ctrl-] to detach)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			snap, err := state(ctx, cl)
			if err != nil {
				return err
			}
			kind := ""
			for _, s := range snap.Services {
				if s.Project == id && s.Name == args[0] {
					kind = "service"
				}
			}
			for _, t := range snap.Tasks {
				if t.Project == id && t.Name == args[0] {
					kind = "task"
				}
			}
			if kind == "" {
				return errors.New("no service or task named " + args[0])
			}
			if kind == "task" {
				_, err := c.attachTask(ctx, cl, id, args[0], false)
				return err
			}
			_, err = c.attach(ctx, cl, &pb.AttachTarget{Kind: kind, Project: id, Name: args[0]}, false)
			return err
		},
	}
}

// attachTask attaches to the task's current run (waiting while it waits
// for dependencies) and returns its exit code.
func (c *Context) attachTask(ctx context.Context, cl *client.Client, project, task string, stopOnInterrupt bool) (int, error) {
	for {
		code, err := c.attach(ctx, cl, &pb.AttachTarget{Kind: "task", Project: project, Name: task}, stopOnInterrupt)
		if err == nil {
			return code, nil
		}
		if errors.Is(err, errEndedWithoutStatus) {
			return c.waitTaskExit(ctx, cl, project, task)
		}
		if client.Code(err) != connect.CodeFailedPrecondition {
			return 0, err
		}
		// Not running (yet, or anymore): decide from the task state.
		t, serr := findTask(ctx, cl, project, task)
		if serr != nil {
			return 0, serr
		}
		switch t.Status {
		case "waiting", "running":
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		// Finished before we could attach: print its output instead.
		if err := c.streamLogs(ctx, cl, project, sourcesFor("task", []string{task}), 0, false, 0); err != nil {
			return 0, err
		}
		if t.Status == "failed" && t.Message != "" {
			c.Errorf("devyard: task %s failed: %s\n", task, t.Message)
			return 1, nil
		}
		return int(t.ExitCode), nil
	}
}

// errEndedWithoutStatus is returned by attach when the stream ends without
// an exit message (e.g. the daemon restarted mid-session).
var errEndedWithoutStatus = errors.New("session ended without an exit status")

// waitTaskExit waits for the task's recorded exit.
func (c *Context) waitTaskExit(ctx context.Context, cl *client.Client, project, task string) (int, error) {
	for {
		t, err := findTask(ctx, cl, project, task)
		if err != nil {
			return 0, err
		}
		if t.Status == "exited" || t.Status == "failed" {
			return int(t.ExitCode), nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func findTask(ctx context.Context, cl *client.Client, project, name string) (*pb.Task, error) {
	snap, err := state(ctx, cl)
	if err != nil {
		return nil, err
	}
	for _, t := range snap.Tasks {
		if t.Project == project && t.Name == name {
			return t, nil
		}
	}
	return nil, errors.New("task " + name + " not found")
}

// attach runs an interactive session. With a terminal on stdin, it switches
// to raw mode and forwards keystrokes and window size; otherwise stdin is
// streamed and closed at EOF. Ctrl-] detaches. When stopOnInterrupt is set
// and stdin is not a terminal, SIGINT stops the task.
func (c *Context) attach(ctx context.Context, cl *client.Client, target *pb.AttachTarget, stopOnInterrupt bool) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdinFd := os.Stdin.Fd()
	isTTY := term.IsTerminal(stdinFd)
	cols, rows := 80, 24
	if isTTY {
		if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
			cols, rows = w, h
		}
	}
	stream := cl.Attach(ctx)
	if err := stream.Send(&pb.AttachRequest{Msg: &pb.AttachRequest_Open{Open: &pb.AttachOpen{Target: target, Cols: int32(cols), Rows: int32(rows)}}}); err != nil {
		return 0, err
	}
	first, err := stream.Receive()
	if err != nil {
		return 0, err
	}
	ready := first.GetReady()
	if ready == nil {
		return 0, errors.New("unexpected attach response")
	}
	var sendMu sync.Mutex
	send := func(req *pb.AttachRequest) {
		sendMu.Lock()
		defer sendMu.Unlock()
		_ = stream.Send(req)
	}

	if isTTY && ready.Tty {
		old, err := term.MakeRaw(stdinFd)
		if err == nil {
			defer func() { _ = term.Restore(stdinFd, old) }()
		}
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
					send(&pb.AttachRequest{Msg: &pb.AttachRequest_Resize{Resize: &pb.AttachResize{Cols: int32(w), Rows: int32(h)}}})
				}
			}
		}()
	} else if stopOnInterrupt {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		go func() {
			if _, ok := <-sigCh; ok && target.Kind == "task" {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer stopCancel()
				_, _ = cl.StopTask(stopCtx, connect.NewRequest(&pb.StopTaskRequest{Project: target.Project, Task: target.Name}))
			}
		}()
	}

	detached := make(chan struct{})
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if isTTY {
					for i, b := range chunk {
						if b == 0x1d { // Ctrl-]
							if i > 0 {
								send(&pb.AttachRequest{Msg: &pb.AttachRequest_Input{Input: append([]byte(nil), chunk[:i]...)}})
							}
							close(detached)
							cancel()
							return
						}
					}
				}
				send(&pb.AttachRequest{Msg: &pb.AttachRequest_Input{Input: append([]byte(nil), chunk...)}})
			}
			if err != nil {
				if errors.Is(err, io.EOF) && !isTTY {
					send(&pb.AttachRequest{Msg: &pb.AttachRequest_CloseStdin{CloseStdin: true}})
				}
				return
			}
		}
	}()

	for {
		msg, err := stream.Receive()
		if err != nil {
			select {
			case <-detached:
				c.Errorf("\r\ndevyard: detached\r\n")
				return 0, nil
			default:
			}
			if ctx.Err() != nil {
				return 130, nil
			}
			if errors.Is(err, io.EOF) {
				return 0, errEndedWithoutStatus
			}
			return 0, err
		}
		switch m := msg.Msg.(type) {
		case *pb.AttachResponse_Output:
			_, _ = os.Stdout.Write(m.Output)
		case *pb.AttachResponse_Exit:
			return int(m.Exit.ExitCode), nil
		}
	}
}
