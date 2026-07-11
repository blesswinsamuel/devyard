package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

var startDaemonCmd = &cobra.Command{
	Use:   "start-daemon",
	Short: "Start the global daemon (manages multiple projects)",
	RunE: func(cmd *cobra.Command, args []string) error {
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
	},
}

var stopDaemonCmd = &cobra.Command{
	Use:   "stop-daemon",
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
	},
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
