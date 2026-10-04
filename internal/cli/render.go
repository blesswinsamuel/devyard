package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/ui"
)

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

func fmtUptime(startedMs int64) string {
	if startedMs == 0 {
		return "-"
	}
	d := time.Since(time.UnixMilli(startedMs)).Round(time.Second)
	switch {
	case d < time.Minute:
		return d.String()
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func serviceStatus(s *pb.Service) string {
	switch s.Status {
	case "exited":
		return fmt.Sprintf("exited (%d)", s.ExitCode)
	case "running":
		if s.Health != "" {
			return "running (" + s.Health + ")"
		}
	}
	return s.Status
}

func renderServices(c *Context, list []*pb.Service, showProject bool) error {
	w := c.table()
	header := []string{"NAME", "STATUS", "PID", "UPTIME", "RESTARTS", "URLS"}
	if showProject {
		header = append([]string{"PROJECT"}, header...)
	}
	_, _ = fmt.Fprintln(w, strings.Join(header, "\t"))
	if len(list) == 0 {
		_, _ = fmt.Fprintln(w, "(no services)")
		return w.Flush()
	}
	for _, s := range list {
		pid, uptime := "-", "-"
		if s.Pid > 0 {
			pid = fmt.Sprint(s.Pid)
		}
		if s.Status == "running" {
			uptime = fmtUptime(s.StartedAtUnixMs)
		}
		status := serviceStatus(s)
		if s.Message != "" && s.Status != "running" {
			status += " · " + s.Message
		}
		row := []string{s.Name, status, pid, uptime, fmt.Sprint(s.Restarts), strings.Join(s.Urls, " ")}
		if showProject {
			row = append([]string{s.Project}, row...)
		}
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

func renderProjects(c *Context, list []*pb.Project) error {
	w := c.table()
	_, _ = fmt.Fprintln(w, "PROJECT\tSTATUS\tSERVICES\tCONFIG")
	if len(list) == 0 {
		_, _ = fmt.Fprintln(w, "(no projects)")
		return w.Flush()
	}
	for _, p := range sortedProjects(list) {
		status := p.Status
		if p.Error != "" {
			status += " · " + p.Error
		}
		services := fmt.Sprintf("%d/%d running", p.ServicesRunning, p.ServicesTotal)
		if !p.HasConfig {
			services = "no devyard.yml"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Id, status, services, p.ConfigPath)
	}
	return w.Flush()
}

func renderTasks(c *Context, list []*pb.Task) error {
	w := c.table()
	_, _ = fmt.Fprintln(w, "TASK\tSTATUS\tPID\tCOMMAND")
	if len(list) == 0 {
		_, _ = fmt.Fprintln(w, "(no tasks)")
		return w.Flush()
	}
	for _, t := range list {
		pid := "-"
		if t.Pid > 0 {
			pid = fmt.Sprint(t.Pid)
		}
		status := t.Status
		if t.Status == "exited" {
			status = fmt.Sprintf("exited (%d)", t.ExitCode)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, status, pid, t.GetSpec().GetCommand())
	}
	return w.Flush()
}

func renderStats(c *Context, list []*pb.ProcessStat) error {
	w := c.table()
	_, _ = fmt.Fprintln(w, "NAME\tKIND\tPID\tPROCS\tCPU%\tMEM")
	if len(list) == 0 {
		_, _ = fmt.Fprintln(w, "(no running processes)")
		return w.Flush()
	}
	for _, s := range list {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%.1f\t%s\n", s.Name, s.Kind, s.Pid, s.Procs, s.CpuPercent, fmtBytes(s.RssBytes))
	}
	return w.Flush()
}

func printLogLine(w io.Writer, l *pb.LogLine, prefixed bool) {
	text := l.Text
	if l.Stream == "system" {
		text = ui.Dim("devyard: " + text)
	}
	if prefixed {
		_, _ = fmt.Fprintf(w, "%s │ %s\n", ui.ServicePrefix(l.GetSource().GetName()), text)
		return
	}
	_, _ = fmt.Fprintln(w, text)
}

// sortedProjects orders projects as the project list does.
func sortedProjects(list []*pb.Project) []*pb.Project {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b *pb.Project) int {
		if a.Position != b.Position {
			return int(a.Position - b.Position)
		}
		return strings.Compare(a.Id, b.Id)
	})
	return out
}
