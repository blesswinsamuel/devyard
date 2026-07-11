package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
)

// Execute runs the root command. Before dispatching to cobra it checks for the
// hidden daemon re-exec flags (--supervisor <project> and --daemon) so the
// daemonized child runs the appropriate code path directly, without cobra
// parsing flags it doesn't know about.
func Execute() error {
	if project, rest, ok := supervisorChildArgs(os.Args); ok {
		return runSupervisorChild(project, rest)
	}
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
	rootCmd.PersistentFlags().StringVarP(&flagConfigPath, "file", "f", "", "Path to local-compose.yml (default: walk up from cwd)")
	rootCmd.PersistentFlags().StringVarP(&flagProject, "project", "p", "", "Project name (default: config name or config dir name)")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(startDaemonCmd)
	rootCmd.AddCommand(stopDaemonCmd)
}

// supervisorChildArgs detects the daemon re-exec argv: the binary was invoked
// as `local-compose --supervisor <project> [-f <path>] [--build]`. It returns
// the project name, the remaining flags (config path + build), and whether the
// invocation is a supervisor child.
func supervisorChildArgs(args []string) (project string, rest []string, ok bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == daemon.SupervisorFlag && i+1 < len(args) {
			project = args[i+1]
			rest = append([]string{}, args[i+2:]...)
			return project, rest, true
		}
	}
	return "", nil, false
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

// runSupervisorChild parses the residual flags from the daemon re-exec and
// runs the foreground supervisor. It exits the process on error since the
// daemonized child has no parent to return to.
func runSupervisorChild(project string, rest []string) error {
	configPath := ""
	runBuilds := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "-f", "--file":
			if i+1 < len(rest) {
				configPath = rest[i+1]
				i++
			}
		case "--build":
			runBuilds = true
		}
	}
	if err := runSupervisor(configPath, project, false, runBuilds); err != nil {
		fmt.Fprintf(os.Stderr, "local-compose: supervisor exited: %v\n", err)
		return err
	}
	return nil
}
