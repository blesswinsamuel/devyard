package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/ui"
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

		service := ""
		if len(args) == 1 {
			service = args[0]
		} else {
			if len(cfg.Order) == 0 {
				return fmt.Errorf("no services defined in config")
			}
			service = cfg.Order[0]
			if len(cfg.Order) > 1 {
				fmt.Fprintf(os.Stderr, "local-compose: no service specified, defaulting to %q\n", service)
			}
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

		return client.Logs(cfg.Project, service, logsFollow, newLogPrinter(service))
	},
}

func init() {
	// --follow has no -f shorthand: -f is reserved for the persistent --file
	// config flag. This matches docker-compose's `--follow` spelling.
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Follow log output")
}

// newLogPrinter returns a callback that prints log lines to stdout with a
// colored service prefix, stripping non-SGR ANSI sequences so control
// characters from child processes don't corrupt the terminal output.
func newLogPrinter(service string) func(string) {
	prefix := ui.ServicePrefix(service) + " │ "
	return func(line string) {
		fmt.Println(prefix + ui.CleanLogLine(line))
	}
}
