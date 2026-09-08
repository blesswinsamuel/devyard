package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the global local-compose daemon process",
	Long:  "Subcommands to start, stop, restart, or inspect status of the global daemon.",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the global daemon (manages multiple projects)",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the global daemon and all projects",
	RunE:  runDaemonStop,
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the global daemon with process adoption",
	Long:  "Stops the daemon and starts a new daemon instance. Running process groups are adopted cleanly without killing services.",
	RunE:  runDaemonRestart,
}

var daemonStatusCmd = &cobra.Command{
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
			_, _ = fmt.Fprintln(os.Stdout, "Daemon status: stopped")
			return nil
		}

		c, err := control.Dial(locs.Socket)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "Daemon status: unresponsive (pid %d, socket unavailable)\n", pid)
			return nil
		}
		defer func() { _ = c.Close() }()

		projects, err := c.ListProjects()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "Daemon status: running (pid %d), error listing projects: %v\n", pid, err)
			return nil
		}

		_, _ = fmt.Fprintf(os.Stdout, "Daemon status: running (pid %d)\n", pid)
		_, _ = fmt.Fprintf(os.Stdout, "Active projects: %d\n", len(projects))
		for _, p := range projects {
			_, _ = fmt.Fprintf(os.Stdout, "  - %s (%s) [%s]\n", p.Name, p.Status, p.ConfigPath)
		}
		return nil
	},
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	locs, err := project.ResolveDaemon()
	if err != nil {
		return err
	}
	if pid, err := daemon.DaemonRunning(locs); err != nil {
		return fmt.Errorf("check running daemon: %w", err)
	} else if pid > 0 {
		fmt.Fprintf(os.Stderr, "local-compose: daemon already running (pid %d)\n", pid)
		return nil
	}

	pid, err := daemon.SpawnDaemon(locs)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "local-compose: daemon started (pid %d)\n", pid)

	if err := control.WaitForSocket(locs.Socket, 3*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "local-compose: %v\n", err)
		return nil
	}
	return nil
}

func runDaemonStop(cmd *cobra.Command, args []string) error {
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
		fmt.Fprintln(os.Stderr, "local-compose: no daemon running")
		return nil
	}

	client, err := control.Dial(locs.Socket)
	if err != nil {
		_ = daemon.RemoveDaemonPidfile(locs)
		fmt.Fprintln(os.Stderr, "local-compose: no daemon running")
		return nil
	}
	defer func() { _ = client.Close() }()

	if err := client.StopDaemon(); err != nil {
		return err
	}
	waitForDaemonExit(locs, 15*time.Second)
	_ = daemon.RemoveDaemonPidfile(locs)
	fmt.Fprintln(os.Stderr, "local-compose: daemon stopped")
	return nil
}

var flagRestartServices bool

func runDaemonRestart(cmd *cobra.Command, args []string) error {
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
		fmt.Fprintln(os.Stderr, "local-compose: no daemon running")
		return nil
	}

	client, err := control.Dial(locs.Socket)
	if err != nil {
		_ = daemon.RemoveDaemonPidfile(locs)
		fmt.Fprintln(os.Stderr, "local-compose: no daemon running")
		return nil
	}
	defer func() { _ = client.Close() }()

	newPid, err := client.RestartDaemon(flagRestartServices)
	if err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}

	if err := waitForDaemonHandover(locs, newPid, 30*time.Second); err != nil {
		return fmt.Errorf("wait for daemon restart: %w", err)
	}

	if flagRestartServices {
		fmt.Fprintln(os.Stderr, "local-compose: daemon and services restarted")
	} else {
		fmt.Fprintln(os.Stderr, "local-compose: daemon restarted (running services adopted)")
	}
	return nil
}

// waitForDaemonHandover waits until the replacement daemon is serving the
// control socket. It verifies the *serving* daemon's pid matches the pid the
// old daemon reported spawning — the socket file alone is not enough, because
// the old daemon's socket lingers until it exits. Fails fast if the
// replacement daemon process died (e.g. it could not acquire the daemon lock).
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

// waitForDaemonExit polls the pidfile until the daemon process is gone or the
// timeout elapses.
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

func init() {
	daemonRestartCmd.Flags().BoolVarP(&flagRestartServices, "restart-services", "r", false, "Restart all managed services in addition to the daemon")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
}
