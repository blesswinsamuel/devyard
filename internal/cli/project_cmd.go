package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage local-compose projects",
	Long:  "Commands to list, start, stop, restart, and remove projects by name.",
}

var projectLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all projects managed by the daemon",
	RunE:  lsCmd.RunE,
}

var projectStartCmd = &cobra.Command{
	Use:   "start [project]",
	Short: "Start a project by name or from current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName := flagProject
		if len(args) == 1 {
			projName = args[0]
		}

		cfg, err := loadConfig(flagConfigPath, projName)
		if err != nil {
			return err
		}

		socket, err := ensureDaemon()
		if err != nil {
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()

		if err := client.StartProject(cfg.ConfigPath, false); err != nil {
			return fmt.Errorf("start project %q: %w", cfg.Project, err)
		}
		fmt.Fprintf(os.Stderr, "local-compose: project %q started\n", cfg.Project)
		return nil
	},
}

var projectStopCmd = &cobra.Command{
	Use:   "stop [project]",
	Short: "Stop a project by name or from current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName := flagProject
		if len(args) == 1 {
			projName = args[0]
		}

		cfg, err := loadConfig(flagConfigPath, projName)
		if err != nil && len(args) == 0 {
			return err
		}
		// If project name was explicitly passed in args, use it directly if config loading failed
		targetProject := projName
		if cfg != nil {
			targetProject = cfg.Project
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}
		defer func() { _ = client.Close() }()

		if err := client.StopProject(targetProject); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: project %q stopped\n", targetProject)
			return nil
		}
		fmt.Fprintf(os.Stderr, "local-compose: project %q stopped\n", targetProject)
		return nil
	},
}

var projectRestartCmd = &cobra.Command{
	Use:   "restart [project]",
	Short: "Restart a project by name or from current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName := flagProject
		if len(args) == 1 {
			projName = args[0]
		}

		cfg, err := loadConfig(flagConfigPath, projName)
		if err != nil {
			return err
		}

		socket, err := dialDaemon()
		if err != nil {
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()

		if err := client.Restart(cfg.Project, ""); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "local-compose: project %q restarted\n", cfg.Project)
		return nil
	},
}

var projectRemoveCmd = &cobra.Command{
	Use:     "remove [project]",
	Aliases: []string{"rm"},
	Short:   "Remove a project state by name or from current directory",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName := flagProject
		if len(args) == 1 {
			projName = args[0]
		}

		cfg, err := loadConfig(flagConfigPath, projName)
		if err != nil && len(args) == 0 {
			return err
		}
		targetProject := projName
		if cfg != nil {
			targetProject = cfg.Project
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}
		defer func() { _ = client.Close() }()

		if err := client.RemoveProject(targetProject); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: project %q removed\n", targetProject)
			return nil
		}
		fmt.Fprintf(os.Stderr, "local-compose: project %q removed\n", targetProject)
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectLsCmd)
	projectCmd.AddCommand(projectStartCmd)
	projectCmd.AddCommand(projectStopCmd)
	projectCmd.AddCommand(projectRestartCmd)
	projectCmd.AddCommand(projectRemoveCmd)
}
