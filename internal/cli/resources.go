package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/blesswinsamuel/devyard/internal/client"
	"github.com/blesswinsamuel/devyard/internal/daemon"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// --- project ---------------------------------------------------------------

func newProjectCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Aliases: []string{"projects", "p"}, Short: "Manage projects"}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List projects",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				cl, err := c.dial(ctx)
				if err != nil {
					return err
				}
				defer cl.Close()
				snap, err := state(ctx, cl)
				if err != nil {
					return err
				}
				if c.JSON() {
					sorted := sortedProjects(snap.Projects)
					msgs := make([]proto.Message, len(sorted))
					for i, p := range sorted {
						msgs[i] = p
					}
					return c.printProtoJSON(msgs)
				}
				return renderProjects(c, snap.Projects)
			},
		},
		newProjectMoveCmd(c),
		projectAction(c, "start", "Start all services of a project", func(ctx context.Context, cl *client.Client, id string) error {
			_, err := cl.StartProject(ctx, connect.NewRequest(&pb.StartProjectRequest{Project: id}))
			return err
		}),
		projectAction(c, "stop", "Stop a project (it will not autostart)", func(ctx context.Context, cl *client.Client, id string) error {
			_, err := cl.StopProject(ctx, connect.NewRequest(&pb.StopProjectRequest{Project: id}))
			return err
		}),
		projectAction(c, "restart", "Restart a project's services", func(ctx context.Context, cl *client.Client, id string) error {
			_, err := cl.RestartProject(ctx, connect.NewRequest(&pb.RestartProjectRequest{Project: id}))
			return err
		}),
		projectAction(c, "reload", "Re-read a project's config and apply changes", func(ctx context.Context, cl *client.Client, id string) error {
			_, err := cl.ReloadProject(ctx, connect.NewRequest(&pb.ReloadProjectRequest{Project: id}))
			return err
		}),
		projectAction(c, "remove", "Remove a project from the list: stop it and delete its state and logs", func(ctx context.Context, cl *client.Client, id string) error {
			_, err := cl.RemoveProject(ctx, connect.NewRequest(&pb.RemoveProjectRequest{Project: id}))
			return err
		}),
		newProjectLogsCmd(c),
	)
	return cmd
}

func projectAction(c *Context, use, short string, fn func(context.Context, *client.Client, string) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use + " [project]",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			if len(args) == 1 {
				c.Project = args[0]
			}
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			if err := fn(ctx, cl, id); err != nil {
				return err
			}
			c.Errorf("devyard: project %q: %s done\n", id, use)
			return nil
		},
	}
	if use == "remove" {
		cmd.Aliases = []string{"rm"}
	}
	return cmd
}

// newAddCmd adds a project directory to the global config's project list.
func newAddCmd(c *Context) *cobra.Command {
	var noStart bool
	cmd := &cobra.Command{
		Use:   "add [path]",
		Short: "Add a project (default: the current directory)",
		Long: "Adds a directory to the project list in the global config and registers it. The\n" +
			"directory may have a devyard.yml or not: without one it is a project with only the\n" +
			"git view and terminals. Services start unless --no-start.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.ensureDaemon(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			path, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			resp, err := cl.AddProject(ctx, connect.NewRequest(&pb.AddProjectRequest{
				Path: path, Env: captureEnv(), Start: !noStart,
			}))
			if err != nil {
				return err
			}
			c.Errorf("devyard: added project %q (%s)\n", resp.Msg.Project.GetId(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noStart, "no-start", false, "Add without starting services")
	return cmd
}

// newProjectMoveCmd reorders the project list.
func newProjectMoveCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "move <project> <position|first|last>",
		Short: "Move a project to a position in the list (1 is first)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parsePosition(args[1])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			if _, err := cl.MoveProject(ctx, connect.NewRequest(&pb.MoveProjectRequest{Project: args[0], Index: int32(index)})); err != nil {
				return err
			}
			c.Errorf("devyard: moved project %q to %s\n", args[0], args[1])
			return nil
		},
	}
}

// parsePosition converts a 1-based position (or first/last) to the 0-based
// index of the protocol; last is any index past the end (the server clamps).
func parsePosition(s string) (int, error) {
	switch s {
	case "first":
		return 0, nil
	case "last":
		return math.MaxInt32, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("position must be a number from 1, first or last, got %q", s)
	}
	return n - 1, nil
}

func newProjectLogsCmd(c *Context) *cobra.Command {
	var follow, previous bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs [project]",
		Short: "Merged logs of every service in a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				c.Project = args[0]
			}
			return c.logsCommand(cmd.Context(), "service", nil, follow, previous, tail)
		},
	}
	logFlags(cmd, &follow, &previous, &tail)
	return cmd
}

// --- service ---------------------------------------------------------------

func newServiceCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{Use: "service", Aliases: []string{"services", "svc", "s"}, Short: "Manage services"}
	list := newStatusCmd(c)
	list.Use = "list [service...]"
	list.Aliases = []string{"ls"}
	cmd.AddCommand(list, newStartCmd(c), newStopCmd(c), newRestartCmd(c), newKillCmd(c), newLogsCmd(c), newTopCmd(c), newBuildCmd(c))
	return cmd
}

// --- task --------------------------------------------------------------------

func newTaskCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{Use: "task", Aliases: []string{"tasks", "t"}, Short: "Manage tasks"}
	var follow, previous bool
	var tail int
	logs := &cobra.Command{
		Use:   "logs <task>",
		Short: "Show (or follow) a task's output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.logsCommand(cmd.Context(), "task", args, follow, previous, tail)
		},
	}
	logFlags(logs, &follow, &previous, &tail)
	var sig string
	kill := &cobra.Command{
		Use:   "kill <task>",
		Short: "Send a signal (default SIGKILL) to a running task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.withProject(cmd.Context(), func(ctx context.Context, cl *client.Client, id string) error {
				_, err := cl.KillTask(ctx, connect.NewRequest(&pb.KillTaskRequest{Project: id, Task: args[0], Signal: sig}))
				return err
			})
		},
	}
	kill.Flags().StringVarP(&sig, "signal", "s", "SIGKILL", "Signal to send")
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List tasks",
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
				var list []*pb.Task
				for _, t := range snap.Tasks {
					if t.Project == id {
						list = append(list, t)
					}
				}
				if c.JSON() {
					msgs := make([]proto.Message, len(list))
					for i, t := range list {
						msgs[i] = t
					}
					return c.printProtoJSON(msgs)
				}
				return renderTasks(c, list)
			},
		},
		newRunCmd(c),
		&cobra.Command{
			Use:   "stop <task>",
			Short: "Stop a running task",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.withProject(cmd.Context(), func(ctx context.Context, cl *client.Client, id string) error {
					_, err := cl.StopTask(ctx, connect.NewRequest(&pb.StopTaskRequest{Project: id, Task: args[0]}))
					return err
				})
			},
		},
		kill,
		logs,
	)
	return cmd
}

func (c *Context) withProject(ctx context.Context, fn func(context.Context, *client.Client, string) error) error {
	cl, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	id, err := c.projectID(ctx, cl)
	if err != nil {
		return err
	}
	return fn(ctx, cl, id)
}

// --- daemon ------------------------------------------------------------------

func newDaemonCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Aliases: []string{"d"}, Short: "Manage the devyard daemon"}
	var restartServices bool
	restart := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon (services keep running unless --restart-services)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			before, err := cl.Ping(ctx, 2*time.Second)
			if err != nil {
				return err
			}
			if _, err := cl.RestartDaemon(ctx, connect.NewRequest(&pb.RestartDaemonRequest{RestartServices: restartServices})); err != nil {
				return err
			}
			deadline := time.Now().Add(5 * time.Minute)
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				fresh := client.Dial(mustSocket())
				info, err := fresh.Ping(ctx, 500*time.Millisecond)
				fresh.Close()
				if err == nil && info.Pid != before.Pid {
					c.Errorf("devyard: daemon restarted (pid %d)\n", info.Pid)
					return nil
				}
			}
			return errors.New("timed out waiting for the replacement daemon")
		},
	}
	restart.Flags().BoolVarP(&restartServices, "restart-services", "r", false, "Also restart every service")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start the daemon",
			RunE: func(cmd *cobra.Command, args []string) error {
				cl, err := c.ensureDaemon(cmd.Context())
				if err != nil {
					return err
				}
				cl.Close()
				return nil
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop every service and the daemon",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				d, err := dirs()
				if err != nil {
					return err
				}
				cl, err := c.dial(ctx)
				if err != nil {
					c.Errorf("devyard: no daemon running\n")
					return nil
				}
				defer cl.Close()
				info, err := cl.Ping(ctx, 2*time.Second)
				if err != nil {
					return err
				}
				if _, err := cl.StopDaemon(ctx, connect.NewRequest(&pb.StopDaemonRequest{})); err != nil {
					return err
				}
				deadline := time.Now().Add(5 * time.Minute)
				for daemon.Alive(int(info.Pid)) && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
				if daemon.Alive(int(info.Pid)) {
					return fmt.Errorf("daemon (pid %d) did not exit; see %s", info.Pid, d.DaemonLog())
				}
				c.Errorf("devyard: daemon stopped\n")
				return nil
			},
		},
		restart,
		&cobra.Command{
			Use:   "status",
			Short: "Show daemon status",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				d, err := dirs()
				if err != nil {
					return err
				}
				cl := client.Dial(d.Socket())
				defer cl.Close()
				info, err := cl.Ping(ctx, 2*time.Second)
				if err != nil {
					if c.JSON() {
						c.Printf("{\"status\": \"stopped\"}\n")
						return nil
					}
					c.Printf("Daemon: stopped\n")
					return nil
				}
				if c.JSON() {
					return c.printProtoJSON([]proto.Message{info})
				}
				c.Printf("Daemon:    running (pid %d, %s, up %s)\n", info.Pid, info.Version, fmtUptime(info.StartedAtUnixMs))
				c.Printf("Dashboard: http://%s\n", dashboardHost(info.WebAddr))
				c.Printf("Proxy:     %s (*.%s)\n", info.ProxyAddr, info.DomainSuffix)
				c.Printf("Socket:    %s\n", d.Socket())
				c.Printf("Log:       %s\n", d.DaemonLog())
				return nil
			},
		},
	)
	return cmd
}

func mustSocket() string {
	d, err := dirs()
	if err != nil {
		return ""
	}
	return d.Socket()
}
