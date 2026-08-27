package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List projects",
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

		projects, err := client.ListProjects()
		if err != nil {
			return err
		}
		printProjects(projects)
		return nil
	},
}

func printProjects(projects []*protocol.ProjectInfo) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tSERVICES\tCONFIG PATH")
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(w, "(no projects)")
		_ = w.Flush()
		return
	}
	for _, p := range projects {
		svcSummary := fmt.Sprintf("%d/%d running", p.RunningServices, p.TotalServices)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.Status, svcSummary, p.ConfigPath)
	}
	_ = w.Flush()
}
