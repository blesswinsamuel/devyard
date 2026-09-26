package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/protocol"
	"github.com/blesswinsamuel/devyard/internal/proxy"
	"github.com/blesswinsamuel/devyard/internal/supervisor"
	"github.com/blesswinsamuel/devyard/internal/ui"
)

type logEntry struct {
	timestamp time.Time
	service   string
	line      string
}

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

// renderStatesHelper formats and displays service states as JSON or an aligned table.
// urls maps service name → proxy host:port list (nil omits the column).
func renderStatesHelper(ctx *CLIContext, states []*protocol.ServiceState, showProject bool, urls map[string][]string) error {
	if ctx.IsJSON() {
		return ctx.PrintJSON(states)
	}
	if urls == nil {
		for _, st := range states {
			if len(st.ProxyUrls) > 0 {
				if urls == nil {
					urls = make(map[string][]string)
				}
				clean := make([]string, len(st.ProxyUrls))
				for i, u := range st.ProxyUrls {
					clean[i] = strings.TrimPrefix(u, "http://")
				}
				urls[st.Name] = clean
			}
		}
	}
	w := ctx.NewTabWriter()
	showURLs := len(urls) > 0
	if showProject {
		if showURLs {
			_, _ = fmt.Fprintln(w, "PROJECT\tNAME\tSTATUS\tPID\tRESTARTS\tHEALTH\tURLS")
		} else {
			_, _ = fmt.Fprintln(w, "PROJECT\tNAME\tSTATUS\tPID\tRESTARTS\tHEALTH")
		}
	} else {
		if showURLs {
			_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tRESTARTS\tHEALTH\tURLS")
		} else {
			_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tRESTARTS\tHEALTH")
		}
	}
	if len(states) == 0 {
		_, _ = fmt.Fprintln(w, "(no services)")
		return w.Flush()
	}
	for _, st := range states {
		pid := "-"
		if st.Pid > 0 {
			pid = fmt.Sprintf("%d", st.Pid)
		}
		health := "-"
		if st.HasHealth {
			health = st.Health
		}
		status := st.Status
		if st.Status == "exited" {
			status = fmt.Sprintf("exited (%d)", st.ExitCode)
		}
		if showURLs {
			urls := strings.Join(urls[st.Name], ", ")
			if showProject {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", st.Project, st.Name, status, pid, st.Restarts, health, urls)
			} else {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", st.Name, status, pid, st.Restarts, health, urls)
			}
			continue
		}
		if showProject {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", st.Project, st.Name, status, pid, st.Restarts, health)
		} else {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", st.Name, status, pid, st.Restarts, health)
		}
	}
	return w.Flush()
}

// proxyURLsFor returns the proxy URL (host:port) for every service that
// exposes ports in cfg, keyed by service name. Empty when no service is
// exposed or the global config cannot be loaded.
func proxyURLsFor(cfg *loadedConfig, project string) map[string][]string {
	gcfg, err := globalconfig.Load()
	if err != nil {
		return nil
	}
	if cfg.File == nil {
		return nil
	}
	out := make(map[string][]string)
	for name, svc := range cfg.File.Services {
		if svc.Ports == nil || len(svc.Ports.Entries) == 0 {
			continue
		}
		isDefault := cfg.File.Proxy != nil && cfg.File.Proxy.DefaultService == name
		hosts := proxy.ServiceHosts(project, name, svc, isDefault, gcfg.Proxy.DomainSuffix)
		if len(hosts) == 0 {
			continue
		}
		list := make([]string, 0, len(hosts))
		for _, host := range hosts {
			list = append(list, fmt.Sprintf("%s:%d", host, gcfg.Proxy.Port))
		}
		out[name] = list
	}
	return out
}

// renderTasksHelper formats and displays task states as JSON or an aligned table.
func renderTasksHelper(ctx *CLIContext, tasks []*protocol.TaskState) error {
	if ctx.IsJSON() {
		return ctx.PrintJSON(tasks)
	}
	w := ctx.NewTabWriter()
	_, _ = fmt.Fprintln(w, "TASK\tSTATUS\tPID\tCOMMAND")
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(w, "(no tasks defined)")
		return w.Flush()
	}
	for _, t := range tasks {
		pid := "-"
		if t.Pid > 0 {
			pid = fmt.Sprintf("%d", t.Pid)
		}
		status := t.Status
		if status == "" {
			status = "idle"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, status, pid, t.Command)
	}
	return w.Flush()
}

// renderProjectsHelper formats and displays project info as JSON or an aligned table.
func renderProjectsHelper(ctx *CLIContext, projects []*protocol.ProjectInfo) error {
	if ctx.IsJSON() {
		return ctx.PrintJSON(projects)
	}
	w := ctx.NewTabWriter()
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tSERVICES\tCONFIG PATH")
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(w, "(no projects)")
		return w.Flush()
	}
	for _, p := range projects {
		svcSummary := fmt.Sprintf("%d/%d running", p.RunningServices, p.TotalServices)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.Status, svcSummary, p.ConfigPath)
	}
	return w.Flush()
}

// renderTopHelper formats and displays CPU/memory resource usage as JSON or an aligned table.
func renderTopHelper(ctx *CLIContext, stats []*protocol.ServiceStat) error {
	if ctx.IsJSON() {
		return ctx.PrintJSON(stats)
	}
	w := ctx.NewTabWriter()
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPID\tPROCS\tCPU%\tMEM")
	if len(stats) == 0 {
		_, _ = fmt.Fprintln(w, "(no active processes)")
		return w.Flush()
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
	return w.Flush()
}

// runKillHelper sends a termination signal to a service or task process group.
func runKillHelper(ctx *CLIContext, client *control.Client, projName, target, signal string) error {
	if signal == "" {
		signal = "SIGKILL"
	}
	if err := client.KillService(projName, target, signal); err != nil {
		return err
	}
	if target == "" {
		ctx.Errorf("devyard: killed all services (%s)\n", signal)
	} else {
		ctx.Errorf("devyard: killed %q (%s)\n", target, signal)
	}
	return nil
}

// runStopHelper stops a service, task, or entire project gracefully.
func runStopHelper(ctx *CLIContext, client *control.Client, projName, target string, isTask bool) error {
	if target == "" {
		if err := client.StopProject(projName); err != nil {
			ctx.Errorln("devyard: stopped")
			return nil
		}
		ctx.Errorln("devyard: stopped")
		return nil
	}
	if isTask {
		if err := client.StopTask(projName, target); err != nil {
			return err
		}
		ctx.Errorf("devyard: stopped task %q\n", target)
		return nil
	}
	if err := client.StopService(projName, target); err != nil {
		return err
	}
	ctx.Errorf("devyard: stopped service %q\n", target)
	return nil
}

// streamLogsHelper handles unified log streaming for a service, task, or full project.
func streamLogsHelper(cmdCtx context.Context, ctx *CLIContext, client *control.Client, projName, target string, isTask, follow, previous bool, tail int) error {
	if follow && previous {
		return fmt.Errorf("cannot follow previous logs (the previous run has already ended)")
	}

	printer := newLogPrinterTo(ctx.Out, target)

	if isTask {
		return client.TaskLogsCtx(cmdCtx, projName, target, follow, previous, tail, printer)
	}

	if target != "" {
		return client.LogsCtx(cmdCtx, projName, target, follow, previous, tail, printer)
	}

	// Project-wide log follow
	cfg, err := ctx.LoadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Order) == 0 {
		return fmt.Errorf("no services defined in config")
	}
	if len(cfg.Order) == 1 {
		svc := cfg.Order[0]
		return client.LogsCtx(cmdCtx, cfg.Project, svc, follow, previous, tail, newLogPrinterTo(ctx.Out, svc))
	}

	socketPath, err := daemonSocketPath()
	if err != nil {
		return err
	}

	if follow {
		return logsAllFollowTo(cmdCtx, ctx.Out, socketPath, cfg.Project, cfg.Order, tail)
	}
	return logsAllSnapshotTo(cmdCtx, ctx.Out, socketPath, cfg.Project, cfg.Order, tail, previous)
}

func newLogPrinterTo(w io.Writer, name string) func(string) {
	prefix := ""
	if name != "" {
		prefix = ui.ServicePrefix(name) + " │ "
	}
	return func(line string) {
		_, _ = fmt.Fprintln(w, prefix+ui.CleanLogLine(line))
	}
}

func parseLogTimestamp(line string) (time.Time, string) {
	return supervisor.ParseTimestamp(line)
}

func logsAllFollowTo(ctx context.Context, out io.Writer, socket, project string, order []string, tail int) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(order))

	for _, svc := range order {
		wg.Add(1)
		go func(service string) {
			defer wg.Done()
			c, err := control.Dial(socket)
			if err != nil {
				errCh <- err
				return
			}
			defer func() { _ = c.Close() }()
			p := newLogPrinterTo(out, service)
			_ = c.LogsCtx(ctx, project, service, true, false, tail, p)
		}(svc)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func logsAllSnapshotTo(ctx context.Context, out io.Writer, socket, project string, order []string, tail int, previous bool) error {
	var mu sync.Mutex
	var entries []logEntry

	var wg sync.WaitGroup
	errCh := make(chan error, len(order))

	for _, svc := range order {
		wg.Add(1)
		go func(service string) {
			defer wg.Done()
			c, err := control.Dial(socket)
			if err != nil {
				errCh <- err
				return
			}
			defer func() { _ = c.Close() }()

			collector := func(line string) {
				ts, clean := parseLogTimestamp(line)
				mu.Lock()
				entries = append(entries, logEntry{timestamp: ts, service: service, line: clean})
				mu.Unlock()
			}
			_ = c.LogsCtx(ctx, project, service, false, previous, tail, collector)
		}(svc)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].timestamp.Before(entries[j].timestamp)
	})

	for _, e := range entries {
		newLogPrinterTo(out, e.service)(e.line)
	}
	return nil
}
