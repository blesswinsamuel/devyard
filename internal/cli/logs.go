package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Fetch or stream service logs",
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
		} else {
			// No service given: pick the first service in start order so
			// `logs` is still useful without an argument in the common
			// single-service case.
			if len(cfg.Order) == 0 {
				return fmt.Errorf("no services defined in config")
			}
			service = cfg.Order[0]
			if len(cfg.Order) > 1 {
				fmt.Fprintf(os.Stderr, "local-compose: no service specified, defaulting to %q\n", service)
			}
		}

		client, err := control.Dial(locs.Socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no supervisor running for project %q\n", cfg.Project)
			return err
		}
		defer func() { _ = client.Close() }()

		return client.Logs("", service, logsFollow, func(line string) {
			fmt.Println(line)
		})
	},
}

func init() {
	// --follow has no -f shorthand: -f is reserved for the persistent --file
	// config flag. This matches docker-compose's `--follow` spelling.
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Follow log output")
}
