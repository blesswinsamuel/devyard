package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// DaemonLogTail returns the last n lines of the daemon log.
func (sb *Sandbox) DaemonLogTail(n int) string {
	data, err := os.ReadFile(sb.DaemonLogPath())
	if err != nil {
		return fmt.Sprintf("(no daemon log: %v)", err)
	}
	return tailLines(string(data), n)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (sb *Sandbox) daemonLogDiag() string {
	return "--- daemon log (" + sb.DaemonLogPath() + ", tail) ---\n" + sb.DaemonLogTail(60) + "\n"
}

func (sb *Sandbox) fetchStateQuiet() *State {
	ctx, cancel := context.WithTimeout(context.Background(), sb.diagTimeout())
	defer cancel()
	resp, err := sb.Client().GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
	if err != nil {
		return nil
	}
	s := StateFromSnapshot(resp.Msg.GetRevision(), resp.Msg.GetSnapshot())
	return &s
}

// Diagnostics returns the daemon log tail, current state, the recent logs
// of every project and the processes carrying this sandbox's tag.
func (sb *Sandbox) Diagnostics() string {
	var b strings.Builder
	b.WriteString(sb.daemonLogDiag())
	st := sb.fetchStateQuiet()
	if st == nil {
		b.WriteString("--- state: daemon not reachable; latest run logs from disk ---\n")
		b.WriteString(sb.runLogsFromDisk(20))
	} else {
		b.WriteString("--- current " + st.String())
		ids := make([]string, 0, len(st.Projects))
		for id := range st.Projects {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "--- logs of project %s (tail) ---\n%s\n", id, sb.logsTailQuiet(id, 40))
		}
	}
	procs := sb.TaggedProcesses()
	fmt.Fprintf(&b, "--- %d tagged processes ---\n", len(procs))
	for _, p := range procs {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	return b.String()
}

func (sb *Sandbox) logsTailQuiet(project string, n int) string {
	ctx, cancel := context.WithTimeout(context.Background(), sb.diagTimeout())
	defer cancel()
	lines, err := collectLogs(ctx, sb.Client(), &v1.LogsRequest{Project: project, Tail: int32(n)})
	if err != nil && len(lines) == 0 {
		return fmt.Sprintf("(logs unavailable: %v)", err)
	}
	return FormatLogLines(lines)
}

// FormatLogLines renders lines as "source[stream] text".
func FormatLogLines(lines []*v1.LogLine) string {
	var b strings.Builder
	for _, l := range lines {
		text := l.GetText()
		if len(text) > 300 {
			text = text[:300] + fmt.Sprintf("...(%d bytes)", len(l.GetText()))
		}
		fmt.Fprintf(&b, "  %s %s/%s#%d [%s] %s\n", time.Unix(0, l.GetTsUnixNanos()).Format("15:04:05.000"), l.GetSource().GetKind(), l.GetSource().GetName(), l.GetSeq(), l.GetStream(), text)
	}
	return b.String()
}

// runLogsFromDisk tails the latest run log of every process:
// projects/<id>/procs/<kind>-<name>/runs/<N>.log.
func (sb *Sandbox) runLogsFromDisk(n int) string {
	var b strings.Builder
	procDirs, _ := filepath.Glob(filepath.Join(sb.dirs.projectsDir(), "*", "procs", "*"))
	for _, dir := range procDirs {
		runs, _ := filepath.Glob(filepath.Join(dir, "runs", "*.log"))
		latest, latestN := "", -1
		for _, r := range runs {
			if v, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(r), ".log")); err == nil && v > latestN {
				latest, latestN = r, v
			}
		}
		if latest == "" {
			continue
		}
		data, err := os.ReadFile(latest)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(sb.dirs.projectsDir(), latest)
		fmt.Fprintf(&b, "  == %s ==\n%s\n", rel, tailLines(string(data), n))
	}
	if b.Len() == 0 {
		return "  (no run logs on disk)\n"
	}
	return b.String()
}
