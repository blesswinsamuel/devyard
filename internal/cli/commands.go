package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/blesswinsamuel/devyard/internal/client"
	"github.com/blesswinsamuel/devyard/internal/config"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

func newStartCmd(c *Context) *cobra.Command {
	var follow, build bool
	cmd := &cobra.Command{
		Use:   "start [service...|@group]",
		Short: "Add the project and start all (or the named) services",
		Long: "Adds the project to the project list (capturing this shell's environment for its\n" +
			"services) and starts all services, or just the named ones plus their depends_on\n" +
			"chain. Starts the daemon if needed. `@group` starts every project of a group from\n" +
			"the global config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.ensureDaemon(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			if group, ok := groupArg(args); ok {
				if follow {
					return errors.New("--follow does not work with a group")
				}
				return c.forEachInGroup(ctx, cl, group, "started", func(id string) error {
					// Refresh the launch environment from this shell, as start does.
					if _, err := cl.ReloadProject(ctx, connect.NewRequest(&pb.ReloadProjectRequest{Project: id, Env: captureEnv(), UpdateEnv: true})); err != nil {
						return err
					}
					_, err := cl.StartProject(ctx, connect.NewRequest(&pb.StartProjectRequest{Project: id, Build: build}))
					return err
				})
			}
			id, err := c.startProject(ctx, cl, args, build)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				c.Errorf("devyard: project %q started\n", id)
			} else {
				c.Errorf("devyard: started %s\n", strings.Join(args, ", "))
			}
			if !follow {
				return nil
			}
			return c.followForeground(ctx, cl, id, args)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow logs; Ctrl-C stops the project")
	cmd.Flags().BoolVar(&build, "build", false, "Run build steps before starting")
	return cmd
}

// startProject registers (or re-registers) the local project and starts it.
func (c *Context) startProject(ctx context.Context, cl *client.Client, services []string, build bool) (string, error) {
	if c.Project != "" {
		id, err := c.projectID(ctx, cl)
		if err != nil {
			return "", err
		}
		_, err = cl.StartProject(ctx, connect.NewRequest(&pb.StartProjectRequest{Project: id, Services: services, Build: build}))
		return id, err
	}
	lc, err := c.loadLocalConfig()
	if err != nil {
		return "", err
	}
	resp, err := cl.AddProject(ctx, connect.NewRequest(&pb.AddProjectRequest{
		Path:  lc.Path,
		Env:   captureEnv(),
		Start: len(services) == 0,
		Build: build && len(services) == 0,
	}))
	if err != nil {
		return "", err
	}
	id := resp.Msg.Project.GetId()
	if id == "" {
		id = lc.Project.ID
	}
	if len(services) > 0 {
		if _, err := cl.StartProject(ctx, connect.NewRequest(&pb.StartProjectRequest{Project: id, Services: services, Build: build})); err != nil {
			return id, err
		}
	}
	return id, nil
}

// followForeground streams logs until interrupted (then stops what it
// started) or until every followed service has finished.
func (c *Context) followForeground(ctx context.Context, cl *client.Client, id string, services []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	interrupted := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			close(interrupted)
			cancel()
		case <-ctx.Done():
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.streamLogs(ctx, cl, id, sourcesFor("service", services), 200, true, 0)
	}()
	c.waitAllFinished(ctx, cl, id, services)
	cancel()
	<-done
	select {
	case <-interrupted:
		c.Errorf("\ndevyard: stopping %s…\n", id)
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		if len(services) == 0 {
			_, err := cl.StopProject(stopCtx, connect.NewRequest(&pb.StopProjectRequest{Project: id}))
			return err
		}
		for _, s := range services {
			if _, err := cl.StopService(stopCtx, connect.NewRequest(&pb.StopServiceRequest{Project: id, Service: s})); err != nil {
				return err
			}
		}
	default:
	}
	return nil
}

func finished(status string) bool {
	return status == "stopped" || status == "exited" || status == "failed"
}

// waitAllFinished blocks until ctx ends or every followed service is done.
func (c *Context) waitAllFinished(ctx context.Context, cl *client.Client, id string, services []string) {
	stream, err := cl.Watch(ctx, connect.NewRequest(&pb.WatchRequest{}))
	if err != nil {
		<-ctx.Done()
		return
	}
	defer func() { _ = stream.Close() }()
	svcs := map[string]*pb.Service{}
	check := func() bool {
		any := false
		for name, s := range svcs {
			if len(services) > 0 && !slices.Contains(services, name) {
				continue
			}
			any = true
			if !finished(s.Status) {
				return false
			}
		}
		return any
	}
	for stream.Receive() {
		msg := stream.Msg()
		switch ev := msg.Event.(type) {
		case *pb.WatchResponse_Snapshot:
			svcs = map[string]*pb.Service{}
			for _, s := range ev.Snapshot.Services {
				if s.Project == id {
					svcs[s.Name] = s
				}
			}
		case *pb.WatchResponse_Change:
			if s := ev.Change.GetService(); s != nil && s.Project == id {
				svcs[s.Name] = s
			}
		}
		if check() {
			// Give the log stream a moment to print the final lines.
			time.Sleep(300 * time.Millisecond)
			return
		}
	}
}

func sourcesFor(kind string, names []string) []*pb.LogSource {
	var out []*pb.LogSource
	for _, n := range names {
		out = append(out, &pb.LogSource{Kind: kind, Name: n})
	}
	return out
}

func newStopCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [service...|@group]",
		Short: "Stop the project (it won't autostart) or the named services",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			if group, ok := groupArg(args); ok {
				return c.forEachInGroup(ctx, cl, group, "stopped", func(id string) error {
					_, err := cl.StopProject(ctx, connect.NewRequest(&pb.StopProjectRequest{Project: id}))
					return err
				})
			}
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if _, err := cl.StopProject(ctx, connect.NewRequest(&pb.StopProjectRequest{Project: id})); err != nil {
					return err
				}
				c.Errorf("devyard: project %q stopped\n", id)
				return nil
			}
			for _, s := range args {
				if _, err := cl.StopService(ctx, connect.NewRequest(&pb.StopServiceRequest{Project: id, Service: s})); err != nil {
					return err
				}
				c.Errorf("devyard: stopped %q\n", s)
			}
			return nil
		},
	}
}

func newRestartCmd(c *Context) *cobra.Command {
	var build bool
	cmd := &cobra.Command{
		Use:   "restart [service...|@group]",
		Short: "Restart the project's services or the named services",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			if group, ok := groupArg(args); ok {
				return c.forEachInGroup(ctx, cl, group, "restarted", func(id string) error {
					_, err := cl.RestartProject(ctx, connect.NewRequest(&pb.RestartProjectRequest{Project: id, Build: build}))
					return err
				})
			}
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if _, err := cl.RestartProject(ctx, connect.NewRequest(&pb.RestartProjectRequest{Project: id})); err != nil {
					return err
				}
				c.Errorf("devyard: project %q restarted\n", id)
				return nil
			}
			for _, s := range args {
				if _, err := cl.RestartService(ctx, connect.NewRequest(&pb.RestartServiceRequest{Project: id, Service: s, Build: build})); err != nil {
					return err
				}
				c.Errorf("devyard: restarted %q\n", s)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&build, "build", false, "Rebuild the named services before starting them")
	return cmd
}

func newReloadCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Re-read the config and apply changes (and refresh the launch environment)",
		Long: "Re-reads devyard.yml: added services start, removed ones stop, services whose\n" +
			"command or environment changed restart, and everything else is left alone.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			if _, err := cl.ReloadProject(ctx, connect.NewRequest(&pb.ReloadProjectRequest{Project: id, Env: captureEnv(), UpdateEnv: true})); err != nil {
				return err
			}
			c.Errorf("devyard: project %q reloaded\n", id)
			return nil
		},
	}
}

func newStatusCmd(c *Context) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "status [service...]",
		Aliases: []string{"ps"},
		Short:   "Show service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			project := ""
			if !all {
				if project, err = c.projectID(ctx, cl); err != nil {
					if c.Project != "" {
						return err
					}
					all = true
				}
			}
			snap, err := state(ctx, cl)
			if err != nil {
				return err
			}
			var list []*pb.Service
			for _, s := range snap.Services {
				if (all || s.Project == project) && (len(args) == 0 || slices.Contains(args, s.Name)) {
					list = append(list, s)
				}
			}
			if c.JSON() {
				msgs := make([]proto.Message, len(list))
				for i, s := range list {
					msgs[i] = s
				}
				return c.printProtoJSON(msgs)
			}
			for _, p := range snap.Projects {
				if !all && p.Id != project {
					continue
				}
				if p.Error != "" && !all {
					c.Errorf("devyard: project %q has a config error: %s\n", p.Id, p.Error)
				}
				c.Errorf("%s", driftNote(p))
			}
			return renderServices(c, list, all)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show services of all projects")
	return cmd
}

func newLogsCmd(c *Context) *cobra.Command {
	var follow, previous bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs [service...]",
		Short: "Show (or follow) service logs; merged when several services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.logsCommand(cmd.Context(), "service", args, follow, previous, tail)
		},
	}
	logFlags(cmd, &follow, &previous, &tail)
	return cmd
}

func logFlags(cmd *cobra.Command, follow, previous *bool, tail *int) {
	cmd.Flags().BoolVarP(follow, "follow", "f", false, "Follow new output")
	cmd.Flags().BoolVar(previous, "previous", false, "Show the previous run instead of the current one")
	cmd.Flags().IntVar(tail, "tail", 0, "Number of lines to show (0 = all)")
}

func (c *Context) logsCommand(ctx context.Context, kind string, names []string, follow, previous bool, tail int) error {
	if follow && previous {
		return errors.New("--follow and --previous cannot be combined (the previous run has ended)")
	}
	cl, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	id, err := c.projectID(ctx, cl)
	if err != nil {
		return err
	}
	offset := int64(0)
	if previous {
		offset = -1
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return c.streamLogs(ctx, cl, id, sourcesFor(kind, names), tail, follow, offset)
}

// streamLogs prints a Logs stream. Lines are prefixed with their source
// when more than one source can appear.
func (c *Context) streamLogs(ctx context.Context, cl *client.Client, project string, sources []*pb.LogSource, tail int, follow bool, offset int64) error {
	stream, err := cl.Logs(ctx, connect.NewRequest(&pb.LogsRequest{
		Project: project, Sources: sources, Tail: int32(tail), Follow: follow, RunOffset: offset,
	}))
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	prefixed := len(sources) != 1
	for stream.Receive() {
		for _, l := range stream.Msg().Lines {
			printLogLine(c.Out, l, prefixed)
		}
	}
	if err := stream.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func newTopCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "top [name...]",
		Short: "Show CPU and memory usage of running services and tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			stream, err := cl.Stats(ctx, connect.NewRequest(&pb.StatsRequest{Project: id, IntervalMs: 1000}))
			if err != nil {
				return err
			}
			defer func() { _ = stream.Close() }()
			if !stream.Receive() {
				return stream.Err()
			}
			var stats []*pb.ProcessStat
			for _, s := range stream.Msg().Stats {
				if len(args) == 0 || slices.Contains(args, s.Name) {
					stats = append(stats, s)
				}
			}
			if c.JSON() {
				msgs := make([]proto.Message, len(stats))
				for i, s := range stats {
					msgs[i] = s
				}
				return c.printProtoJSON(msgs)
			}
			return renderStats(c, stats)
		},
	}
}

func newKillCmd(c *Context) *cobra.Command {
	var sig string
	cmd := &cobra.Command{
		Use:   "kill [service...]",
		Short: "Send a signal (default SIGKILL) to services",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			targets := args
			if len(targets) == 0 {
				snap, err := state(ctx, cl)
				if err != nil {
					return err
				}
				for _, s := range snap.Services {
					if s.Project == id && s.Pid > 0 {
						targets = append(targets, s.Name)
					}
				}
			}
			for _, s := range targets {
				if _, err := cl.KillService(ctx, connect.NewRequest(&pb.KillServiceRequest{Project: id, Service: s, Signal: sig})); err != nil {
					return err
				}
				c.Errorf("devyard: sent %s to %q\n", sig, s)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&sig, "signal", "s", "SIGKILL", "Signal to send (e.g. SIGTERM, SIGHUP)")
	return cmd
}

func newWebCmd(c *Context) *cobra.Command {
	var open bool
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Print (or open) the dashboard URL",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.ensureDaemon(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			info, err := cl.Ping(ctx, 2*time.Second)
			if err != nil {
				return err
			}
			url := "http://" + dashboardHost(info.WebAddr)
			c.Printf("%s\n", url)
			if open {
				return openBrowser(url)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&open, "open", false, "Open the dashboard in a browser")
	return cmd
}

// dashboardHost turns a bind address into a browsable host:port.
func dashboardHost(addr string) string {
	host, port, ok := strings.Cut(addr, ":")
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host, port, ok = addr[:i], addr[i+1:], true
	}
	if !ok {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" || host == "::" {
		host = "127.0.0.1"
	}
	return host + ":" + port
}

func newVersionCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			c.Printf("devyard %s\n", Version)
		},
	}
}

func newSchemaCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema of devyard.yml",
		Long: "Print the JSON Schema of devyard.yml. Point your editor at it for completion and\n" +
			"validation, e.g. with a `# yaml-language-server: $schema=<path>` comment.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = c.Out.Write(config.SchemaJSON)
		},
	}
}

func openBrowser(url string) error {
	name := "xdg-open"
	if isDarwin {
		name = "open"
	}
	return runDetached(name, url)
}
