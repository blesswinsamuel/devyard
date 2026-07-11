package cli

import (
	"fmt"
	"os"
	"strings"
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

		if err := control.WaitForSocket(locs.Socket, 5*time.Second); err != nil {
			return fmt.Errorf("wait for daemon: %w", err)
		}

		// Verify the daemon is responsive before sending start requests.
		newProjects, err := func() ([]protocol.ProjectInfo, error) {
			c, err := control.Dial(locs.Socket)
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.ListProjects()
		}()
		if err != nil {
			return fmt.Errorf("verify daemon: %w", err)
		}

		// Build the set of projects the new daemon already started (via
		// autostart) so we don't hit "already running" errors.
		alreadyRunning := make(map[string]bool, len(newProjects))
		for _, p := range newProjects {
			if p.Status == "running" {
				alreadyRunning[p.Name] = true
			}
		}

		var startErrs []error
		for _, cfgPath := range runningConfigs {
			if err := func() error {
				c, err := control.Dial(locs.Socket)
				if err != nil {
					return err
				}
				defer c.Close()
				return c.StartProject(cfgPath, false)
			}(); err != nil {
				// "already running" is not a real error — autostart
				// already started the project with the same config.
				if !isAlreadyRunning(err, alreadyRunning) {
					startErrs = append(startErrs, fmt.Errorf("start project %s: %w", cfgPath, err))
				}
			}
		}

		if len(startErrs) > 0 {
			for _, e := range startErrs {
				fmt.Fprintf(os.Stderr, "local-compose: %v\n", e)
			}
			return fmt.Errorf("reload completed with %d error(s)", len(startErrs))
		}

		fmt.Fprintln(os.Stderr, "local-compose: daemon reloaded")
		return nil
	},
}

// isAlreadyRunning reports whether err is an "already running" error for a
// project in the alreadyRunning set. This avoids treating the autostart race
// as a real failure.
func isAlreadyRunning(err error, alreadyRunning map[string]bool) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for name := range alreadyRunning {
		if strings.Contains(msg, fmt.Sprintf("project %q is already running", name)) {
			return true
		}
	}
	return false
}
