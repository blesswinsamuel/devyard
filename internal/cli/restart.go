package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var restartCmd = &cobra.Command{
	Use:   "restart [service]",
	Short: "Restart one or all services",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}
		locs, err := resolveLocations(cfg.Project)
		if err != nil {
			return err
		}

		service := ""
		if len(args) == 1 {
			service = args[0]
		}

		client, err := control.Dial(locs.Socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no supervisor running for project %q\n", cfg.Project)
			return err
		}
		defer func() { _ = client.Close() }()

		if err := client.Restart("", service); err != nil {
			return err
		}
		if service == "" {
			fmt.Fprintln(os.Stderr, "local-compose: restarted all services")
		} else {
			fmt.Fprintf(os.Stderr, "local-compose: restarted %q\n", service)
		}
		return nil
	},
}
