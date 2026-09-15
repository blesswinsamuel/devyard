package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTaskCmd(ctx *CLIContext) *cobra.Command {
	taskCmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"t"},
		Short:   "Manage and execute one-off tasks",
	}

	taskCmd.AddCommand(
		newTaskListCmd(ctx),
		newTaskRunCmd(ctx),
		newTaskStopCmd(ctx),
		newTaskKillCmd(ctx),
		newTaskLogsCmd(ctx),
		newTaskTopCmd(ctx),
	)

	return taskCmd
}

func newTaskListCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List defined tasks in the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.LoadConfig()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				// Offline fallback: list from config file
				if len(cfg.File.Tasks) == 0 {
					ctx.Println("No tasks defined.")
					return nil
				}
				w := ctx.NewTabWriter()
				_, _ = fmt.Fprintln(w, "TASK\tCOMMAND")
				for name, t := range cfg.File.Tasks {
					_, _ = fmt.Fprintf(w, "%s\t%s\n", name, t.Spec.Command)
				}
				return w.Flush()
			}
			defer func() { _ = client.Close() }()

			tasks, err := client.ListTasks(cfg.Project)
			if err != nil {
				return err
			}
			return renderTasksHelper(ctx, tasks)
		},
	}
}

// ExitCodeError wraps a process exit code without immediately calling os.Exit.
type ExitCodeError struct {
	Code int
}

func (e *ExitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func newTaskRunCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "run <task> [args...]",
		Short: "Run a one-off task defined in local-compose.yml",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskName := args[0]
			extraArgs := args[1:]

			cfg, err := ctx.LoadConfig()
			if err != nil {
				return err
			}

			client, err := ctx.EnsureDaemon()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if err := client.StartProject(cfg.ConfigPath, false, ctx.EnvFile, true); err != nil {
				return fmt.Errorf("start project: %w", err)
			}

			exitCode, err := client.RunTask(cmd.Context(), cfg.Project, taskName, extraArgs, func(line string) {
				_, _ = fmt.Fprintln(ctx.Out, line)
			})
			if err != nil {
				return err
			}
			if exitCode != 0 {
				return &ExitCodeError{Code: exitCode}
			}
			return nil
		},
	}
}

func newTaskStopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <task>",
		Short: "Stop a running task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskName := args[0]
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			return runStopHelper(ctx, client, projName, taskName, true)
		},
	}
}

func newTaskKillCmd(ctx *CLIContext) *cobra.Command {
	var killSignal string

	cmd := &cobra.Command{
		Use:   "kill <task>",
		Short: "Forcefully terminate a running task's process group with a signal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskName := args[0]
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			return runKillHelper(ctx, client, projName, taskName, killSignal)
		},
	}

	cmd.Flags().StringVarP(&killSignal, "signal", "s", "SIGKILL", "Signal to send (e.g. SIGTERM, SIGINT, SIGHUP)")
	return cmd
}

func newTaskLogsCmd(ctx *CLIContext) *cobra.Command {
	var follow bool
	var previous bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs <task>",
		Short: "View or stream logs for a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskName := args[0]
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return err
			}
			defer func() { _ = client.Close() }()

			return streamLogsHelper(cmd.Context(), ctx, client, projName, taskName, true, follow, previous, tail)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs in real time")
	cmd.Flags().BoolVar(&previous, "previous", false, "Inspect the immediately preceding run's log")
	cmd.Flags().IntVar(&tail, "tail", 0, "Number of lines to show from the end of logs (0 = all)")
	return cmd
}

func newTaskTopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "top [task]",
		Short: "Show CPU/memory usage of running tasks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			taskName := ""
			if len(args) == 1 {
				taskName = args[0]
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("local-compose: no daemon running")
				return err
			}
			defer func() { _ = client.Close() }()

			stats, err := client.Top(projName, taskName)
			if err != nil {
				return err
			}
			return renderTopHelper(ctx, stats)
		},
	}
}
