package cli

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
	"github.com/blesswinsamuel/local-compose/internal/ui"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Fetch or stream service logs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		if len(args) == 1 {
			projName, err := resolveProjectName(flagConfigPath, flagProject)
			if err != nil {
				return err
			}
			service := args[0]
			client, err := control.Dial(socket)
			if err != nil {
				fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
				return err
			}
			defer func() { _ = client.Close() }()
			return client.Logs(projName, service, logsFollow, newLogPrinter(service))
		}

		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		if len(cfg.Order) == 0 {
			return fmt.Errorf("no services defined in config")
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

// logsAllServices opens a client connection per service and streams their logs.
// In follow mode goroutines print lines as they arrive. In snapshot mode all
// lines are buffered, sorted by timestamp, then printed.
func logsAllServices(socket, project string, order []string) error {
	if logsFollow {
		return logsAllFollow(socket, project, order)
	}
	return logsAllSnapshot(socket, project, order)
}

type logEntry struct {
	timestamp time.Time
	service   string
	line      string
}

// logsAllSnapshot collects every service's log content, sorts by timestamp,
// and prints the merged result.
func logsAllSnapshot(socket, project string, order []string) error {
	var entries []logEntry
	var mu sync.Mutex
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
			_ = c.Logs(project, svc, false, func(line string) {
				ts, rest := supervisor.ParseTimestamp(line)
				mu.Lock()
				entries = append(entries, logEntry{timestamp: ts, service: svc, line: rest})
				mu.Unlock()
			})
		}(name)
	}

	wg.Wait()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].timestamp.Before(entries[j].timestamp)
	})

	for _, e := range entries {
		prefix := ui.ServicePrefix(e.service) + " │ "
		fmt.Println(prefix + ui.CleanLogLine(e.line))
	}
	return nil
}

// logsAllFollow streams every service's logs in real time. Lines are printed
// as they arrive (no cross-service sorting) so there is no added latency.
func logsAllFollow(socket, project string, order []string) error {
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
			_ = c.Logs(project, svc, true, func(line string) {
				_, rest := supervisor.ParseTimestamp(line)
				prefix := ui.ServicePrefix(svc) + " │ "
				fmt.Println(prefix + ui.CleanLogLine(rest))
			})
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
