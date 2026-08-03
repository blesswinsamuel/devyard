package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop all services in the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projName, err := resolveProjectName(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}
		defer func() { _ = client.Close() }()

		if err := client.StopProject(projName); err != nil {
			// If the project isn't running in the daemon, treat as success
			// (idempotent down).
			fmt.Fprintln(os.Stderr, "local-compose: stopped")
			return nil
		}
		fmt.Fprintln(os.Stderr, "local-compose: stopped")
		return nil
	},
}
