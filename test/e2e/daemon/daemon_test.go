// Package daemon_test covers the daemon lifecycle: single instance, restart
// with and without services, crash recovery (adoption through the runners),
// autostart, and teardown.
package daemon_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const marked = `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -prefix A -interval 100ms
    env:
      DY_MARK: {{.Name}}-alpha
  beta:
    command: {{fixture "ticker"}} -prefix B -interval 100ms
    env:
      DY_MARK: {{.Name}}-beta
`

// daemonProcs returns the live daemon processes of the sandbox (the
// runners are devyard processes too, but without --daemon).
func daemonProcs(sb *harness.Sandbox) []harness.ProcInfo {
	var out []harness.ProcInfo
	for _, p := range sb.TaggedProcesses() {
		if strings.Contains(p.Command, "--daemon") && harness.PidAlive(p.Pid) {
			out = append(out, p)
		}
	}
	return out
}

func requireOneDaemon(t *testing.T, sb *harness.Sandbox) {
	t.Helper()
	harness.Eventually(t, "exactly one daemon process", func(c *harness.C) {
		if procs := daemonProcs(sb); len(procs) != 1 {
			c.Errorf("%d daemon processes: %v", len(procs), procs)
		}
	})
}

func requireOneGroup(t *testing.T, sb *harness.Sandbox, markers ...string) {
	t.Helper()
	harness.Eventually(t, "one process per service", func(c *harness.C) {
		for _, m := range markers {
			if g := sb.LiveGroups("DY_MARK", m); len(g) != 1 {
				c.Errorf("%s: live groups %v", m, g)
			}
		}
	})
}

// O16: concurrent `daemon start` yields one daemon; the pidfile names it.
func TestLedger_O16_ConcurrentDaemonStartSingleDaemon(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	var wg sync.WaitGroup
	results := make([]harness.Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = sb.CLI("daemon", "start")
		}()
	}
	wg.Wait()
	for i, r := range results {
		if r.Code != 0 {
			t.Errorf("daemon start #%d failed:\n%s", i, r)
		}
	}
	d := sb.Daemon()
	requireOneDaemon(t, sb)
	if got, want := sb.PidfilePid(), d.Pid(); got != want {
		t.Errorf("pidfile names %d, live daemon is %d", got, want)
	}
	d.Stop()
	if pid := sb.PidfilePid(); pid != 0 && harness.PidAlive(pid) {
		t.Errorf("pidfile names a live process %d after stop", pid)
	}
}

// S5: `daemon restart` keeps services (same pids, same run) and their
// output keeps flowing with no gap.
func TestLedger_S5_DaemonRestartKeepsServicesAndLogs(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("s5r", marked, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w)
	before := harness.MaxSeq(harness.LogText(d.Logs(&v1.LogsRequest{Project: "s5r", Sources: []*v1.LogSource{harness.Svc("alpha")}})), "A")

	sb.CLI("daemon", "restart").MustSucceed(t)
	d.WaitReady()
	requireOneDaemon(t, sb)
	after := w.WaitForWithin(t, 20*time.Second, "same pids after restart", func(s harness.State) bool {
		return s.AllRunning("s5r") && s.ServicePid("s5r", "alpha") == st.ServicePid("s5r", "alpha") &&
			s.ServicePid("s5r", "beta") == st.ServicePid("s5r", "beta")
	})
	if a, b := st.Service("s5r", "alpha").GetRun(), after.Service("s5r", "alpha").GetRun(); a != b {
		t.Errorf("run changed %d -> %d across a keep-services restart", a, b)
	}
	harness.Eventually(t, "output keeps flowing, no gap", func(c *harness.C) {
		text := harness.LogText(d.Logs(&v1.LogsRequest{Project: "s5r", Sources: []*v1.LogSource{harness.Svc("alpha")}}))
		seqs := harness.Seqs(text, "A")
		if len(seqs) == 0 || seqs[len(seqs)-1] < before+10 {
			c.Fatalf("no new lines after restart (before=%d, seqs tail=%v)", before, tail(seqs, 5))
		}
		for i := 1; i < len(seqs); i++ {
			if seqs[i] != seqs[i-1]+1 {
				c.Fatalf("gap in output: %d -> %d", seqs[i-1], seqs[i])
			}
		}
	})
	requireOneGroup(t, sb, "s5r-alpha", "s5r-beta")
}

func tail(s []int, n int) []int {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// S5: SIGKILL of the daemon doesn't touch services; the next CLI command
// brings up a daemon that adopts them without duplicating anything.
func TestLedger_S5_DaemonCrashKeepsServices(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("s5c", marked, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w)
	before := harness.MaxSeq(harness.LogText(d.Logs(&v1.LogsRequest{Project: "s5c", Sources: []*v1.LogSource{harness.Svc("beta")}})), "B")

	d.KillHard()
	for _, n := range []string{"alpha", "beta"} {
		if !harness.GroupAlive(st.ServicePid("s5c", n)) {
			t.Fatalf("%s died with the daemon", n)
		}
	}
	time.Sleep(time.Second) // output produced while no daemon runs
	p.Start()               // auto-starts a daemon; must not duplicate
	d.WaitReady()
	w.WaitForWithin(t, 20*time.Second, "adopted with the same pids", func(s harness.State) bool {
		return s.AllRunning("s5c") && s.ServicePid("s5c", "alpha") == st.ServicePid("s5c", "alpha") &&
			s.ServicePid("s5c", "beta") == st.ServicePid("s5c", "beta")
	})
	requireOneGroup(t, sb, "s5c-alpha", "s5c-beta")
	requireOneDaemon(t, sb)
	harness.Eventually(t, "lines written while the daemon was down are in the logs", func(c *harness.C) {
		seqs := harness.Seqs(harness.LogText(d.Logs(&v1.LogsRequest{Project: "s5c", Sources: []*v1.LogSource{harness.Svc("beta")}})), "B")
		have := map[int]bool{}
		for _, s := range seqs {
			have[s] = true
		}
		for i := before + 1; i <= before+8; i++ {
			if !have[i] {
				c.Fatalf("line B %d missing", i)
			}
		}
	})
}

// O4: an exit that happens while the daemon is down is reported with the
// real exit code afterwards, and on-failure doesn't restart a clean exit.
func TestLedger_O4_ExitWhileDaemonDownReported(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("o4", `version: "1"
services:
  seven:
    command: {{fixture "exiter"}} -code 7 -after 6s
  clean:
    command: {{fixture "exiter"}} -code 0 -after 6s
    restart: on-failure
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w)
	d.KillHard()
	for _, n := range []string{"seven", "clean"} {
		pid := st.ServicePid("o4", n)
		harness.Eventually(t, n+" exited while the daemon is down", func(c *harness.C) {
			if harness.GroupAlive(pid) {
				c.Errorf("still running")
			}
		})
	}
	sb.CLI("daemon", "start").MustSucceed(t)
	d.WaitReady()
	after := w.WaitFor(t, "exits reported", func(s harness.State) bool {
		return s.ServiceIs("o4", "seven", "exited", "failed") && s.ServiceIs("o4", "clean", "exited")
	})
	if code := after.Service("o4", "seven").GetExitCode(); code != 7 {
		t.Errorf("seven exit code %d, want 7", code)
	}
	if code := after.Service("o4", "clean").GetExitCode(); code != 0 {
		t.Errorf("clean exit code %d, want 0", code)
	}
	harness.Consistently(t, "on-failure does not restart a clean exit", time.Second, func(c *harness.C) {
		sv := w.State().Service("o4", "clean")
		if sv.GetRun() != st.Service("o4", "clean").GetRun() {
			c.Errorf("clean restarted: %s", harness.FormatService(sv))
		}
	})
}

// S5: an adopted service still follows its restart policy.
func TestLedger_S5_AdoptedServiceFollowsRestartPolicy(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("s5p", `version: "1"
services:
  svc:
    command: {{fixture "ticker"}} -interval 200ms
    restart: always
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	pid := p.WaitRunning(w).ServicePid("s5p", "svc")
	d.Restart(false)
	w.WaitFor(t, "adopted", func(s harness.State) bool { return s.ServicePid("s5p", "svc") == pid && s.Running("s5p", "svc") })
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill adopted group: %v", err)
	}
	st := w.WaitFor(t, "relaunched", func(s harness.State) bool {
		return s.Running("s5p", "svc") && s.ServicePid("s5p", "svc") != pid
	})
	if r := st.Service("s5p", "svc").GetRestarts(); r < 1 {
		t.Errorf("restarts = %d after an adopted crash", r)
	}
	d.Info()
}

// S5: stopping an adopted service escalates to SIGKILL like a fresh one.
func TestLedger_S5_AdoptedStopEscalates(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("s5e", `version: "1"
services:
  trap:
    command: {{fixture "sigtrap"}}
    stop_grace_period: 500ms
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	pid := p.WaitRunning(w).ServicePid("s5e", "trap")
	d.WaitLog("s5e", harness.Svc("trap"), "sigtrap ready")
	d.Restart(false)
	w.WaitFor(t, "adopted", func(s harness.State) bool { return s.ServicePid("s5e", "trap") == pid })
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(45*time.Second))
	defer cancel()
	_, err := d.Client().StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "s5e", Service: "trap"}))
	harness.NoError(t, err, "StopService (adopted)")
	if harness.GroupAlive(pid) {
		t.Fatalf("adopted process group %d survived stop", pid)
	}
}

// O8 / S13: restart with services while services are slow to stop: the
// command completes, exactly one new daemon runs, state is intact.
func TestLedger_O8_S13_RestartWithSlowServices(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("o8", `version: "1"
services:
  s1:
    command: {{fixture "sigtrap"}} -term-delay 2s
    stop_grace_period: 5s
  s2:
    command: {{fixture "sigtrap"}} -term-delay 2s
    stop_grace_period: 5s
  s3:
    command: {{fixture "sigtrap"}} -term-delay 2s
    stop_grace_period: 5s
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w)
	for _, n := range []string{"s1", "s2", "s3"} {
		d.WaitLog("o8", harness.Svc(n), "sigtrap ready")
	}
	old := d.Info()
	r := sb.CLIWith(harness.RunOpts{Timeout: 90 * time.Second}, "daemon", "restart", "-r").MustSucceed(t)
	t.Logf("daemon restart -r took %s", r.Duration)
	d.WaitReplaced(old)
	requireOneDaemon(t, sb)
	after := w.WaitForWithin(t, 30*time.Second, "all services back with new pids", func(s harness.State) bool {
		if !s.AllRunning("o8") {
			return false
		}
		for _, n := range []string{"s1", "s2", "s3"} {
			if s.ServicePid("o8", n) == st.ServicePid("o8", n) {
				return false
			}
		}
		return true
	})
	for _, n := range []string{"s1", "s2", "s3"} {
		if harness.GroupAlive(st.ServicePid("o8", n)) {
			t.Errorf("old %s (pgid %d) still alive", n, st.ServicePid("o8", n))
		}
	}
	if after.Project("o8") == nil {
		t.Fatalf("project lost across restart")
	}
	if _, err := os.Stat(filepath.Join(sb.ProjectStateDir("o8"), "project.json")); err != nil {
		t.Errorf("project state removed by the old daemon: %v", err)
	}
	if _, err := os.Stat(sb.SocketPath()); err != nil {
		t.Errorf("socket missing after restart: %v", err)
	}
	if got := sb.PidfilePid(); got != d.Pid() {
		t.Errorf("pidfile %d != daemon %d", got, d.Pid())
	}
}

// `daemon stop` stops every service and task and leaves no processes.
func TestDaemon_StopLeavesNothing(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("stopall", marked+`tasks:
  long: {{fixture "ticker"}} -interval 100ms
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	_, err := d.Client().RunTask(d.Ctx(), connect.NewRequest(&v1.RunTaskRequest{Project: "stopall", Task: "long"}))
	harness.NoError(t, err, "RunTask")
	st := w.WaitFor(t, "task running", func(s harness.State) bool { return s.TaskRunning("stopall", "long") })
	pids := []int{st.ServicePid("stopall", "alpha"), st.ServicePid("stopall", "beta"), int(st.Task("stopall", "long").GetPid())}
	d.Stop()
	harness.Eventually(t, "nothing left running", func(c *harness.C) {
		for _, pid := range pids {
			if harness.GroupAlive(pid) {
				c.Errorf("group %d alive", pid)
			}
		}
		if procs := sb.TaggedProcesses(); len(procs) > 0 {
			c.Errorf("tagged processes remain: %v", procs)
		}
	})
}

func TestDaemon_AutostartResumesRunningProjects(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("auto", `version: "1"
services:
  web:
    command: {{fixture "ticker"}} -interval 1s
  worker:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	d.Watch(context.Background())
	d.Stop()
	sb.CLI("daemon", "start").MustSucceed(t)
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
}

func TestDaemon_AutostartHonorsStopped(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("autostop", `version: "1"
services:
  web:
    command: {{fixture "ticker"}} -prefix W -interval 100ms
`, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	d.WaitLog("autostop", harness.Svc("web"), "W 1")
	p.CLI("stop").MustSucceed(t)
	w.WaitFor(t, "stopped", func(s harness.State) bool { return s.ServiceIs("autostop", "web", "stopped") })
	d.Stop()
	sb.CLI("daemon", "start").MustSucceed(t)
	d = sb.Daemon()
	w2 := d.Watch(context.Background())
	w2.WaitFor(t, "listed as stopped", func(s harness.State) bool {
		return s.Project("autostop") != nil && s.ServiceIs("autostop", "web", "stopped")
	})
	harness.Consistently(t, "not autostarted", time.Second, func(c *harness.C) {
		if w2.State().Running("autostop", "web") {
			c.Errorf("autostarted a stopped project")
		}
	})
	if lines := d.Logs(&v1.LogsRequest{Project: "autostop", Sources: []*v1.LogSource{harness.Svc("web")}}); !harness.ContainsText(lines, "W 1") {
		t.Errorf("historical logs unreadable after restart")
	}
}

func TestDaemon_RestartKeepsPartialSelection(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("sel", `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -interval 1s
  beta:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start("alpha")
	d := sb.Daemon()
	w := d.Watch(context.Background())
	pid := w.WaitFor(t, "alpha only", func(s harness.State) bool {
		return s.Running("sel", "alpha") && s.ServiceIs("sel", "beta", "stopped")
	}).ServicePid("sel", "alpha")
	d.Restart(false)
	w.WaitFor(t, "alpha adopted, beta still stopped", func(s harness.State) bool {
		return s.ServicePid("sel", "alpha") == pid && s.ServiceIs("sel", "beta", "stopped")
	})
	d.Stop()
	sb.CLI("daemon", "start").MustSucceed(t)
	w2 := sb.Daemon().Watch(context.Background())
	w2.WaitFor(t, "autostart honors the selection", func(s harness.State) bool {
		return s.Running("sel", "alpha") && s.ServiceIs("sel", "beta", "stopped")
	})
	harness.Consistently(t, "beta stays stopped", time.Second, func(c *harness.C) {
		if w2.State().Running("sel", "beta") {
			c.Errorf("beta started")
		}
	})
}

// O20: a project whose config is broken at autostart stays listed, with an
// error.
func TestLedger_O20_BrokenConfigAtAutostartStillListed(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("broken", `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	d.Watch(context.Background())
	d.Stop()
	p.WriteConfig("version: \"1\"\nservices: [\n")
	sb.CLI("daemon", "start").MustSucceed(t)
	w := sb.Daemon().Watch(context.Background())
	w.WaitFor(t, "listed with an error", func(s harness.State) bool {
		pr := s.Project("broken")
		return pr != nil && pr.GetError() != "" && pr.GetStatus() == "error"
	})
}

// O3: `start` racing the daemon's autostart never duplicates processes.
func TestLedger_O3_StartDuringAutostartNoDuplicates(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("race", marked, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	d.Stop()

	var wg sync.WaitGroup
	results := make([]harness.Result, 4)
	wg.Add(len(results))
	go func() { defer wg.Done(); results[0] = sb.CLI("daemon", "start") }()
	for i := 1; i < len(results); i++ {
		go func() { defer wg.Done(); results[i] = p.CLI("start") }()
	}
	wg.Wait()
	for i, r := range results {
		if r.Code != 0 {
			t.Errorf("command %d failed:\n%s", i, r)
		}
	}
	w2 := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w2)
	requireOneDaemon(t, sb)
	harness.Consistently(t, "exactly one process per service", 1500*time.Millisecond, func(c *harness.C) {
		for _, m := range []string{"race-alpha", "race-beta"} {
			if g := sb.LiveGroups("DY_MARK", m); len(g) != 1 {
				c.Errorf("%s: %v", m, g)
			}
		}
	})
}

func TestDaemon_ListenerCollisionFailsStartup(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"web", "proxy"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()
			busy := ln.Addr().(*net.TCPAddr).Port
			web, proxy := 0, 0
			if which == "web" {
				web = busy
			} else {
				proxy = busy
			}
			sb := harness.New(t, harness.WithGlobalConfig(fmt.Sprintf("web:\n  host: 127.0.0.1\n  port: %d\nproxy:\n  host: 127.0.0.1\n  port: %d\n", web, proxy)))
			r := sb.CLI("daemon", "start").MustFail(t)
			if !strings.Contains(r.Stderr, "in use") {
				t.Errorf("error should explain the port is in use:\n%s", r)
			}
			if sb.DaemonRunning() {
				t.Errorf("a daemon is running despite the collision")
			}
		})
	}
}
