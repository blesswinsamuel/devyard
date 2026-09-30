package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

// setup writes and starts a project, returning the daemon and a watcher.
func setup(t *testing.T, name, cfg string) (*harness.Sandbox, *harness.Project, *harness.Daemon, *harness.Watcher) {
	t.Helper()
	sb := harness.New(t)
	p := sb.WriteProject(name, cfg, nil)
	p.Start()
	d := sb.Daemon()
	return sb, p, d, d.Watch(context.Background())
}

// groupSampler polls the live process groups of a marked service in the
// background and records any moment with more than one.
type groupSampler struct {
	stop       chan struct{}
	done       chan struct{}
	max        atomic.Int32
	violations []string
	mu         sync.Mutex
}

func sampleGroups(sb *harness.Sandbox, marker string) *groupSampler {
	g := &groupSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(g.done)
		for {
			select {
			case <-g.stop:
				return
			default:
			}
			groups := sb.LiveGroups("DY_MARK", marker)
			if n := int32(len(groups)); n > g.max.Load() {
				g.max.Store(n)
			}
			if len(groups) > 1 {
				g.mu.Lock()
				g.violations = append(g.violations, fmt.Sprintf("%s: %d live groups %v", time.Now().Format("15:04:05.000"), len(groups), groups))
				g.mu.Unlock()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return g
}

func (g *groupSampler) finish(t *testing.T) {
	t.Helper()
	close(g.stop)
	<-g.done
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.violations) > 0 {
		n := len(g.violations)
		if n > 10 {
			g.violations = g.violations[:10]
		}
		t.Errorf("more than one live process for the service (%d samples):\n  %s", n, strings.Join(g.violations, "\n  "))
	}
}

// S1: tty services start and their output is logged.
func TestLedger_S1_TTYServiceStartsAndLogs(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s1", `version: "1"
services:
  term:
    command: sh -c 'if [ -t 1 ]; then echo on-a-tty; fi; exec {{fixture "ticker"}} -prefix T -interval 100ms'
    tty: true
`)
	st := w.WaitFor(t, "tty service running", func(s harness.State) bool { return s.Running("s1", "term") })
	if !st.Service("s1", "term").GetSpec().GetTty() {
		t.Errorf("spec.tty = false")
	}
	f := d.FollowLogs(&v1.LogsRequest{Project: "s1", Sources: []*v1.LogSource{harness.Svc("term")}})
	f.WaitForText(t, "on-a-tty")
	f.WaitForText(t, "T 3")
}

// S2: concurrent restarts never produce two live processes, and the daemon
// survives them.
func TestLedger_S2_ConcurrentRestartsSingleProcess(t *testing.T) {
	t.Parallel()
	sb, _, d, w := setup(t, "s2", `version: "1"
services:
  svc:
    command: {{fixture "ticker"}} -interval 100ms
    env:
      DY_MARK: s2
`)
	w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("s2", "svc") })
	sampler := sampleGroups(sb, "s2")
	ctx := d.Ctx()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Client().RestartService(ctx, connect.NewRequest(&v1.RestartServiceRequest{Project: "s2", Service: "svc"}))
			errs <- err
		}()
	}
	waitGroup(t, &wg, 60*time.Second, "10 concurrent RestartService calls")
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("RestartService: %v", err)
		}
	}
	st := w.WaitFor(t, "running after the restart storm", func(s harness.State) bool { return s.Running("s2", "svc") })
	harness.Consistently(t, "exactly one live process", time.Second, func(c *harness.C) {
		if g := sb.LiveGroups("DY_MARK", "s2"); len(g) != 1 {
			c.Errorf("live groups: %v", g)
		}
	})
	sampler.finish(t)
	if g := sb.LiveGroups("DY_MARK", "s2"); len(g) == 1 && g[0] != st.ServicePid("s2", "svc") && st.ServicePid("s2", "svc") != 0 {
		// pid is the group leader of the service's process group.
		if pg, err := syscall.Getpgid(st.ServicePid("s2", "svc")); err == nil && pg != g[0] {
			t.Errorf("live group %d is not the reported service pid %d", g[0], st.ServicePid("s2", "svc"))
		}
	}
	d.Info() // daemon still alive
}

func waitGroup(t *testing.T, wg *sync.WaitGroup, d time.Duration, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(harness.Scale(d)):
		t.Fatalf("%s did not complete within %s", what, harness.Scale(d))
	}
}

// S3: stop interrupts backoff promptly and nothing restarts afterwards;
// an explicit start resets the restart counter.
func TestLedger_S3_StopDuringBackoffIsPrompt(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s3", `version: "1"
services:
  crash:
    command: {{fixture "exiter"}} -code 1 -after 50ms
    restart: always
`)
	st := w.WaitForWithin(t, 30*time.Second, "crash-looping service in backoff", func(s harness.State) bool {
		sv := s.Service("s3", "crash")
		return sv.GetStatus() == "backoff" && sv.GetRestarts() >= 2
	})
	if st.Service("s3", "crash").GetNextRestartAtUnixMs() == 0 {
		t.Errorf("backoff without next_restart_at: %s", harness.FormatService(st.Service("s3", "crash")))
	}
	start := time.Now()
	_, err := d.Client().StopService(d.Ctx(), connect.NewRequest(&v1.StopServiceRequest{Project: "s3", Service: "crash"}))
	harness.NoError(t, err, "StopService")
	if el := time.Since(start); el > harness.Scale(2*time.Second) {
		t.Errorf("StopService during backoff took %s", el)
	}
	st = w.WaitFor(t, "stopped", func(s harness.State) bool { return s.ServiceIs("s3", "crash", "stopped") })
	run := st.Service("s3", "crash").GetRun()
	harness.Consistently(t, "no relaunch after stop", 1500*time.Millisecond, func(c *harness.C) {
		sv := w.State().Service("s3", "crash")
		if sv.GetStatus() != "stopped" || sv.GetRun() != run {
			c.Errorf("relaunched: %s", harness.FormatService(sv))
		}
	})
	_, err = d.Client().StartService(d.Ctx(), connect.NewRequest(&v1.StartServiceRequest{Project: "s3", Service: "crash"}))
	harness.NoError(t, err, "StartService")
	w.WaitFor(t, "new run after explicit start", func(s harness.State) bool {
		return s.Service("s3", "crash").GetRun() > run
	})
	for _, e := range w.ServiceEvents("s3", "crash") {
		if e.Run == run+1 {
			if e.Restarts != 0 {
				t.Errorf("restarts not reset by an explicit start: first event of run %d has restarts=%d", e.Run, e.Restarts)
			}
			break
		}
	}
}

// S7 + S16: stop escalates SIGTERM -> SIGKILL, and Kill works while a stop
// is in progress.
func TestLedger_S7_KillDuringStop(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s7", `version: "1"
services:
  trap:
    command: {{fixture "sigtrap"}}
    stop_grace_period: 30s
`)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("s7", "trap") })
	pid := st.ServicePid("s7", "trap")
	d.WaitLog("s7", harness.Svc("trap"), "sigtrap ready")
	stopDone := make(chan error, 1)
	go func() {
		_, err := d.Client().StopService(d.Ctx(), connect.NewRequest(&v1.StopServiceRequest{Project: "s7", Service: "trap"}))
		stopDone <- err
	}()
	w.WaitFor(t, "stopping", func(s harness.State) bool { return s.ServiceIs("s7", "trap", "stopping") })
	killedAt := time.Now()
	_, err := d.Client().KillService(d.Ctx(), connect.NewRequest(&v1.KillServiceRequest{Project: "s7", Service: "trap", Signal: "SIGKILL"}))
	harness.NoError(t, err, "KillService during stop")
	w.WaitForWithin(t, 3*time.Second, "stopped right after kill", func(s harness.State) bool {
		return s.ServiceIs("s7", "trap", "stopped") && !harness.GroupAlive(pid)
	})
	select {
	case err := <-stopDone:
		harness.NoError(t, err, "StopService")
	case <-time.After(harness.Scale(3 * time.Second)):
		t.Fatalf("StopService still blocked %s after the kill", time.Since(killedAt))
	}
}

func TestLedger_S16_StopEscalatesToSIGKILL(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s16", `version: "1"
services:
  trap:
    command: {{fixture "sigtrap"}}
    stop_grace_period: 500ms
`)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("s16", "trap") })
	pid := st.ServicePid("s16", "trap")
	d.WaitLog("s16", harness.Svc("trap"), "sigtrap ready")
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(45*time.Second))
	defer cancel()
	start := time.Now()
	_, err := d.Client().StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "s16", Service: "trap"}))
	harness.NoError(t, err, "StopService (must escalate)")
	if el := time.Since(start); el < 400*time.Millisecond || el > harness.Scale(5*time.Second) {
		t.Errorf("stop took %s; want about stop_grace_period (500ms) before SIGKILL", el)
	}
	if harness.GroupAlive(pid) {
		t.Fatalf("StopService returned while the process group %d is alive", pid)
	}
	w.WaitFor(t, "stopped", func(s harness.State) bool { return s.ServiceIs("s16", "trap", "stopped") })
	logs := d.Logs(&v1.LogsRequest{Project: "s16", Sources: []*v1.LogSource{harness.Svc("trap")}})
	if !harness.ContainsText(logs, "sigtrap got terminated") {
		t.Errorf("SIGTERM was not sent before SIGKILL:\n%s", harness.FormatLogLines(logs))
	}
}

// S7: a non-fatal signal doesn't leave the service stuck in "stopping".
func TestLedger_S7_NonFatalSignalKeepsRunning(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s7b", `version: "1"
services:
  trap:
    command: {{fixture "sigtrap"}}
    stop_grace_period: 500ms
`)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("s7b", "trap") })
	pid := st.ServicePid("s7b", "trap")
	d.WaitLog("s7b", harness.Svc("trap"), "sigtrap ready")
	_, err := d.Client().KillService(d.Ctx(), connect.NewRequest(&v1.KillServiceRequest{Project: "s7b", Service: "trap", Signal: "SIGHUP"}))
	harness.NoError(t, err, "KillService SIGHUP")
	f := d.FollowLogs(&v1.LogsRequest{Project: "s7b", Sources: []*v1.LogSource{harness.Svc("trap")}})
	f.WaitForText(t, "sigtrap got hangup")
	harness.Consistently(t, "still running, same pid", time.Second, func(c *harness.C) {
		s := w.State()
		if !s.Running("s7b", "trap") || s.ServicePid("s7b", "trap") != pid {
			c.Errorf("%s", harness.FormatService(s.Service("s7b", "trap")))
		}
	})
	_, err = d.Client().KillService(d.Ctx(), connect.NewRequest(&v1.KillServiceRequest{Project: "s7b", Service: "trap"}))
	harness.NoError(t, err, "KillService default")
	w.WaitFor(t, "killed", func(s harness.State) bool {
		return s.ServiceIs("s7b", "trap", "exited", "failed", "stopped") && !harness.GroupAlive(pid)
	})
}

// S8: after any interleaving of commands, a final stop leaves the service
// stopped (never stuck in stopping/backoff), and start recovers it.
func TestLedger_S8_NoStuckStates(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s8", `version: "1"
services:
  flappy:
    command: {{fixture "exiter"}} -code 1 -after 100ms
    restart: always
  steady:
    command: {{fixture "ticker"}} -interval 100ms
`)
	w.WaitFor(t, "steady running", func(s harness.State) bool { return s.Running("s8", "steady") })
	ctx := d.Ctx()
	c := d.Client()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		for _, svc := range []string{"flappy", "steady"} {
			svc := svc
			wg.Add(4)
			go func() {
				defer wg.Done()
				_, _ = c.StartService(ctx, connect.NewRequest(&v1.StartServiceRequest{Project: "s8", Service: svc}))
			}()
			go func() {
				defer wg.Done()
				_, _ = c.StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "s8", Service: svc}))
			}()
			go func() {
				defer wg.Done()
				_, _ = c.RestartService(ctx, connect.NewRequest(&v1.RestartServiceRequest{Project: "s8", Service: svc}))
			}()
			go func() {
				defer wg.Done()
				_, _ = c.KillService(ctx, connect.NewRequest(&v1.KillServiceRequest{Project: "s8", Service: svc}))
			}()
		}
	}
	waitGroup(t, &wg, 60*time.Second, "command storm")
	for _, svc := range []string{"flappy", "steady"} {
		start := time.Now()
		_, err := c.StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "s8", Service: svc}))
		harness.NoError(t, err, "final StopService "+svc)
		if el := time.Since(start); el > harness.Scale(5*time.Second) {
			t.Errorf("final stop of %s took %s", svc, el)
		}
	}
	w.WaitForWithin(t, 5*time.Second, "both stopped", func(s harness.State) bool { return s.AllServicesIn("s8", "stopped") })
	harness.Consistently(t, "stay stopped", time.Second, func(cc *harness.C) {
		if s := w.State(); !s.AllServicesIn("s8", "stopped") {
			cc.Errorf("%s", s)
		}
	})
	for _, svc := range []string{"flappy", "steady"} {
		_, err := c.StartService(ctx, connect.NewRequest(&v1.StartServiceRequest{Project: "s8", Service: svc}))
		harness.NoError(t, err, "StartService "+svc)
	}
	w.WaitFor(t, "steady recovers", func(s harness.State) bool { return s.Running("s8", "steady") })
	w.WaitFor(t, "flappy relaunched", func(s harness.State) bool {
		return s.ServiceIs("s8", "flappy", "starting", "running", "exited", "backoff", "failed")
	})
}

// S10 / O6: a grandchild that escapes the process group and holds stdout
// must not wedge stop, status, or later commands.
func TestLedger_S10_EscapedGrandchildDoesNotWedge(t *testing.T) {
	t.Parallel()
	sb, _, d, w := setup(t, "s10", `version: "1"
services:
  forker:
    command: {{fixture "forker"}} -hold 5m
  quick:
    command: {{fixture "forker"}} -hold 5m -exit-after 300ms
`)
	t.Cleanup(func() { killForkerGrandchildren(sb) })

	// A parent that exits while its grandchild holds stdout: the service
	// must be reported as exited, not stay "running".
	w.WaitForWithin(t, 15*time.Second, "quick exited", func(s harness.State) bool {
		return s.ServiceIs("s10", "quick", "exited", "failed")
	})

	w.WaitFor(t, "forker running", func(s harness.State) bool { return s.Running("s10", "forker") })
	d.WaitLog("s10", harness.Svc("forker"), "forker ready")
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(30*time.Second))
	defer cancel()
	start := time.Now()
	_, err := d.Client().StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "s10", Service: "forker"}))
	harness.NoError(t, err, "StopService with an escaped grandchild")
	t.Logf("stop took %s", time.Since(start))
	w.WaitFor(t, "forker stopped", func(s harness.State) bool { return s.ServiceIs("s10", "forker", "stopped") })

	stateStart := time.Now()
	d.State()
	if el := time.Since(stateStart); el > harness.Scale(2*time.Second) {
		t.Errorf("GetState took %s after the wedge scenario", el)
	}
	// Later project commands still work.
	_, err = d.Client().StopProject(ctx, connect.NewRequest(&v1.StopProjectRequest{Project: "s10"}))
	harness.NoError(t, err, "StopProject")
	_, err = d.Client().StartProject(ctx, connect.NewRequest(&v1.StartProjectRequest{Project: "s10", Services: []string{"forker"}}))
	harness.NoError(t, err, "StartProject")
	w.WaitFor(t, "forker running again", func(s harness.State) bool { return s.Running("s10", "forker") })
	// Let it spawn its grandchild so cleanup can find and kill it.
	d.WaitLog("s10", harness.Svc("forker"), "forker ready")
}

func killForkerGrandchildren(sb *harness.Sandbox) {
	for _, p := range sb.TaggedProcesses() {
		if strings.HasSuffix(p.Exe, "/forker") || strings.Contains(p.Command, "forker -grandchild") {
			_ = syscall.Kill(p.Pid, syscall.SIGKILL)
		}
	}
}

// S12: CPU is reported in percent of one core (Apple Silicon unit bug).
func TestLedger_S12_StatsCPUPercent(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "s12", `version: "1"
services:
  busy:
    command: {{fixture "ticker"}} -busy -interval 1s
  idle:
    command: {{fixture "ticker"}} -interval 1s
`)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.AllRunning("s12") })
	d.WaitLog("s12", harness.Svc("busy"), "tick 1")
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(20*time.Second))
	defer cancel()
	stream, err := d.Client().Stats(ctx, connect.NewRequest(&v1.StatsRequest{Project: "s12", IntervalMs: 500}))
	harness.NoError(t, err, "Stats")
	defer func() { _ = stream.Close() }()
	var maxBusy, maxIdle float64
	msgs := 0
	for stream.Receive() && msgs < 6 {
		msgs++
		for _, ps := range stream.Msg().GetStats() {
			switch ps.GetName() {
			case "busy":
				maxBusy = max(maxBusy, ps.GetCpuPercent())
				if ps.GetPid() != int32(st.ServicePid("s12", "busy")) {
					t.Errorf("stats pid %d != service pid %d", ps.GetPid(), st.ServicePid("s12", "busy"))
				}
				if ps.GetRssBytes() == 0 {
					t.Errorf("busy rss = 0")
				}
			case "idle":
				maxIdle = max(maxIdle, ps.GetCpuPercent())
			}
		}
	}
	if msgs < 3 {
		t.Fatalf("got %d stats messages: %v", msgs, stream.Err())
	}
	if maxBusy < 40 {
		t.Errorf("busy loop cpu%% = %.1f, want >= 40 (percent of one core)", maxBusy)
	}
	if maxIdle >= maxBusy {
		t.Errorf("idle cpu%% %.1f >= busy %.1f", maxIdle, maxBusy)
	}
}

func TestAPI_ListPorts(t *testing.T) {
	t.Parallel()
	_, p, d, w := setup(t, "ports", `version: "1"
services:
  web:
    command: {{fixture "httpecho"}}
    port: {{port "web"}}
    env:
      PORT: "{{port "web"}}"
`)
	w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("ports", "web") })
	want := int32(p.Port("web"))
	harness.Eventually(t, "port binding reported", func(c *harness.C) {
		resp, err := d.Client().ListPorts(d.Ctx(), connect.NewRequest(&v1.ListPortsRequest{Project: "ports"}))
		if err != nil {
			c.Fatalf("ListPorts: %v", err)
		}
		for _, b := range resp.Msg.GetPorts() {
			if b.GetService() == "web" && b.GetPort() == want {
				return
			}
		}
		c.Errorf("no binding for web:%d in %v", want, resp.Msg.GetPorts())
	})
}

func TestAPI_ServiceSpecExposed(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("spec", `version: "1"
services:
  db:
    command: {{fixture "ticker"}} -interval 1s
    healthcheck:
      test: ["CMD-SHELL", "true"]
      interval: 200ms
  api:
    command: {{fixture "ticker"}} -interval 1s
    working_dir: sub
    restart: on-failure
    env:
      SECRET_TOKEN: hunter2
    depends_on:
      db: { condition: service_healthy }
`, map[string]string{"sub/.keep": ""})
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	st := w.WaitFor(t, "api running", func(s harness.State) bool { return s.Running("spec", "api") })
	spec := st.Service("spec", "api").GetSpec()
	if spec.GetRestart() != "on-failure" || !strings.HasSuffix(spec.GetWorkingDir(), "/sub") {
		t.Errorf("spec: %v", spec)
	}
	if len(spec.GetDependsOn()) != 1 || spec.GetDependsOn()[0].GetName() != "db" || spec.GetDependsOn()[0].GetCondition() != "service_healthy" {
		t.Errorf("depends_on: %v", spec.GetDependsOn())
	}
	found := false
	for _, k := range spec.GetEnvKeys() {
		if k == "SECRET_TOKEN" {
			found = true
		}
		if strings.Contains(k, "hunter2") {
			t.Errorf("env value leaked in env_keys: %q", k)
		}
	}
	if !found {
		t.Errorf("env_keys missing SECRET_TOKEN: %v", spec.GetEnvKeys())
	}
	if hc := st.Service("spec", "db").GetSpec().GetHealthcheck(); hc.GetIntervalMs() != 200 {
		t.Errorf("healthcheck spec: %v", hc)
	}
}
