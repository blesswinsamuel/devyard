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
		locs, err := resolveLocations(cfg.Project)
		if err != nil {
			return err
		}

		client, err := control.Dial(locs.Socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no supervisor running for project %q (is it up?)\n", cfg.Project)
			return err
		}
		defer func() { _ = client.Close() }()

		states, err := client.List("")
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
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tRESTARTS\tHEALTH\tEXIT")
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
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%d\n", st.Name, st.Status, pid, st.Restarts, health, st.ExitCode)
	}
	_ = w.Flush()
}
