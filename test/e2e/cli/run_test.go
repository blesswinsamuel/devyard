package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const taskProject = `services:
  idle:
    run: {{fixture "ticker"}} -interval 1s
tasks:
  greet: {{fixture "prompter"}}
  greet-notty:
    run: {{fixture "prompter"}}
    tty: false
  fail: {{fixture "exiter"}} -code 3
  echoargs: "echo args:"
  long: {{fixture "ticker"}} -prefix L -interval 100ms
`

func TestCLI_RunTaskNonTTYStreamsAndPropagatesExitCode(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("runs", taskProject, nil)
	p.Start()

	r := p.CLI("run", "fail")
	if r.Code != 3 {
		t.Errorf("devyard run fail: exit %d, want 3\n%s", r.Code, r)
	}
	if !strings.Contains(r.Stdout, "exiter exiting code=3") {
		t.Errorf("task output not streamed:\n%s", r)
	}
	r = p.CLI("run", "echoargs", "a", "b c").MustSucceed(t)
	if !strings.Contains(r.Stdout, "args: a b c") {
		t.Errorf("args not appended:\n%s", r)
	}
	// A tty:false task really has no terminal.
	r = p.CLI("run", "greet-notty")
	if r.Code != 3 || !strings.Contains(r.Stdout, "not a tty") {
		t.Errorf("greet-notty: %s", r)
	}
	// `task run` is the long form.
	if r := p.CLI("task", "run", "fail"); r.Code != 3 {
		t.Errorf("task run fail: exit %d", r.Code)
	}
}

func TestCLI_RunTaskInteractiveUnderPTY(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("interactive", taskProject, nil)
	p.Start()
	pt := sb.CLIPtyIn(p.Dir, "run", "greet")
	pt.Expect(t, "Name? ")
	pt.Send(t, "bob\r")
	pt.Expect(t, "hello bob")
	if code := pt.Wait(t, 20*time.Second); code != 0 {
		t.Fatalf("devyard run greet exited %d; output:\n%q", code, pt.Output())
	}
	st := sb.Daemon().State()
	if tk := st.Task("interactive", "greet"); tk.GetStatus() != "exited" || tk.GetExitCode() != 0 {
		t.Errorf("task state after run: %v", tk)
	}
}

func TestCLI_RunTaskCtrlCStopsTask(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("ctrlc", taskProject, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	pt := sb.CLIPtyIn(p.Dir, "run", "long")
	pt.Expect(t, "L 3")
	st := w.WaitFor(t, "task running", func(s harness.State) bool { return s.TaskRunning("ctrlc", "long") })
	pid := int(st.Task("ctrlc", "long").GetPid())
	pt.SendCtrlC(t)
	pt.Wait(t, 20*time.Second)
	w.WaitFor(t, "task no longer running", func(s harness.State) bool {
		st := s.TaskStatus("ctrlc", "long")
		return st != "running" && st != "stopping" && st != "waiting"
	})
	if pid > 0 && harness.GroupAlive(pid) {
		t.Errorf("task process group %d survived Ctrl-C", pid)
	}
}

func TestCLI_AttachTTYService(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("att", `services:
  console:
    run: sh -c 'echo console-ready; while read l; do echo "got:$l"; done'
    tty: true
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w, "console")
	pt := sb.CLIPtyIn(p.Dir, "attach", "console")
	pt.Send(t, "hello\r")
	pt.Expect(t, "got:hello")
	pt.Kill()
	// Detaching (the client going away) must not stop the service.
	harness.Consistently(t, "service keeps running after detach", time.Second, func(c *harness.C) {
		if !w.State().Running("att", "console") {
			c.Errorf("console stopped")
		}
	})
}
