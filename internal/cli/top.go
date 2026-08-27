package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var topCmd = &cobra.Command{
	Use:   "top [service]",
	Short: "Show CPU/memory usage of running services",
	Long: "Show CPU and memory usage of the process groups managed by the " +
		"daemon. CPU is a percentage of one core averaged over a one-second " +
		"sampling interval (it can exceed 100 for multi-core work); memory is " +
		"the aggregate resident set size of every process in a service's " +
		"process group.",
	Args: cobra.MaximumNArgs(1),
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

		stats, err := client.Top(projName, service)
		if err != nil {
			return err
		}
		printTop(stats)
		return nil
	},
}

// printTop renders the per-service resource snapshot as an aligned table.
// Services without a live process group show "-" for the stats columns.
func printTop(stats []*protocol.ServiceStat) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tPROCS\tCPU%\tMEM")
	if len(stats) == 0 {
		_, _ = fmt.Fprintln(w, "(no services)")
		_ = w.Flush()
		return
	}
	for _, st := range stats {
		pid := "-"
		if st.Pid > 0 {
			pid = fmt.Sprintf("%d", st.Pid)
		}
		procs, cpu, mem := "-", "-", "-"
		if st.Procs > 0 {
			procs = fmt.Sprintf("%d", st.Procs)
			cpu = fmt.Sprintf("%.1f", st.Cpu)
			mem = fmtBytes(st.RssBytes)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", st.Name, st.Status, pid, procs, cpu, mem)
	}
	_ = w.Flush()
}

// fmtBytes renders a byte count in human-readable binary units (KiB, MiB, ...).
func fmtBytes(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
