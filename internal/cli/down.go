package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop all services and the supervisor",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}
		locs, err := resolveLocations(cfg.Project)
		if err != nil {
			return err
		}

		client, err := control.Dial(locs.Socket)
		if err != nil {
			// No supervisor running: make sure a stale pidfile/socket isn't
			// left behind. Treat as success (idempotent down) so callers can
			// run `down` without first checking state.
			_ = daemon.RemovePidfile(locs)
			fmt.Fprintf(os.Stderr, "local-compose: no supervisor running for project %q\n", cfg.Project)
			return nil
		}
		defer func() { _ = client.Close() }()

		if err := client.Stop(); err != nil {
			return err
		}
		// The Stop request returns once the supervisor has signalled every
		// service; wait for the daemon process itself to exit before telling
		// the user it's down.
		waitForSupervisorExit(locs, 15*time.Second)
		_ = daemon.RemovePidfile(locs)
		fmt.Fprintln(os.Stderr, "local-compose: stopped")
		return nil
	},
}
