// Package chaos_test runs randomized command sequences (including daemon
// SIGKILLs and restarts) against a real daemon and checks the engine's
// invariants: at most one live process per service, every command
// completes within a bound, and a final stop leaves nothing running.
//
// Opt-in: DEVYARD_E2E_CHAOS=1 go test ./test/e2e/chaos/ [-run ...]
// Reproduce with DEVYARD_E2E_CHAOS_SEED=<seed>; DEVYARD_E2E_CHAOS_STEPS
// sets the sequence length.
package chaos_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

func requireChaos(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("DEVYARD_E2E_CHAOS") != "1" {
		t.Skip("set DEVYARD_E2E_CHAOS=1 (and don't pass -short) to run the chaos suite")
	}
}

func seed(t *testing.T) uint64 {
	if s := os.Getenv("DEVYARD_E2E_CHAOS_SEED"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("bad DEVYARD_E2E_CHAOS_SEED: %v", err)
		}
		return v
	}
	return uint64(time.Now().UnixNano())
}

func steps() int {
	if s := os.Getenv("DEVYARD_E2E_CHAOS_STEPS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return 60
}

var services = []string{"tick", "crash", "trap"}

func config(interval string) string {
	return `services:
  tick:
    run: {{fixture "ticker"}} -interval ` + interval + `
    restart: always
    env:
      DY_MARK: chaos-tick
  crash:
    run: {{fixture "exiter"}} -code 1 -after 300ms
    restart: on-failure
    env:
      DY_MARK: chaos-crash
  trap:
    run: {{fixture "sigtrap"}}
    stop: {timeout: 300ms}
    env:
      DY_MARK: chaos-trap
    depends_on: [tick]
tasks:
  job:
    run: {{fixture "ticker"}} -interval 50ms -count 40
    tty: false
`
}

// sampler records any moment with more than one live process per service.
type sampler struct {
	sb   *harness.Sandbox
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	bad  []string
}

func startSampler(sb *harness.Sandbox) *sampler {
	s := &sampler{sb: sb, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			select {
			case <-s.stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
			for _, svc := range services {
				if g := sb.LiveGroups("DY_MARK", "chaos-"+svc); len(g) > 1 {
					s.mu.Lock()
					s.bad = append(s.bad, fmt.Sprintf("%s %s: groups %v", time.Now().Format("15:04:05.000"), svc, g))
					s.mu.Unlock()
				}
			}
		}
	}()
	return s
}

func (s *sampler) finish(t *testing.T) {
	t.Helper()
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bad) > 0 {
		t.Errorf("duplicate live processes observed (%d samples), first: %v", len(s.bad), s.bad[:min(len(s.bad), 5)])
	}
}

func TestChaos_RandomOperations(t *testing.T) {
	requireChaos(t)
	sd := seed(t)
	t.Logf("chaos seed %d (DEVYARD_E2E_CHAOS_SEED=%d to reproduce)", sd, sd)
	rng := rand.New(rand.NewPCG(sd, sd^0x9e3779b97f4a7c15))
	sb := harness.New(t)
	p := sb.WriteProject("chaos", config("100ms"), nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w, "tick")
	smp := startSampler(sb)

	bound := harness.Scale(30 * time.Second)
	intervalToggle := false
	type op struct {
		name   string
		weight int
		run    func(ctx context.Context) error
	}
	svc := func() string { return services[rng.IntN(len(services))] }
	ops := []op{
		{"StartProject", 3, func(ctx context.Context) error {
			_, err := d.Client().StartProject(ctx, connect.NewRequest(&v1.StartProjectRequest{Project: "chaos"}))
			return err
		}},
		{"StopProject", 2, func(ctx context.Context) error {
			_, err := d.Client().StopProject(ctx, connect.NewRequest(&v1.StopProjectRequest{Project: "chaos"}))
			return err
		}},
		{"RestartProject", 1, func(ctx context.Context) error {
			_, err := d.Client().RestartProject(ctx, connect.NewRequest(&v1.RestartProjectRequest{Project: "chaos"}))
			return err
		}},
		{"StartService", 3, func(ctx context.Context) error {
			_, err := d.Client().StartService(ctx, connect.NewRequest(&v1.StartServiceRequest{Project: "chaos", Service: svc()}))
			return err
		}},
		{"StopService", 3, func(ctx context.Context) error {
			_, err := d.Client().StopService(ctx, connect.NewRequest(&v1.StopServiceRequest{Project: "chaos", Service: svc()}))
			return err
		}},
		{"RestartService", 4, func(ctx context.Context) error {
			_, err := d.Client().RestartService(ctx, connect.NewRequest(&v1.RestartServiceRequest{Project: "chaos", Service: svc()}))
			return err
		}},
		{"KillService", 3, func(ctx context.Context) error {
			sig := []string{"SIGKILL", "SIGTERM", "SIGHUP"}[rng.IntN(3)]
			_, err := d.Client().KillService(ctx, connect.NewRequest(&v1.KillServiceRequest{Project: "chaos", Service: svc(), Signal: sig}))
			return err
		}},
		{"Reload", 2, func(ctx context.Context) error {
			intervalToggle = !intervalToggle
			iv := "100ms"
			if intervalToggle {
				iv = "150ms"
			}
			p.WriteConfig(config(iv))
			_, err := d.Client().ReloadProject(ctx, connect.NewRequest(&v1.ReloadProjectRequest{Project: "chaos"}))
			return err
		}},
		{"RunTask", 2, func(ctx context.Context) error {
			_, err := d.Client().RunTask(ctx, connect.NewRequest(&v1.RunTaskRequest{Project: "chaos", Task: "job"}))
			return err
		}},
		{"StopTask", 1, func(ctx context.Context) error {
			_, err := d.Client().StopTask(ctx, connect.NewRequest(&v1.StopTaskRequest{Project: "chaos", Task: "job"}))
			return err
		}},
		{"KillDaemon", 1, func(ctx context.Context) error {
			d.KillHard()
			if r := sb.CLIWith(harness.RunOpts{Timeout: 30 * time.Second}, "daemon", "start"); r.Code != 0 {
				return fmt.Errorf("daemon start after kill: %s", r)
			}
			d.WaitReady()
			return nil
		}},
		{"RestartDaemon", 1, func(ctx context.Context) error {
			d.Restart(rng.IntN(3) == 0)
			return nil
		}},
	}
	total := 0
	for _, o := range ops {
		total += o.weight
	}
	pick := func() op {
		n := rng.IntN(total)
		for _, o := range ops {
			if n < o.weight {
				return o
			}
			n -= o.weight
		}
		return ops[0]
	}
	var history []string
	for i := 0; i < steps(); i++ {
		o := pick()
		// Ops run on the test goroutine (some use t); RPCs are bounded by
		// ctx, CLI calls by the harness timeout.
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		start := time.Now()
		err := o.run(ctx)
		cancel()
		if connect.CodeOf(err) == connect.CodeDeadlineExceeded || time.Since(start) > bound {
			t.Fatalf("step %d %s did not complete within %s\nhistory: %s\n%s", i, o.name, bound, strings.Join(history, ", "), sb.Diagnostics())
		}
		history = append(history, fmt.Sprintf("%s(%s)", o.name, time.Since(start).Round(time.Millisecond)))
		if err != nil {
			switch connect.CodeOf(err) {
			case connect.CodeFailedPrecondition, connect.CodeNotFound, connect.CodeAborted:
				// Legitimate rejections (e.g. task already running).
			default:
				t.Errorf("step %d %s: unexpected error %v (code %v)", i, o.name, err, connect.CodeOf(err))
			}
		}
		if rng.IntN(3) == 0 {
			time.Sleep(time.Duration(rng.IntN(300)) * time.Millisecond)
		}
	}
	t.Logf("history: %s", strings.Join(history, ", "))

	// Final stop leaves nothing running.
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	_, err := d.Client().StopProject(ctx, connect.NewRequest(&v1.StopProjectRequest{Project: "chaos"}))
	harness.NoError(t, err, "final StopProject")
	w.WaitForWithin(t, 30*time.Second, "everything stopped", func(s harness.State) bool {
		tk := s.Task("chaos", "job")
		return s.AllServicesIn("chaos", "stopped") && tk.GetStatus() != "running" && tk.GetStatus() != "stopping"
	})
	harness.Eventually(t, "no live service processes", func(c *harness.C) {
		for _, svc := range services {
			if g := sb.LiveGroups("DY_MARK", "chaos-"+svc); len(g) > 0 {
				c.Errorf("%s: %v", svc, g)
			}
		}
	})
	smp.finish(t)
	w.AssertNoViolations(t)
}

// Restart storms interrupted by a daemon SIGKILL converge to one process
// per service.
func TestChaos_DaemonKillDuringRestartStorm(t *testing.T) {
	requireChaos(t)
	sb := harness.New(t)
	p := sb.WriteProject("storm", config("100ms"), nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w, "tick")
	smp := startSampler(sb)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, _ = d.Client().RestartService(ctx, connect.NewRequest(&v1.RestartServiceRequest{Project: "storm", Service: services[i%len(services)]}))
				cancel()
			}
		}()
	}
	time.Sleep(time.Second)
	d.KillHard()
	time.Sleep(300 * time.Millisecond)
	sb.CLI("daemon", "start").MustSucceed(t)
	d.WaitReady()
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	w.WaitForWithin(t, 30*time.Second, "tick and trap running", func(s harness.State) bool {
		return s.AllRunning("storm", "tick", "trap")
	})
	harness.Consistently(t, "one process per running service", 2*time.Second, func(c *harness.C) {
		for _, svc := range []string{"tick", "trap"} {
			if g := sb.LiveGroups("DY_MARK", "chaos-"+svc); len(g) != 1 {
				c.Errorf("%s: %v", svc, g)
			}
		}
	})
	smp.finish(t)
}
