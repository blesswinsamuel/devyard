package api_test

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

// The db healthcheck passes while <project>/db.healthy exists.
const healthProject = `services:
  db:
    run: {{fixture "ticker"}} -interval 1s
    restart: always
    ready:
      exec: "test -f {{.Dir}}/db.healthy"
      interval: 100ms
      timeout: 1s
      retries: %d
      start_period: %s
  api:
    run: {{fixture "ticker"}} -interval 1s
    depends_on: [db]
`

func healthCfg(retries int, startPeriod string) string {
	return fmt.Sprintf(healthProject, retries, startPeriod)
}

// S9: a dependent waits until its dependency is ready (healthy).
func TestLedger_S9_DependentWaitsForHealthy(t *testing.T) {
	t.Parallel()
	_, p, _, w := setup(t, "s9wait", healthCfg(2, "30s"))
	w.WaitFor(t, "db running, api waiting", func(s harness.State) bool {
		return s.Running("s9wait", "db") && s.ServiceIs("s9wait", "api", "waiting")
	})
	harness.Consistently(t, "api keeps waiting while db is not healthy", time.Second, func(c *harness.C) {
		s := w.State()
		if s.Service("s9wait", "api").GetPid() != 0 || s.Running("s9wait", "api") {
			c.Errorf("api started early: %s", harness.FormatService(s.Service("s9wait", "api")))
		}
		if h := s.Service("s9wait", "db").GetHealth(); h == "healthy" {
			c.Errorf("db healthy without the health file")
		}
	})
	if msg := w.State().Service("s9wait", "api").GetMessage(); msg == "" {
		t.Errorf("waiting service has no message explaining what it waits for")
	}
	p.WriteFile("db.healthy", "")
	w.WaitFor(t, "db healthy and api running", func(s harness.State) bool {
		return s.Service("s9wait", "db").GetHealth() == "healthy" && s.Running("s9wait", "api")
	})
}

// S9: health is per run: after the dependency crashes and restarts, it
// isn't reported healthy until a probe of the new run passes, and a
// restarted dependent waits again. (retries: 100 keeps the new run in
// "starting" rather than "unhealthy", so the dependent waits instead of
// failing fast.)
func TestLedger_S9_CrashResetsHealth(t *testing.T) {
	t.Parallel()
	_, p, d, w := setup(t, "s9crash", healthCfg(100, "0s"))
	p.WriteFile("db.healthy", "")
	st := w.WaitFor(t, "both up", func(s harness.State) bool {
		return s.Service("s9crash", "db").GetHealth() == "healthy" && s.Running("s9crash", "api")
	})
	oldRun := st.Service("s9crash", "db").GetRun()
	if err := os.Remove(p.Path("db.healthy")); err != nil {
		t.Fatal(err)
	}
	// Crash it behind the daemon's back (KillService is an explicit stop
	// and doesn't trigger the restart policy).
	if err := syscall.Kill(-st.ServicePid("s9crash", "db"), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.WaitFor(t, "db relaunched", func(s harness.State) bool {
		return s.Service("s9crash", "db").GetRun() > oldRun && s.Running("s9crash", "db")
	})
	harness.Consistently(t, "new run is not healthy", 1500*time.Millisecond, func(c *harness.C) {
		sv := w.State().Service("s9crash", "db")
		if sv.GetRun() > oldRun && sv.GetHealth() == "healthy" {
			c.Errorf("stale health carried into run %d: %s", sv.GetRun(), harness.FormatService(sv))
		}
	})
	for _, e := range w.ServiceEvents("s9crash", "db") {
		if e.Run > oldRun && e.Health == "healthy" {
			t.Errorf("event reported the new run healthy: %s", e)
		}
	}
	// A restarted dependent must wait for the new run's health.
	_, err := d.Client().RestartService(d.Ctx(), connect.NewRequest(&v1.RestartServiceRequest{Project: "s9crash", Service: "api"}))
	harness.NoError(t, err, "RestartService api")
	w.WaitFor(t, "api waiting on db", func(s harness.State) bool { return s.ServiceIs("s9crash", "api", "waiting") })
	p.WriteFile("db.healthy", "")
	w.WaitFor(t, "api running once db is healthy again", func(s harness.State) bool { return s.Running("s9crash", "api") })
}

// S9: failures during start_period don't count.
func TestLedger_S9_StartPeriodSuppressesEarlyFailures(t *testing.T) {
	t.Parallel()
	_, p, _, w := setup(t, "s9sp", healthCfg(1, "2s"))
	st := w.WaitFor(t, "db running", func(s harness.State) bool { return s.Running("s9sp", "db") })
	started := time.UnixMilli(st.Service("s9sp", "db").GetStartedAtUnixMs())
	harness.Consistently(t, "not unhealthy during start_period", 1500*time.Millisecond, func(c *harness.C) {
		sv := w.State().Service("s9sp", "db")
		if sv.GetHealth() == "unhealthy" && time.Since(started) < 2*time.Second {
			c.Errorf("unhealthy %s after start (start_period 2s)", time.Since(started))
		}
	})
	w.WaitFor(t, "unhealthy after start_period", func(s harness.State) bool {
		return s.Service("s9sp", "db").GetHealth() == "unhealthy"
	})
	if s := w.State(); s.Service("s9sp", "db").GetHealthDetail() == "" {
		t.Errorf("unhealthy without health_detail")
	}
	p.WriteFile("db.healthy", "")
	w.WaitFor(t, "recovers to healthy", func(s harness.State) bool {
		return s.Service("s9sp", "db").GetHealth() == "healthy"
	})
}
