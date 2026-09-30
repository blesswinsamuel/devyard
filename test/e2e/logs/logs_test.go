// Package logs_test covers the Logs API and CLI: tail, follow, paging,
// merged sources, runs (--previous), stderr, and no loss across rotation.
package logs_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const cfg = `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -prefix A -interval 20ms
  b:
    command: {{fixture "ticker"}} -prefix B -interval 20ms -stderr
tasks:
  t: {{fixture "ticker"}} -prefix T -interval 0 -count 5
`

func setup(t *testing.T, name string) (*harness.Project, *harness.Daemon, *harness.Watcher) {
	t.Helper()
	sb := harness.New(t)
	p := sb.WriteProject(name, cfg, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	return p, d, w
}

func src(name string) []*v1.LogSource { return []*v1.LogSource{harness.Svc(name)} }

func TestLogs_TailAndFollow(t *testing.T) {
	t.Parallel()
	_, d, _ := setup(t, "tail")
	harness.Eventually(t, "enough history", func(c *harness.C) {
		if n := len(d.Logs(&v1.LogsRequest{Project: "tail", Sources: src("a")})); n < 20 {
			c.Errorf("%d lines", n)
		}
	})
	lines := d.Logs(&v1.LogsRequest{Project: "tail", Sources: src("a"), Tail: 5})
	if len(lines) != 5 {
		t.Fatalf("tail 5 returned %d lines", len(lines))
	}
	for i, l := range lines {
		if l.GetSource().GetName() != "a" || l.GetStream() != "stdout" || l.GetRun() < 1 {
			t.Errorf("line %d: %v", i, l)
		}
		if i > 0 && l.GetSeq() != lines[i-1].GetSeq()+1 {
			t.Errorf("seq not contiguous: %d then %d", lines[i-1].GetSeq(), l.GetSeq())
		}
	}
	f := d.FollowLogs(&v1.LogsRequest{Project: "tail", Sources: src("a"), Tail: 1})
	got := f.WaitFor(t, "20 followed lines", func(ls []*v1.LogLine) bool { return len(ls) >= 20 })
	for i := 1; i < len(got); i++ {
		if got[i].GetSeq() != got[i-1].GetSeq()+1 {
			t.Fatalf("follow gap: seq %d then %d", got[i-1].GetSeq(), got[i].GetSeq())
		}
	}
	if got[0].GetSeq() != lines[len(lines)-1].GetSeq() && got[0].GetSeq() < lines[len(lines)-1].GetSeq() {
		t.Errorf("follow with tail 1 started before the tail: %d", got[0].GetSeq())
	}
}

func TestLogs_StderrAndMerged(t *testing.T) {
	t.Parallel()
	_, d, _ := setup(t, "merged")
	harness.Eventually(t, "both sources and stderr", func(c *harness.C) {
		lines := d.Logs(&v1.LogsRequest{Project: "merged", Tail: 200})
		sources, stderr := map[string]bool{}, false
		var last int64
		for _, l := range lines {
			sources[l.GetSource().GetName()] = true
			if l.GetStream() == "stderr" && strings.Contains(l.GetText(), "B err") {
				stderr = true
			}
			if l.GetTsUnixNanos() < last {
				c.Fatalf("merged stream not ordered by timestamp")
			}
			last = l.GetTsUnixNanos()
		}
		if !sources["a"] || !sources["b"] {
			c.Errorf("sources %v", sources)
		}
		if !stderr {
			c.Errorf("no stderr lines from b")
		}
	})
}

func TestLogs_BeforeSeqPaging(t *testing.T) {
	t.Parallel()
	_, d, _ := setup(t, "paging")
	var last []*v1.LogLine
	harness.Eventually(t, "history", func(c *harness.C) {
		last = d.Logs(&v1.LogsRequest{Project: "paging", Sources: src("a"), Tail: 10})
		if len(last) < 10 || last[0].GetSeq() < 20 {
			c.Errorf("not enough yet")
		}
	})
	before := last[0].GetSeq()
	page := d.LogsFull(&v1.LogsRequest{Project: "paging", Sources: src("a"), Tail: 5, BeforeSeq: before})
	if len(page.Lines) != 5 || !page.HasMoreBefore {
		t.Fatalf("page: %d lines, has_more_before=%v", len(page.Lines), page.HasMoreBefore)
	}
	for i, l := range page.Lines {
		if want := before - 5 + uint64(i); l.GetSeq() != want {
			t.Errorf("page[%d].seq = %d, want %d", i, l.GetSeq(), want)
		}
	}
	first := d.LogsFull(&v1.LogsRequest{Project: "paging", Sources: src("a"), Tail: 1000, BeforeSeq: page.Lines[0].GetSeq()})
	if first.HasMoreBefore {
		t.Errorf("has_more_before at the start of the run")
	}
}

// S14: run_offset -1 (--previous) is exactly the previous run.
func TestLedger_S14_PreviousIsExactlyThePreviousRun(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "prev")
	st := w.State()
	oldPid, oldRun := st.ServicePid("prev", "a"), st.Service("prev", "a").GetRun()
	// Under load a fixture can take seconds to print its first line.
	d.WaitLog("prev", harness.Svc("a"), fmt.Sprintf("pid=%d", oldPid))
	_, err := d.Client().RestartService(d.Ctx(), connect.NewRequest(&v1.RestartServiceRequest{Project: "prev", Service: "a"}))
	harness.NoError(t, err, "RestartService")
	st = w.WaitFor(t, "new run", func(s harness.State) bool {
		return s.Running("prev", "a") && s.Service("prev", "a").GetRun() == oldRun+1
	})
	newPid := st.ServicePid("prev", "a")
	harness.Eventually(t, "current run logged", func(c *harness.C) {
		cur := d.Logs(&v1.LogsRequest{Project: "prev", Sources: src("a")})
		if !harness.ContainsText(cur, fmt.Sprintf("pid=%d", newPid)) {
			c.Errorf("no start line of the new run")
		}
		for _, l := range cur {
			if l.GetRun() != oldRun+1 {
				c.Fatalf("current run contains a line of run %d", l.GetRun())
			}
		}
	})
	prev := d.Logs(&v1.LogsRequest{Project: "prev", Sources: src("a"), RunOffset: -1})
	if !harness.ContainsText(prev, fmt.Sprintf("pid=%d", oldPid)) {
		t.Errorf("previous run missing its start line")
	}
	for _, l := range prev {
		if l.GetRun() != oldRun {
			t.Fatalf("previous run contains a line of run %d: %v", l.GetRun(), l)
		}
	}
}

// S14: no lines are lost across log rotation, in history or when following.
func TestLedger_S14_NoLossAcrossRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("writes tens of MiB of logs")
	}
	t.Parallel()
	sb := harness.New(t)
	const n = 6000 // 6000 x 4 KiB = ~24 MiB, past any sane rotation threshold
	p := sb.WriteProject("rot", fmt.Sprintf(`version: "1"
services:
  burst:
    command: {{fixture "ticker"}} -prefix R -interval 0 -count %d -line-size 4096
  steady:
    command: {{fixture "ticker"}} -prefix S -interval 1ms -line-size 8192
`, n), nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	f := d.FollowLogs(&v1.LogsRequest{Project: "rot", Sources: src("steady"), Tail: 1})
	w.WaitForWithin(t, 60*time.Second, "burst finished", func(s harness.State) bool {
		return s.ServiceIs("rot", "burst", "exited")
	})
	// Page backwards through the whole run.
	var all []*v1.LogLine
	req := &v1.LogsRequest{Project: "rot", Sources: src("burst"), Tail: 1000}
	for {
		page := d.LogsFull(req)
		all = append(page.Lines, all...)
		if !page.HasMoreBefore || len(page.Lines) == 0 {
			break
		}
		req = &v1.LogsRequest{Project: "rot", Sources: src("burst"), Tail: 1000, BeforeSeq: page.Lines[0].GetSeq()}
	}
	seqs := harness.Seqs(harness.LogText(all), "R")
	if len(seqs) != n || seqs[0] != 1 || seqs[len(seqs)-1] != n {
		t.Fatalf("history across rotation: %d lines (first %v, last %v), want 1..%d", len(seqs), head(seqs), tailN(seqs), n)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatalf("history gap at %d -> %d", seqs[i-1], seqs[i])
		}
	}
	lines := f.WaitForWithin(t, 60*time.Second, "followed 3000 steady lines", func(ls []*v1.LogLine) bool { return len(ls) >= 3000 })
	fs := harness.Seqs(harness.LogText(lines), "S")
	for i := 1; i < len(fs); i++ {
		if fs[i] != fs[i-1]+1 {
			t.Fatalf("follow gap at %d -> %d", fs[i-1], fs[i])
		}
	}
}

func head(s []int) []int {
	if len(s) > 3 {
		return s[:3]
	}
	return s
}

func tailN(s []int) []int {
	if len(s) > 3 {
		return s[len(s)-3:]
	}
	return s
}

func TestLogs_CLIMergedPrefixTailAndPrevious(t *testing.T) {
	t.Parallel()
	p, _, _ := setup(t, "clilogs")
	harness.Eventually(t, "merged CLI logs prefixed with service names", func(c *harness.C) {
		r := p.CLI("logs")
		r.Stdout = harness.StripANSI(r.Stdout)
		sawA := regexp.MustCompile(`(?m)^.*\ba\b.*\bA \d+`).MatchString(r.Stdout)
		sawB := regexp.MustCompile(`(?m)^.*\bb\b.*\bB \d+`).MatchString(r.Stdout)
		if !sawA || !sawB {
			c.Errorf("merged output not prefixed:\n%s", clip(r.Stdout))
		}
	})
	r := p.CLI("logs", "--tail", "3", "a").MustSucceed(t)
	if n := len(strings.Split(strings.TrimSpace(r.Stdout), "\n")); n != 3 {
		t.Errorf("logs --tail 3 printed %d lines:\n%s", n, r.Stdout)
	}
	// logs -f with several services follows all of them.
	f := p.Sandbox().CLIStart(harness.RunOpts{Dir: p.Dir}, "logs", "-f", "a", "b")
	f.WaitOutput(t, "A ")
	f.WaitOutput(t, "B ")
	f.Kill()
	// service logs / task logs are the long forms.
	p.CLI("run", "t").MustSucceed(t)
	if r := p.CLI("task", "logs", "t"); !strings.Contains(r.Stdout, "T 5") {
		t.Errorf("task logs:\n%s", r)
	}
	if r := p.CLI("service", "logs", "a"); !strings.Contains(r.Stdout, "A ") {
		t.Errorf("service logs:\n%s", r)
	}
}

func clip(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}
