package cli

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/protocol"
)

func newServiceCmd(ctx *CLIContext) *cobra.Command {
	serviceCmd := &cobra.Command{
		Use:     "service",
		Aliases: []string{"svc", "s"},
		Short:   "Manage and inspect supervised services",
	}

	serviceCmd.AddCommand(
		newServiceListCmd(ctx),
		newServiceStartCmd(ctx),
		newServiceStopCmd(ctx),
		newServiceRestartCmd(ctx),
		newServiceKillCmd(ctx),
		newServiceLogsCmd(ctx),
		newServiceTopCmd(ctx),
		newServiceBuildCmd(ctx),
	)

	return serviceCmd
}

func newServiceListCmd(ctx *CLIContext) *cobra.Command {
	var showAll bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ps"},
		Short:   "List services and their statuses",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			var projName string
			if !showAll {
				p, err := ctx.ResolveProjectName()
				if err != nil {
					showAll = true
				} else {
					projName = p
				}
			}

			states, err := client.List(projName)
			if err != nil {
				return err
			}
			var urls map[string][]string
			if !showAll {
				if cfg, err := ctx.LoadConfig(); err == nil {
					urls = proxyURLsFor(cfg, projName)
				}
			}
			return renderStatesHelper(ctx, states, showAll, urls)
		},
	}

	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "Show services across all projects")
	return cmd
}

func newServiceStartCmd(ctx *CLIContext) *cobra.Command {
	var build bool
	var follow bool
	var removeOrphans = true

	cmd := &cobra.Command{
		Use:   "start [service...]",
		Short: "Start one or more services (or all services in the project)",
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

			if len(args) == 0 {
				if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, removeOrphans); err != nil {
					return err
				}
				ctx.Errorf("devyard: project %q started\n", cfg.Project)
				if !follow {
					return nil
				}
				return followForegroundTo(ctx, cfg.Project, cfg.Order)
			}

			for _, svc := range args {
				if err := client.StartService(cfg.Project, svc); err != nil {
					return err
				}
				ctx.Errorf("devyard: started %q\n", svc)
			}
			if !follow {
				return nil
			}
			return followForegroundTo(ctx, cfg.Project, args)
		},
	}

	cmd.Flags().BoolVar(&build, "build", false, "Run build commands before starting")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow logs in real time after starting")
	cmd.Flags().BoolVar(&removeOrphans, "remove-orphans", true, "Stop orphan services not declared in config")
	return cmd
}

func newServiceStopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [service...]",
		Short: "Stop one or more services (or all services in the project)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running")
				return nil
			}
			defer func() { _ = client.Close() }()

			if len(args) == 0 {
				return runStopHelper(ctx, client, projName, "", false)
			}
			for _, svc := range args {
				if err := runStopHelper(ctx, client, projName, svc, false); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newServiceRestartCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "restart [service...]",
		Short: "Restart one or more services (or all services in the project)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			if len(args) == 0 {
				if err := client.Restart(projName, ""); err != nil {
					return err
				}
				ctx.Errorln("devyard: restarted all services")
				return nil
			}

			for _, svc := range args {
				if err := client.Restart(projName, svc); err != nil {
					return err
				}
				ctx.Errorf("devyard: restarted %q\n", svc)
			}
			return nil
		},
	}
}

func newServiceKillCmd(ctx *CLIContext) *cobra.Command {
	var killSignal string

	cmd := &cobra.Command{
		Use:   "kill [service...]",
		Short: "Forcefully terminate one or more services with a signal",
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			if len(args) == 0 {
				return runKillHelper(ctx, client, projName, "", killSignal)
			}

			for _, svc := range args {
				if err := runKillHelper(ctx, client, projName, svc, killSignal); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&killSignal, "signal", "s", "SIGKILL", "Signal to send (e.g. SIGTERM, SIGINT, SIGHUP)")
	return cmd
}

func newServiceLogsCmd(ctx *CLIContext) *cobra.Command {
	var follow bool
	var previous bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "View or stream logs for a service or all services",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			service := ""
			if len(args) == 1 {
				service = args[0]
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			return streamLogsHelper(cmd.Context(), ctx, client, projName, service, false, follow, previous, tail)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs in real time")
	cmd.Flags().BoolVar(&previous, "previous", false, "Inspect the immediately preceding run's log")
	cmd.Flags().IntVar(&tail, "tail", 0, "Number of lines to show from the end of logs (0 = all)")
	return cmd
}

func newServiceTopCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "top [service]",
		Short: "Show CPU/memory usage of running services",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projName, err := ctx.ResolveProjectName()
			if err != nil {
				return err
			}

			service := ""
			if len(args) == 1 {
				service = args[0]
			}

			client, err := ctx.DialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}
			defer func() { _ = client.Close() }()

			stats, err := client.Top(projName, service)
			if err != nil {
				return err
			}
			return renderTopHelper(ctx, stats)
		},
	}
}

func newServiceBuildCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "build [service...]",
		Short: "Run build commands for services that declare them",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.LoadConfig()
			if err != nil {
				return err
			}
			return runBuilds(cfg, args...)
		},
	}
}

func followForegroundTo(ctx *CLIContext, project string, order []string) error {
	socket, err := daemonSocketPath()
	if err != nil {
		return err
	}

	logsCtx, cancelLogs := context.WithCancel(context.Background())
	defer cancelLogs()

	stop := make(chan struct{})
	stopOnce := sync.Once{}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			stopOnce.Do(func() { close(stop) })
			cancelLogs()
			c, err := control.Dial(socket)
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.StopProject(project)
		case <-stop:
		}
	}()

	client, err := control.Dial(socket)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	for _, svc := range order {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = client.LogsCtx(logsCtx, project, name, true, false, protocol.DefaultLogTail, newLogPrinterTo(ctx.Out, name))
		}(svc)
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			cancelLogs()
			wg.Wait()
			return nil
		case <-ticker.C:
			states, err := client.List(project)
			if err != nil {
				continue
			}
			allDone := true
			for _, s := range states {
				if s.Status != "exited" && s.Status != "stopped" {
					allDone = false
					break
				}
			}
			if allDone {
				stopOnce.Do(func() { close(stop) })
				cancelLogs()
				wg.Wait()
				return nil
			}
		}
	}
}
