package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var reloadCmd = &cobra.Command{
	Use:   "reload-daemon",
	Short: "Reload the daemon with fresh code (restarts all running projects)",
	Long:  "Stops the running daemon, then starts a new one with the current binary. Projects that were running are re-started automatically.",
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

		projects, err := func() ([]protocol.ProjectInfo, error) {
			c, err := control.Dial(locs.Socket)
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.ListProjects()
		}()
		if err != nil {
			_ = daemon.RemoveDaemonPidfile(locs)
			fmt.Fprintln(os.Stderr, "local-compose: no daemon running")
			return nil
		}

		var runningConfigs []string
		for _, p := range projects {
			if p.Status == "running" && p.ConfigPath != "" {
				runningConfigs = append(runningConfigs, p.ConfigPath)
			}
		}

		if err := func() error {
			c, err := control.Dial(locs.Socket)
			if err != nil {
				return err
			}
			defer c.Close()
			return c.StopDaemon()
		}(); err != nil {
			return fmt.Errorf("stop daemon: %w", err)
		}

		waitForDaemonExit(locs, 15*time.Second)
		_ = daemon.RemoveDaemonPidfile(locs)
		fmt.Fprintln(os.Stderr, "local-compose: daemon stopped")

		newPid, err := daemon.SpawnDaemon(locs)
		if err != nil {
			return fmt.Errorf("start daemon: %w", err)
		}
		fmt.Fprintf(os.Stderr, "local-compose: daemon started (pid %d)\n", newPid)

		control.WaitForSocket(locs.Socket, 3*time.Second)

		for _, cfgPath := range runningConfigs {
			if err := func() error {
				c, err := control.Dial(locs.Socket)
				if err != nil {
					return err
				}
				defer c.Close()
				return c.StartProject(cfgPath, false)
			}(); err != nil {
				fmt.Fprintf(os.Stderr, "local-compose: reload: start project %s: %v\n", cfgPath, err)
			}
		}

		fmt.Fprintln(os.Stderr, "local-compose: daemon reloaded")
		return nil
	},
}
