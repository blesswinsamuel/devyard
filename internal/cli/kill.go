package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var killSignal string

var killCmd = &cobra.Command{
	Use:   "kill [service]",
	Short: "Forcefully terminate one or all services",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName, err := resolveProjectName(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		service := ""
		if len(args) == 1 {
			service = args[0]
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}
		defer func() { _ = client.Close() }()

		if err := client.KillService(projName, service, killSignal); err != nil {
			return err
		}
		if service == "" {
			fmt.Fprintf(os.Stderr, "local-compose: killed all services (%s)\n", killSignal)
		} else {
			fmt.Fprintf(os.Stderr, "local-compose: killed %q (%s)\n", service, killSignal)
		}
		return nil
	},
}

func init() {
	killCmd.Flags().StringVarP(&killSignal, "signal", "s", "SIGKILL", "Signal to send (e.g. SIGTERM, SIGINT, SIGHUP)")
}
