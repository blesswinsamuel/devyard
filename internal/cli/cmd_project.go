package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newProjectCmd(ctx *CLIContext) *cobra.Command {
	projectCmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"proj", "p"},
		Short:   "Manage projects registered with the daemon",
	}

	projectCmd.AddCommand(
		newProjectListCmd(ctx),
		newProjectAddCmd(ctx),
		newProjectReloadCmd(ctx),
		newProjectStartCmd(ctx),
		newProjectStopCmd(ctx),
		newProjectRestartCmd(ctx),
		newProjectRemoveCmd(ctx),
		newProjectLogsCmd(ctx),
	)

	return projectCmd
}

func newProjectListCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all projects managed by the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			projects, err := client.ListProjects()
			if err != nil {
				return err
			}
			return renderProjectsHelper(ctx, projects)
		},
	}
}

func newProjectAddCmd(ctx *CLIContext) *cobra.Command {
	var startImmediately bool

	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a project config with the daemon",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}

			client, err := ctx.EnsureDaemon()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.StartProject(cfgPath, false, ctx.EnvFile, true); err != nil {
				return fmt.Errorf("register project: %w", err)
			}

			if !startImmediately {
				cfg, loadErr := loadConfigWith(cfgPath, "", ctx.EnvFile)
				if loadErr == nil {
					_ = client.StopProject(cfg.Project)
				}
			}

			ctx.Errorf("local-compose: registered project from %s\n", cfgPath)
			return nil
		},
	}

	cmd.Flags().BoolVar(&startImmediately, "start", true, "Start project services immediately upon registration")
	return cmd
}

func newProjectReloadCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "reload [project]",
		Short: "Re-read config from disk and reconcile running services in place",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}

			cfg, err := loadConfigWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, true); err != nil {
				return fmt.Errorf("reload project: %w", err)
			}

			ctx.Errorf("local-compose: reloaded project %q\n", cfg.Project)
			return nil
		},
	}
}

func newProjectStartCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "start [project]",
		Short: "Start all services in a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}

			cfg, err := loadConfigWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.EnsureDaemon()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, true); err != nil {
				return err
			}
			ctx.Errorf("local-compose: project %q started\n", cfg.Project)
			return nil
		},
	}
}

func newProjectStopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [project]",
		Short: "Stop all services in a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}
			projName, err := resolveProjectNameWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			return runStopHelper(ctx, client, projName, "", false)
		},
	}
}

func newProjectRestartCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "restart [project]",
		Short: "Restart all services in a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}
			projName, err := resolveProjectNameWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.Restart(projName, ""); err != nil {
				return err
			}
			ctx.Errorf("local-compose: restarted project %q\n", projName)
			return nil
		},
	}
}

func newProjectRemoveCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:     "remove [project]",
		Aliases: []string{"rm"},
		Short:   "Stop and completely remove a project from the daemon",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}
			projName, err := resolveProjectNameWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			if err := client.RemoveProject(projName); err != nil {
				ctx.Errorln("local-compose: removed")
				return nil
			}
			ctx.Errorln("local-compose: removed")
			return nil
		},
	}
}

func newProjectLogsCmd(ctx *CLIContext) *cobra.Command {
	var follow bool
	var previous bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs [project]",
		Short: "Combined log stream across all services in the project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proj := ctx.Project
			if len(args) == 1 {
				proj = args[0]
			}
			projName, err := resolveProjectNameWith(ctx.ConfigPath, proj, ctx.EnvFile)
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return err
			}
			defer func() { _ = client.Close() }()

			return streamLogsHelper(cmd.Context(), ctx, client, projName, "", false, follow, previous, tail)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs in real time")
	cmd.Flags().BoolVar(&previous, "previous", false, "Inspect the immediately preceding run's log")
	cmd.Flags().IntVar(&tail, "tail", 0, "Number of lines to show from the end of logs (0 = all)")

	return cmd
}
