// Package tasks_test covers tasks: runs that aren't tied to the caller,
// tty-by-default, interactive attach, concurrency and teardown.
package tasks_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const tasksCfg = `services:
  dep:
    run: {{fixture "ticker"}} -interval 1s
  other:
    run: {{fixture "ticker"}} -interval 1s
tasks:
  greet: {{fixture "prompter"}}
  plain:
    run: {{fixture "prompter"}}
    tty: false
  fail: {{fixture "exiter"}} -code 5 -after 100ms
  long: {{fixture "ticker"}} -prefix L -interval 100ms
  trap: {{fixture "sigtrap"}}
  big:
    run: {{fixture "ticker"}} -interval 0 -count 3 -line-size 200000
    tty: false
  echo: "echo got:"
  needs-dep:
    run: echo dep-ready
    depends_on: [dep]
`

func setup(t *testing.T, name string) (*harness.Sandbox, *harness.Daemon, *harness.Watcher) {
	t.Helper()
	sb := harness.New(t)
	p := sb.WriteProject(name, tasksCfg, nil)
	p.Start("other")
	d := sb.Daemon()
	w := d.Watch(context.Background())
	w.WaitFor(t, "project up", func(s harness.State) bool { return s.Running(name, "other") })
	return sb, d, w
}

func run(t *testing.T, d *harness.Daemon, project, task string, args ...string) int64 {
	t.Helper()
	resp, err := d.Client().RunTask(d.Ctx(), connect.NewRequest(&v1.RunTaskRequest{Project: project, Task: task, Args: args}))
	harness.NoError(t, err, "RunTask "+task)
	return resp.Msg.GetRun()
}

func finished(s harness.State, project, task string, run int64) bool {
	tk := s.Task(project, task)
	return tk.GetRun() >= run && (tk.GetStatus() == "exited" || tk.GetStatus() == "failed")
}

func TestTasks_RunExitCodeLogsAndArgs(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "tk")
	r := run(t, d, "tk", "fail")
	st := w.WaitFor(t, "fail finished", func(s harness.State) bool { return finished(s, "tk", "fail", r) })
	if tk := st.Task("tk", "fail"); tk.GetExitCode() != 5 || (tk.GetStatus() != "failed" && tk.GetStatus() != "exited") {
		t.Errorf("task: %v", tk)
	}
	lines := d.Logs(&v1.LogsRequest{Project: "tk", Sources: []*v1.LogSource{harness.TaskSrc("fail")}})
	if !harness.ContainsText(lines, "exiter exiting code=5") {
		t.Errorf("task logs:\n%s", harness.FormatLogLines(lines))
	}
	r = run(t, d, "tk", "echo", "x", "y z")
	st = w.WaitFor(t, "echo finished", func(s harness.State) bool { return finished(s, "tk", "echo", r) })
	if got := st.Task("tk", "echo").GetArgs(); len(got) != 2 || got[1] != "y z" {
		t.Errorf("task args = %q", got)
	}
	if lines := d.Logs(&v1.LogsRequest{Project: "tk", Sources: []*v1.LogSource{harness.TaskSrc("echo")}}); !harness.ContainsText(lines, "got: x y z") {
		t.Errorf("args not passed:\n%s", harness.FormatLogLines(lines))
	}
	// Runs are numbered; the previous run's logs stay readable.
	r2 := run(t, d, "tk", "fail")
	if r2 <= r {
		t.Logf("run numbers: %d then %d", r, r2)
	}
	w.WaitFor(t, "second fail run finished", func(s harness.State) bool { return finished(s, "tk", "fail", r2) })
	prev := d.Logs(&v1.LogsRequest{Project: "tk", Sources: []*v1.LogSource{harness.TaskSrc("fail")}, RunOffset: -1})
	if !harness.ContainsText(prev, "exiter exiting code=5") {
		t.Errorf("previous task run logs unreadable")
	}
}

func TestTasks_TTYByDefault(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "tty")
	st := d.State()
	if !st.Task("tty", "greet").GetSpec().GetTty() {
		t.Errorf("task without tty: should default to tty=true")
	}
	if st.Task("tty", "plain").GetSpec().GetTty() {
		t.Errorf("tty: false not honored")
	}
	r := run(t, d, "tty", "plain")
	st = w.WaitFor(t, "plain finished", func(s harness.State) bool { return finished(s, "tty", "plain", r) })
	if code := st.Task("tty", "plain").GetExitCode(); code != 3 {
		t.Errorf("tty:false prompter exit %d, want 3 (not a tty)", code)
	}
}

func TestTasks_InteractiveViaAttachRPC(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "att")
	r := run(t, d, "att", "greet")
	s := d.Attach(&v1.AttachTarget{Kind: "task", Project: "att", Name: "greet"}, 80, 24)
	if !s.TTY {
		t.Errorf("attach ready.tty = false for a tty task")
	}
	s.Expect(t, "Name? ")
	s.Send(t, "alice\r")
	s.Expect(t, "hello alice")
	st := w.WaitFor(t, "greet finished", func(st harness.State) bool { return finished(st, "att", "greet", r) })
	if code := st.Task("att", "greet").GetExitCode(); code != 0 {
		t.Errorf("greet exit %d", code)
	}
	if code := s.WaitExit(t, 10*time.Second); code != 0 {
		t.Errorf("AttachExit code %d", code)
	}
}

func TestTasks_AttachLineStdinForNonTTY(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("line", `services:
  idle:
    run: {{fixture "ticker"}} -interval 1s
tasks:
  reader:
    run: sh -c 'read l; echo "read:$l"'
    tty: false
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	r := run(t, d, "line", "reader")
	s := d.Attach(&v1.AttachTarget{Kind: "task", Project: "line", Name: "reader"}, 80, 24)
	if s.TTY {
		t.Errorf("ready.tty = true for a tty:false task")
	}
	s.Send(t, "hi there\n")
	w.WaitFor(t, "reader finished", func(st harness.State) bool { return finished(st, "line", "reader", r) })
	if lines := d.Logs(&v1.LogsRequest{Project: "line", Sources: []*v1.LogSource{harness.TaskSrc("reader")}}); !harness.ContainsText(lines, "read:hi there") {
		t.Errorf("stdin not delivered:\n%s", harness.FormatLogLines(lines))
	}
}

// S6: concurrent RunTask of the same task: one wins, the rest get
// FailedPrecondition.
func TestLedger_S6_ConcurrentRunTaskSecondFails(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "s6")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = d.Client().RunTask(d.Ctx(), connect.NewRequest(&v1.RunTaskRequest{Project: "s6", Task: "long"}))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("RunTask error code %v: %v", connect.CodeOf(err), err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent RunTask calls succeeded, want exactly 1", ok)
	}
	st := w.WaitFor(t, "long running", func(s harness.State) bool { return s.TaskRunning("s6", "long") })
	_, err := d.Client().RunTask(d.Ctx(), connect.NewRequest(&v1.RunTaskRequest{Project: "s6", Task: "long"}))
	harness.RequireCode(t, err, connect.CodeFailedPrecondition)
	// The run is published as soon as it is started, possibly before the
	// child has exec'd, so wait for it to show up in the process list.
	harness.Eventually(t, "exactly one live task process group", func(c *harness.C) {
		if n := countTaskProcs(d.Sandbox(), int(st.Task("s6", "long").GetPid())); n != 1 {
			c.Errorf("%d live task process groups", n)
		}
	})
}

func countTaskProcs(sb *harness.Sandbox, pid int) int {
	groups := map[int]bool{}
	for _, p := range sb.TaggedProcesses() {
		if strings.HasSuffix(p.Exe, "/ticker") && strings.Contains(p.Command, "-prefix L") {
			groups[p.Pgid] = true
		}
	}
	_ = pid
	return len(groups)
}

// S11: lines longer than 64 KiB don't hang the task.
func TestLedger_S11_LongLinesDontHang(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "s11")
	r := run(t, d, "s11", "big")
	w.WaitForWithin(t, 20*time.Second, "big finished", func(s harness.State) bool { return finished(s, "s11", "big", r) })
	lines := d.Logs(&v1.LogsRequest{Project: "s11", Sources: []*v1.LogSource{harness.TaskSrc("big")}})
	long := 0
	for _, l := range lines {
		if len(l.GetText()) >= 64<<10 {
			long++
		}
	}
	if !harness.ContainsText(lines, "tick done") {
		t.Errorf("output after the long lines missing")
	}
	if long == 0 {
		// Long lines may be split into chunks; at least all bytes must be there.
		total := 0
		for _, l := range lines {
			total += len(l.GetText())
		}
		if total < 3*200000 {
			t.Errorf("long lines lost: %d bytes logged", total)
		}
	}
}

// S11: StopTask escalates to SIGKILL; StopProject stops running tasks.
func TestLedger_S11_StopTaskEscalatesAndProjectStopStopsTasks(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "s11b")
	run(t, d, "s11b", "trap")
	st := w.WaitFor(t, "trap running", func(s harness.State) bool { return s.TaskRunning("s11b", "trap") })
	pid := int(st.Task("s11b", "trap").GetPid())
	d.WaitLog("s11b", harness.TaskSrc("trap"), "sigtrap ready")
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(45*time.Second))
	defer cancel()
	_, err := d.Client().StopTask(ctx, connect.NewRequest(&v1.StopTaskRequest{Project: "s11b", Task: "trap"}))
	harness.NoError(t, err, "StopTask")
	w.WaitFor(t, "trap stopped", func(s harness.State) bool {
		st := s.TaskStatus("s11b", "trap")
		return st != "running" && st != "stopping" && !harness.GroupAlive(pid)
	})

	run(t, d, "s11b", "long")
	st = w.WaitFor(t, "long running", func(s harness.State) bool { return s.TaskRunning("s11b", "long") })
	pid = int(st.Task("s11b", "long").GetPid())
	_, err = d.Client().StopProject(ctx, connect.NewRequest(&v1.StopProjectRequest{Project: "s11b"}))
	harness.NoError(t, err, "StopProject")
	w.WaitFor(t, "task stopped with the project", func(s harness.State) bool {
		return s.TaskStatus("s11b", "long") != "running" && !harness.GroupAlive(pid)
	})
}

func TestTasks_KillTask(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "kt")
	run(t, d, "kt", "trap")
	st := w.WaitFor(t, "trap running", func(s harness.State) bool { return s.TaskRunning("kt", "trap") })
	pid := int(st.Task("kt", "trap").GetPid())
	_, err := d.Client().KillTask(d.Ctx(), connect.NewRequest(&v1.KillTaskRequest{Project: "kt", Task: "trap"}))
	harness.NoError(t, err, "KillTask")
	w.WaitFor(t, "killed", func(s harness.State) bool { return !harness.GroupAlive(pid) && s.TaskStatus("kt", "trap") != "running" })
}

// O21: a task started over the API keeps running after its caller goes away.
func TestLedger_O21_TaskSurvivesCallerDisconnect(t *testing.T) {
	t.Parallel()
	sb, d, w := setup(t, "o21")
	client, closeClient := sb.NewClient()
	_, err := client.RunTask(context.Background(), connect.NewRequest(&v1.RunTaskRequest{Project: "o21", Task: "long"}))
	harness.NoError(t, err, "RunTask")
	closeClient()
	st := w.WaitFor(t, "long running", func(s harness.State) bool { return s.TaskRunning("o21", "long") })
	pid := st.Task("o21", "long").GetPid()
	harness.Consistently(t, "task keeps running", 1500*time.Millisecond, func(c *harness.C) {
		tk := w.State().Task("o21", "long")
		if tk.GetStatus() != "running" || tk.GetPid() != pid {
			c.Errorf("task: %v", tk)
		}
	})
	// A client that attached and dropped doesn't kill it either.
	s := d.Attach(&v1.AttachTarget{Kind: "task", Project: "o21", Name: "long"}, 80, 24)
	s.Expect(t, "L ")
	s.Drop()
	harness.Consistently(t, "task survives a dropped attach", time.Second, func(c *harness.C) {
		if w.State().TaskStatus("o21", "long") != "running" {
			c.Errorf("task stopped")
		}
	})
}

// Tasks run under the runner too: they survive a daemon restart.
func TestTasks_SurviveDaemonRestart(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "tdr")
	r := run(t, d, "tdr", "long")
	st := w.WaitFor(t, "long running", func(s harness.State) bool { return s.TaskRunning("tdr", "long") })
	pid := st.Task("tdr", "long").GetPid()
	d.Restart(false)
	w.WaitFor(t, "task adopted", func(s harness.State) bool {
		tk := s.Task("tdr", "long")
		return tk.GetStatus() == "running" && tk.GetPid() == pid && tk.GetRun() == r
	})
	_, err := d.Client().StopTask(d.Ctx(), connect.NewRequest(&v1.StopTaskRequest{Project: "tdr", Task: "long"}))
	harness.NoError(t, err, "StopTask (adopted)")
	w.WaitFor(t, "adopted task stopped", func(s harness.State) bool {
		return s.TaskStatus("tdr", "long") != "running" && !harness.GroupAlive(int(pid))
	})
}

func TestTasks_CloseStdinDeliversEOF(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("eof", `services:
  idle:
    run: {{fixture "ticker"}} -interval 1s
tasks:
  cat:
    run: sh -c 'cat; echo cat-saw-eof'
    tty: false
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	r := run(t, d, "eof", "cat")
	s := d.Attach(&v1.AttachTarget{Kind: "task", Project: "eof", Name: "cat"}, 80, 24)
	s.Send(t, "line-one\n")
	s.CloseStdin(t)
	st := w.WaitFor(t, "cat finished after EOF", func(st harness.State) bool { return finished(st, "eof", "cat", r) })
	if st.Task("eof", "cat").GetExitCode() != 0 {
		t.Errorf("cat: %v", st.Task("eof", "cat"))
	}
	lines := d.Logs(&v1.LogsRequest{Project: "eof", Sources: []*v1.LogSource{harness.TaskSrc("cat")}})
	if !harness.ContainsText(lines, "line-one") || !harness.ContainsText(lines, "cat-saw-eof") {
		t.Errorf("logs:\n%s", harness.FormatLogLines(lines))
	}
}

func TestTasks_DependsOnStartsServices(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "dep")
	r := run(t, d, "dep", "needs-dep")
	st := w.WaitFor(t, "task finished", func(s harness.State) bool { return finished(s, "dep", "needs-dep", r) })
	if !st.Running("dep", "dep") {
		t.Errorf("dependency not started for the task: %s", st)
	}
	if st.Task("dep", "needs-dep").GetExitCode() != 0 {
		t.Errorf("task: %v", st.Task("dep", "needs-dep"))
	}
}
