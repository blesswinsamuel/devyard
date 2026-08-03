package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
)

// Execute runs the root command. Before dispatching to cobra it checks for the
// hidden daemon re-exec flag (--daemon) so the daemonized child runs the global
// daemon code path directly, without cobra parsing flags it doesn't know about.
func Execute() error {
	if isDaemonChild(os.Args) {
		return runDaemonChild()
	}
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "local-compose",
	Short: "Orchestrate local processes (docker-compose-style, no Docker)",
	Long:  "local-compose is a CLI that orchestrates local processes with a compose-inspired config file.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagConfigPath, "file", "", "Path to local-compose.yml (default: walk up from cwd)")
	rootCmd.PersistentFlags().StringVarP(&flagProject, "project", "p", "", "Project name (default: config name or config dir name)")

	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(psCmd)

	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(buildCmd)

	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(webCmd)
	rootCmd.AddCommand(daemonCmd)

	cobra.AddTemplateFunc("commandGroups", func() []struct {
		Title    string
		Commands []*cobra.Command
	} {
		return []struct {
			Title    string
			Commands []*cobra.Command
		}{
			{
				Title:    "Project Commands:",
				Commands: []*cobra.Command{lsCmd, upCmd, downCmd, removeCmd, psCmd},
			},
			{
				Title:    "Service Commands:",
				Commands: []*cobra.Command{logsCmd, restartCmd, buildCmd},
			},
			{
				Title:    "Daemon & Dashboards:",
				Commands: []*cobra.Command{tuiCmd, webCmd, daemonCmd},
			},
		}
	})

	rootCmd.SetHelpTemplate(`{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}Usage:
  {{.UseLine}}{{if .HasAvailableSubCommands}} [command]{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{range commandGroups}}{{.Title}}
{{range .Commands}}  {{rpad .Name .NamePadding}} {{.Short}}
{{end}}
{{end}}{{end}}{{if .HasHelpSubCommands}}
Additional help topics:
{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}
`)
}

// isDaemonChild reports whether the binary was invoked as
// `local-compose --daemon` (the daemonized global daemon child).
func isDaemonChild(args []string) bool {
	for _, a := range args {
		if a == daemon.DaemonFlag {
			return true
		}
	}
	return false
}
