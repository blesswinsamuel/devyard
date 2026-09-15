package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
)

// Execute runs the root command using standard OS streams and environment.
// Before dispatching to cobra it checks for the hidden daemon re-exec flag
// (--daemon) or shim re-exec flag (--shim).
func Execute() error {
	if isDaemonChild(os.Args) {
		return runDaemonChild()
	}
	if isShimChild(os.Args) {
		return runShimChild(os.Args)
	}

	cliCtx := NewDefaultCLIContext()
	err := ExecuteContext(context.Background(), os.Args[1:], cliCtx)
	if err != nil {
		var exitErr *ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		cliCtx.Errorf("Error: %v\n", err)
		return err
	}
	return nil
}

// ExecuteContext executes the CLI command tree with a custom context and streams.
func ExecuteContext(ctx context.Context, args []string, cliCtx *CLIContext) error {
	cmd := NewRootCommand(cliCtx)
	cmd.SetArgs(args)
	cmd.SetIn(cliCtx.In)
	cmd.SetOut(cliCtx.Out)
	cmd.SetErr(cliCtx.Err)
	return cmd.ExecuteContext(ctx)
}

// NewRootCommand creates the root cobra command tree bound to the given CLIContext.
func NewRootCommand(ctx *CLIContext) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "local-compose",
		Short:         "Orchestrate local processes with a compose-style config (no Docker)",
		Long:          "local-compose is a CLI that orchestrates local processes, services, tasks, and multi-project daemons.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}

	// Global persistent flags
	rootCmd.PersistentFlags().StringVar(&ctx.ConfigPath, "file", "", "Path to local-compose.yml (default: walk up from cwd)")
	rootCmd.PersistentFlags().StringVarP(&ctx.Project, "project", "p", "", "Resolve a registered project by name when no config file is found (default: config name)")
	rootCmd.PersistentFlags().StringVar(&ctx.EnvFile, "env-file", "", "Path to an env file for variables and config interpolation (default: .env next to the config file)")
	rootCmd.PersistentFlags().StringVarP(&ctx.Format, "format", "o", "table", "Output format (table, json)")

	// Define command groups
	shortcutGroup := &cobra.Group{ID: "shortcuts", Title: "Daily Shortcuts:"}
	resourceGroup := &cobra.Group{ID: "resources", Title: "Resource Management:"}
	daemonGroup := &cobra.Group{ID: "daemon", Title: "Daemon & Dashboard:"}

	rootCmd.AddGroup(shortcutGroup, resourceGroup, daemonGroup)

	// Resource Commands
	projectCmd := newProjectCmd(ctx)
	projectCmd.GroupID = resourceGroup.ID

	serviceCmd := newServiceCmd(ctx)
	serviceCmd.GroupID = resourceGroup.ID

	taskCmd := newTaskCmd(ctx)
	taskCmd.GroupID = resourceGroup.ID

	daemonCmd := newDaemonCmd(ctx)
	daemonCmd.GroupID = daemonGroup.ID

	uiCmd := newUICmd(ctx)
	uiCmd.GroupID = daemonGroup.ID

	versionCmd := newVersionCmd(ctx)

	rootCmd.AddCommand(projectCmd, serviceCmd, taskCmd, daemonCmd, uiCmd, versionCmd)

	// Top-Level Shortcuts
	startShortcut := newServiceStartCmd(ctx)
	startShortcut.GroupID = shortcutGroup.ID

	stopShortcut := newServiceStopCmd(ctx)
	stopShortcut.GroupID = shortcutGroup.ID

	restartShortcut := newServiceRestartCmd(ctx)
	restartShortcut.GroupID = shortcutGroup.ID

	reloadShortcut := newProjectReloadCmd(ctx)
	reloadShortcut.GroupID = shortcutGroup.ID

	statusShortcut := newServiceListCmd(ctx)
	statusShortcut.Use = "status [service...]"
	statusShortcut.Aliases = []string{"ps"}
	statusShortcut.GroupID = shortcutGroup.ID

	logsShortcut := newServiceLogsCmd(ctx)
	logsShortcut.GroupID = shortcutGroup.ID

	topShortcut := newServiceTopCmd(ctx)
	topShortcut.GroupID = shortcutGroup.ID

	runShortcut := newTaskRunCmd(ctx)
	runShortcut.GroupID = shortcutGroup.ID

	killShortcut := newServiceKillCmd(ctx)
	killShortcut.GroupID = shortcutGroup.ID

	buildShortcut := newBuildCmd(ctx)
	buildShortcut.GroupID = shortcutGroup.ID

	// Transition aliases for docker-compose familiarity
	upAlias := newUpCmd(ctx)
	downAlias := newProjectStopCmd(ctx)
	downAlias.Use = "down"
	downAlias.Hidden = true

	lsAlias := newProjectListCmd(ctx)
	lsAlias.Use = "ls"
	lsAlias.Aliases = nil
	lsAlias.Hidden = true

	removeAlias := newProjectRemoveCmd(ctx)
	removeAlias.Hidden = true

	rootCmd.AddCommand(
		startShortcut,
		stopShortcut,
		restartShortcut,
		reloadShortcut,
		statusShortcut,
		logsShortcut,
		topShortcut,
		killShortcut,
		runShortcut,
		buildShortcut,
		upAlias,
		downAlias,
		lsAlias,
		removeAlias,
	)

	return rootCmd
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

func newUpCmd(ctx *CLIContext) *cobra.Command {
	var detach bool
	var build bool
	var removeOrphans = true

	cmd := &cobra.Command{
		Use:    "up",
		Short:  "Start services defined in local-compose.yml",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.LoadConfig()
			if err != nil {
				return err
			}

			if build {
				if err := runAllBuilds(cfg); err != nil {
					return err
				}
			}

			client, err := ctx.EnsureDaemon()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, removeOrphans); err != nil {
				return err
			}
			ctx.Errorf("local-compose: project %q started\n", cfg.Project)

			if detach {
				return nil
			}

			return followForegroundTo(ctx, cfg.Project, cfg.Order)
		},
	}

	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Detached mode: Run processes in the background")
	cmd.Flags().BoolVar(&build, "build", false, "Build services before starting")
	cmd.Flags().BoolVar(&removeOrphans, "remove-orphans", true, "Stop orphan services not declared in config")
	return cmd
}
