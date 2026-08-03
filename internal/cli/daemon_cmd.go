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

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the global local-compose daemon process",
	Long:  "Subcommands to start, stop, restart, reload, or inspect status of the global daemon.",
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
	RunE:  runDaemonReload,
}

var daemonReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload the global daemon with fresh code (alias for daemon restart)",
	Long:  "Alias for `daemon restart`. Restarts the daemon binary and re-attaches/adopts running projects.",
	RunE:  runDaemonReload,
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
			fmt.Fprintln(os.Stdout, "Daemon status: stopped")
			return nil
		}

		c, err := control.Dial(locs.Socket)
		if err != nil {
			fmt.Fprintf(os.Stdout, "Daemon status: unresponsive (pid %d, socket unavailable)\n", pid)
			return nil
		}
		defer c.Close()

		projects, err := c.ListProjects()
		if err != nil {
			fmt.Fprintf(os.Stdout, "Daemon status: running (pid %d), error listing projects: %v\n", pid, err)
			return nil
		}

		fmt.Fprintf(os.Stdout, "Daemon status: running (pid %d)\n", pid)
		fmt.Fprintf(os.Stdout, "Active projects: %d\n", len(projects))
		for _, p := range projects {
			fmt.Fprintf(os.Stdout, "  - %s (%s) [%s]\n", p.Name, p.Status, p.ConfigPath)
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

func runDaemonReload(cmd *cobra.Command, args []string) error {
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
			return c.StartProject(cfgPath, false, "")
		}(); err != nil {
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

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonReloadCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
}
