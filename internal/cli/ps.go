package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var flagPsAll bool

func init() {
	psCmd.Flags().BoolVarP(&flagPsAll, "all", "a", false, "Show services across all projects")
}

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running services",
	RunE: func(cmd *cobra.Command, args []string) error {
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

		showAll := flagPsAll
		var projName string

		if !showAll {
			p, err := resolveProjectName(flagConfigPath, flagProject)
			if err != nil {
				// Fall back to showing all projects if local-compose.yml is not found
				showAll = true
			} else {
				projName = p
			}
		}

		if showAll {
			projects, err := client.ListProjects()
			if err != nil {
				return err
			}
			var allStates []projState
			for _, p := range projects {
				c, err := control.Dial(socket)
				if err != nil {
					continue
				}
				states, err := c.List(p.Name)
				_ = c.Close()
				if err != nil {
					continue
				}
				for _, st := range states {
					allStates = append(allStates, projState{
						Project: p.Name,
						State:   st,
					})
				}
			}
			printAllStates(allStates)
			return nil
		}

		states, err := client.List(projName)
		if err != nil {
			return err
		}
		printStates(states)
		return nil
	},
}

// printStates renders the service snapshot for a single project as an aligned table.
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

type projState struct {
	Project string
	State   protocol.ServiceState
}

// printAllStates renders the service snapshot across all projects with a PROJECT column.
func printAllStates(allStates []projState) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tNAME\tSTATUS\tPID\tRESTARTS\tHEALTH")
	if len(allStates) == 0 {
		_, _ = fmt.Fprintln(w, "(no services)")
		_ = w.Flush()
		return
	}
	for _, ps := range allStates {
		st := ps.State
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
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", ps.Project, st.Name, status, pid, st.Restarts, health)
	}
	_ = w.Flush()
}
