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

	// Define command groups
	projectGroup := &cobra.Group{ID: "project", Title: "Project Commands:"}
	serviceGroup := &cobra.Group{ID: "service", Title: "Service Commands:"}
	daemonGroup := &cobra.Group{ID: "daemon", Title: "Daemon & Dashboards:"}

	rootCmd.AddGroup(projectGroup, serviceGroup, daemonGroup)

	lsCmd.GroupID = projectGroup.ID
	upCmd.GroupID = projectGroup.ID
	downCmd.GroupID = projectGroup.ID
	removeCmd.GroupID = projectGroup.ID
	psCmd.GroupID = projectGroup.ID

	logsCmd.GroupID = serviceGroup.ID
	restartCmd.GroupID = serviceGroup.ID
	buildCmd.GroupID = serviceGroup.ID

	tuiCmd.GroupID = daemonGroup.ID
	webCmd.GroupID = daemonGroup.ID
	daemonCmd.GroupID = daemonGroup.ID

	rootCmd.AddCommand(lsCmd, upCmd, downCmd, removeCmd, psCmd)
	rootCmd.AddCommand(logsCmd, restartCmd, buildCmd)
	rootCmd.AddCommand(tuiCmd, webCmd, daemonCmd)
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
