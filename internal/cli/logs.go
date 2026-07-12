package cli

import (
	"fmt"
	"os"
	"sync"

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

		if len(cfg.Order) == 0 {
			return fmt.Errorf("no services defined in config")
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		if len(args) == 1 {
			service := args[0]
			client, err := control.Dial(socket)
			if err != nil {
				fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
				return err
			}
			defer func() { _ = client.Close() }()
			return client.Logs(cfg.Project, service, logsFollow, newLogPrinter(service))
		}

		if len(cfg.Order) == 1 {
			service := cfg.Order[0]
			client, err := control.Dial(socket)
			if err != nil {
				fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
				return err
			}
			defer func() { _ = client.Close() }()
			return client.Logs(cfg.Project, service, logsFollow, newLogPrinter(service))
		}

		return logsAllServices(socket, cfg.Project, cfg.Order)
	},
}

// logsAllServices opens a client connection per service and streams their logs
// concurrently. For non-follow mode each connection returns after the existing
// content is printed. For follow mode the goroutines block until the daemon
// shuts down or the connections are closed.
func logsAllServices(socket, project string, order []string) error {
	var wg sync.WaitGroup
	for _, name := range order {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			c, err := control.Dial(socket)
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.Logs(project, svc, logsFollow, newLogPrinter(svc))
		}(name)
	}
	wg.Wait()
	return nil
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
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
