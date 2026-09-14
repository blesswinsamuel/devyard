package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

func newDaemonCmd(ctx *CLIContext) *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"d"},
		Short:   "Manage the global local-compose daemon process",
		Long:    "Subcommands to start, stop, restart, or inspect status of the global daemon.",
	}

	daemonCmd.AddCommand(
		newDaemonStartCmd(ctx),
		newDaemonStopCmd(ctx),
		newDaemonRestartCmd(ctx),
		newDaemonStatusCmd(ctx),
	)

	return daemonCmd
}

func newDaemonStartCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the global daemon (manages multiple projects)",
		RunE: func(cmd *cobra.Command, args []string) error {
			locs, err := project.ResolveDaemon()
			if err != nil {
				return err
			}
			if pid, err := daemon.DaemonRunning(locs); err != nil {
				return fmt.Errorf("check running daemon: %w", err)
			} else if pid > 0 {
				ctx.Errorf("local-compose: daemon already running (pid %d)\n", pid)
				return nil
			}

			pid, err := daemon.SpawnDaemon(locs)
			if err != nil {
				return err
			}
			ctx.Errorf("local-compose: daemon started (pid %d)\n", pid)

			if err := control.WaitForSocket(locs.Socket, 3*time.Second); err != nil {
				ctx.Errorf("local-compose: %v\n", err)
				return nil
			}
			return nil
		},
	}
}

func newDaemonStopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the global daemon and all projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			locs, err := project.ResolveDaemon()
			if err != nil {
				return err
			}
			pid, err := daemon.DaemonRunning(locs)
			if err != nil {
				return fmt.Errorf("check running daemon: %w", err)
			}
			if pid == 0 {
				_ = daemon.RemoveDaemonPidfile(locs)
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}

			client, err := control.Dial(locs.Socket)
			if err != nil {
				_ = daemon.RemoveDaemonPidfile(locs)
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			if err := client.StopDaemon(); err != nil {
				return err
			}
			waitForDaemonExit(locs, 15*time.Second)
			_ = daemon.RemoveDaemonPidfile(locs)
			ctx.Errorln("local-compose: daemon stopped")
			return nil
		},
	}
}

func newDaemonRestartCmd(ctx *CLIContext) *cobra.Command {
	var restartServices bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the global daemon with process adoption",
		Long:  "Stops the daemon and starts a new daemon instance. Running process groups are adopted cleanly without killing services.",
		RunE: func(cmd *cobra.Command, args []string) error {
			locs, err := project.ResolveDaemon()
			if err != nil {
				return err
			}

			pid, err := daemon.DaemonRunning(locs)
			if err != nil {
				return fmt.Errorf("check running daemon: %w", err)
			}
			if pid == 0 {
				_ = daemon.RemoveDaemonPidfile(locs)
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}

			client, err := control.Dial(locs.Socket)
			if err != nil {
				_ = daemon.RemoveDaemonPidfile(locs)
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			newPid, err := client.RestartDaemon(restartServices)
			if err != nil {
				return fmt.Errorf("restart daemon: %w", err)
			}

			if err := waitForDaemonHandover(locs, newPid, 30*time.Second); err != nil {
				return fmt.Errorf("wait for daemon restart: %w", err)
			}

			if restartServices {
				ctx.Errorln("local-compose: daemon and services restarted")
			} else {
				ctx.Errorln("local-compose: daemon restarted (running services adopted)")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&restartServices, "restart-services", "r", false, "Restart all managed services in addition to the daemon")
	return cmd
}

func newDaemonStatusCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check the status of the global daemon process",
		RunE: func(cmd *cobra.Command, args []string) error {
			locs, err := project.ResolveDaemon()
			if err != nil {
				return err
			}
			pid, err := daemon.DaemonRunning(locs)
			if err != nil {
				return fmt.Errorf("check running daemon: %w", err)
			}
			if pid == 0 {
				_ = daemon.RemoveDaemonPidfile(locs)
				if ctx.IsJSON() {
					return ctx.PrintJSON(map[string]any{"status": "stopped"})
				}
				_, _ = fmt.Fprintln(ctx.Out, "Daemon status: stopped")
				return nil
			}

			c, err := control.Dial(locs.Socket)
			if err != nil {
				if ctx.IsJSON() {
					return ctx.PrintJSON(map[string]any{"status": "unresponsive", "pid": pid})
				}
				_, _ = fmt.Fprintf(ctx.Out, "Daemon status: unresponsive (pid %d, socket unavailable)\n", pid)
				return nil
			}
			defer func() { _ = c.Close() }()

			projects, err := c.ListProjects()
			if err != nil {
				if ctx.IsJSON() {
					return ctx.PrintJSON(map[string]any{"status": "running", "pid": pid, "error": err.Error()})
				}
				_, _ = fmt.Fprintf(ctx.Out, "Daemon status: running (pid %d), error listing projects: %v\n", pid, err)
				return nil
			}

			if ctx.IsJSON() {
				return ctx.PrintJSON(map[string]any{
					"status":   "running",
					"pid":      pid,
					"projects": projects,
				})
			}

			_, _ = fmt.Fprintf(ctx.Out, "Daemon status: running (pid %d)\n", pid)
			_, _ = fmt.Fprintf(ctx.Out, "Active projects: %d\n", len(projects))
			for _, p := range projects {
				_, _ = fmt.Fprintf(ctx.Out, "  - %s (%s) [%s]\n", p.Name, p.Status, p.ConfigPath)
			}
			return nil
		},
	}
}

func waitForDaemonHandover(locs *project.DaemonLocations, newPid int32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if newPid > 0 && !daemon.IsAlive(int(newPid)) {
			return fmt.Errorf("replacement daemon (pid %d) exited during restart", newPid)
		}
		client, err := control.Dial(locs.Socket)
		if err == nil {
			info, statusErr := client.DaemonStatus()
			_ = client.Close()
			if statusErr == nil && info != nil && (newPid <= 0 || info.Pid == newPid) {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("timed out waiting for the replacement daemon to take over")
}

func waitForDaemonExit(locs *project.DaemonLocations, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid, err := daemon.ReadPidfile(locs.Pidfile)
		if err != nil {
			return
		}
		if !daemon.IsAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
