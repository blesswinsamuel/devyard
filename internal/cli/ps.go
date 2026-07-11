package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running services",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
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

		states, err := client.List(cfg.Project)
		if err != nil {
			return err
		}
		printStates(states)
		return nil
	},
}

// printStates renders the service snapshot as an aligned table. The header
// uses lipgloss styling via internal/ui; rows are plain text so columns stay
// readable in piped/non-terminal contexts.
func printStates(states []protocol.ServiceState) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tRESTARTS\tHEALTH")
	if len(states) == 0 {
		_, _ = fmt.Fprintln(w, "(no services)")
		_ = w.Flush()
		return
	}
	for _, st := range states {
		pid := "-"
		if st.PID > 0 {
			pid = fmt.Sprintf("%d", st.PID)
		}
		health := "-"
		if st.HasHealth {
			health = st.Health
		}
		status := st.Status
		if st.Status == "exited" {
			status = fmt.Sprintf("exited (%d)", st.ExitCode)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", st.Name, status, pid, st.Restarts, health)
	}
	_ = w.Flush()
}
