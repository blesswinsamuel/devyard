package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

// threeTickers is alpha <- beta <- gamma with both depends_on forms.
const threeTickers = `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -prefix tick-a -interval 100ms
    restart: always
  beta:
    command: {{fixture "ticker"}} -prefix tick-b -interval 100ms
    depends_on: [alpha]
    restart: always
  gamma:
    command: {{fixture "ticker"}} -prefix tick-g -interval 100ms
    depends_on:
      alpha: { condition: service_started }
      beta: { condition: service_started }
    restart: always
`

func TestCLI_Version(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	r := sb.CLI("version").MustSucceed(t)
	if strings.TrimSpace(r.Stdout) == "" {
		t.Fatalf("version printed nothing:\n%s", r)
	}
	if sb.DaemonRunning() {
		t.Fatalf("`devyard version` started a daemon")
	}
}

func TestCLI_Lifecycle_StartStatusLogsRestartStopRemove(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("lc", threeTickers, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w, "alpha", "beta", "gamma")

	// status -o json agrees with the API.
	list := p.StatusJSON()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		s := harness.FindService(list, name)
		if s == nil {
			t.Fatalf("status -o json missing %s: %+v", name, list)
		}
		if s.Status != "running" || s.Pid != st.ServicePid("lc", name) {
			t.Errorf("status %s = %+v, API pid %d", name, *s, st.ServicePid("lc", name))
		}
	}

	// logs <svc>.
	harness.Eventually(t, "logs beta shows ticks", func(c *harness.C) {
		r := p.CLI("logs", "beta")
		if r.Code != 0 || !strings.Contains(r.Stdout, "tick-b 1") {
			c.Errorf("%s", r)
		}
	})

	// logs -f streams new lines.
	follow := sb.CLIStart(harness.RunOpts{Dir: p.Dir}, "logs", "-f", "gamma")
	follow.WaitOutput(t, "tick-g")
	first := harness.MaxSeq(follow.Stdout(), "tick-g")
	harness.Eventually(t, "logs -f keeps streaming", func(c *harness.C) {
		if n := harness.MaxSeq(follow.Stdout(), "tick-g"); n < first+5 {
			c.Errorf("max seq %d, started at %d", n, first)
		}
	})
	follow.Kill()

	// restart beta: new pid; alpha untouched.
	oldBeta, alphaPid := st.ServicePid("lc", "beta"), st.ServicePid("lc", "alpha")
	p.CLI("restart", "beta").MustSucceed(t)
	st = w.WaitFor(t, "beta restarted with a new pid", func(s harness.State) bool {
		return s.Running("lc", "beta") && s.ServicePid("lc", "beta") != oldBeta
	})
	if got := st.ServicePid("lc", "alpha"); got != alphaPid {
		t.Errorf("restart beta changed alpha's pid %d -> %d", alphaPid, got)
	}
	newBeta := st.ServicePid("lc", "beta")

	// logs shows the current run; --previous shows only the previous run.
	harness.Eventually(t, "current run logs", func(c *harness.C) {
		r := p.CLI("logs", "beta")
		if !strings.Contains(r.Stdout, fmt.Sprintf("pgid=%d", newBeta)) {
			c.Errorf("current logs missing new run start:\n%s", r)
		}
		if strings.Contains(r.Stdout, fmt.Sprintf("pgid=%d", oldBeta)) {
			c.Fatalf("current logs contain the previous run:\n%s", r)
		}
	})
	prev := p.CLI("logs", "--previous", "beta").MustSucceed(t)
	if !strings.Contains(prev.Stdout, fmt.Sprintf("pgid=%d", oldBeta)) || strings.Contains(prev.Stdout, fmt.Sprintf("pgid=%d", newBeta)) {
		t.Errorf("logs --previous should contain exactly the previous run:\n%s", prev)
	}
	p.CLI("logs", "--previous", "--follow", "beta").MustFail(t)

	// stop: everything stops, the project stays registered.
	pids := []int{st.ServicePid("lc", "alpha"), newBeta, st.ServicePid("lc", "gamma")}
	p.CLI("stop").MustSucceed(t)
	st = w.WaitFor(t, "all services stopped", func(s harness.State) bool {
		return s.AllServicesIn("lc", "stopped")
	})
	if pr := st.Project("lc"); pr == nil || pr.GetDesired() != "stopped" || pr.GetStatus() != "stopped" {
		t.Errorf("project after stop: %v", pr)
	}
	for _, pid := range pids {
		if harness.GroupAlive(pid) {
			t.Errorf("process group %d survived stop", pid)
		}
	}

	// project remove <id>: gone from the daemon; -p lc now errors.
	p.CLI("project", "remove", "lc").MustSucceed(t)
	w.WaitFor(t, "project removed", func(s harness.State) bool {
		return s.Project("lc") == nil && len(s.ServicesOf("lc")) == 0
	})
	sb.CLI("-p", "lc", "status").MustFail(t)
}

func TestCLI_StatusJSONFields(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("js", `version: "1"
services:
  ok:
    command: {{fixture "ticker"}} -interval 1s
  bad:
    command: {{fixture "exiter"}} -code 3
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	w.WaitFor(t, "ok running, bad exited", func(s harness.State) bool {
		return s.Running("js", "ok") && s.ServiceIs("js", "bad", "exited", "failed")
	})
	r := p.CLI("status", "-o", "json").MustSucceed(t)
	var raw []map[string]any
	r.JSON(t, &raw)
	byName := map[string]map[string]any{}
	for _, m := range raw {
		name, _ := m["name"].(string)
		byName[name] = m
	}
	if m := byName["ok"]; m == nil || m["status"] != "running" || m["pid"] == nil || m["project"] != "js" {
		t.Errorf("ok entry: %v", m)
	}
	if m := byName["bad"]; m == nil || m["exitCode"] != float64(3) {
		t.Errorf("bad entry should carry camelCase exitCode=3: %v", m)
	}
	for _, m := range raw {
		for k := range m {
			if strings.Contains(k, "_") {
				t.Errorf("field %q is not camelCase (want protojson names)", k)
			}
		}
	}
	// `ps` is an alias of status.
	var alias []harness.ServiceJSON
	p.CLI("ps", "-o", "json").MustSucceed(t).JSON(t, &alias)
	if len(alias) != len(raw) {
		t.Errorf("ps and status disagree: %d vs %d entries", len(alias), len(raw))
	}
}

func TestCLI_StatusAllProjects(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	a := sb.WriteProject("pa", `version: "1"
services:
  one:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	b := sb.WriteProject("pb", `version: "1"
services:
  two:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	a.Start()
	b.Start()
	w := sb.Daemon().Watch(context.Background())
	w.WaitFor(t, "both running", func(s harness.State) bool { return s.Running("pa", "one") && s.Running("pb", "two") })
	var all []harness.ServiceJSON
	a.CLI("status", "-a", "-o", "json").MustSucceed(t).JSON(t, &all)
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.Project+"/"+s.Name] = true
	}
	if !seen["pa/one"] || !seen["pb/two"] {
		t.Fatalf("status -a missing entries: %+v", all)
	}
	var mine []harness.ServiceJSON
	a.CLI("status", "-o", "json").MustSucceed(t).JSON(t, &mine)
	for _, s := range mine {
		if s.Project != "pa" {
			t.Errorf("status in pa's dir listed %s/%s", s.Project, s.Name)
		}
	}
}

func TestCLI_StartFollowStopsProjectOnSignal(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			sb := harness.New(t)
			p := sb.WriteProject("fg", `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -prefix tick-a -interval 100ms
  beta:
    command: {{fixture "ticker"}} -prefix tick-b -interval 100ms
`, nil)
			proc := sb.CLIStart(harness.RunOpts{Dir: p.Dir}, "start", "-f")
			proc.WaitOutput(t, "tick-a 3")
			proc.WaitOutput(t, "tick-b 3")
			// Merged output is prefixed with the service name.
			if !regexp.MustCompile(`(?m)alpha.*tick-a \d+`).MatchString(proc.Stdout()) {
				t.Errorf("merged output lines are not prefixed with the service name:\n%s", proc.Stdout())
			}
			d := sb.Daemon()
			proc.Signal(sig)
			proc.Wait(t, 30*time.Second)
			harness.Eventually(t, "project stopped after signal", func(c *harness.C) {
				st := d.State()
				if !st.AllServicesIn("fg", "stopped") {
					c.Errorf("%s", st)
				}
				if st.Project("fg").GetDesired() != "stopped" {
					c.Errorf("desired=%q", st.Project("fg").GetDesired())
				}
			})
		})
	}
}

func TestCLI_BuildStringAndObjectForms(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("bld", `version: "1"
services:
  maker:
    command: {{fixture "ticker"}} -interval 1s
    build: echo built-maker-string-form
  shaper:
    command: {{fixture "ticker"}} -interval 1s
    build:
      command: echo built-shaper-object-form && echo shaper-env=$SHAPER_ENV
      env:
        SHAPER_ENV: "yes"
      shell: sh
`, nil)
	r := p.CLI("build").MustSucceed(t)
	for _, want := range []string{"built-maker-string-form", "built-shaper-object-form", "shaper-env=yes"} {
		if !strings.Contains(r.Output(), want) {
			t.Errorf("build output missing %q:\n%s", want, r)
		}
	}
	r = p.CLI("build", "maker").MustSucceed(t)
	if strings.Contains(r.Output(), "built-shaper") {
		t.Errorf("build maker also built shaper:\n%s", r)
	}
}

func TestCLI_StartWithBuildRunsBuildFirst(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("sbld", `version: "1"
services:
  svc:
    command: sh -c 'test -f built.marker && echo saw-build; exec {{fixture "ticker"}} -interval 1s'
    build: touch built.marker
`, nil)
	p.Start("--build")
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w, "svc")
	harness.Eventually(t, "service saw the build output", func(c *harness.C) {
		if r := p.CLI("logs", "svc"); !strings.Contains(r.Stdout, "saw-build") {
			c.Errorf("%s", r)
		}
	})
}

func TestCLI_BuildFailureAbortsStart(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("bfail", `version: "1"
services:
  svc:
    command: {{fixture "exiter"}} -count-file started -after 1m
    build: sh -c 'echo build-broke >&2; exit 1'
`, nil)
	p.CLI("start", "--build")
	d := sb.Daemon()
	w := d.Watch(context.Background())
	w.WaitFor(t, "svc failed on build", func(s harness.State) bool {
		sv := s.Service("bfail", "svc")
		return sv != nil && (sv.GetStatus() == "failed" || strings.Contains(sv.GetMessage(), "build"))
	})
	harness.Consistently(t, "service never launched", time.Second, func(c *harness.C) {
		if s := w.State(); s.Running("bfail", "svc") {
			c.Errorf("service running on top of a failed build")
		}
	})
}

func TestCLI_ConfigErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, cfg, want string
	}{
		{"cycle", `version: "1"
services:
  a: { command: echo a, depends_on: [b] }
  b: { command: echo b, depends_on: [a] }
`, "cycle"},
		{"empty-command", `version: "1"
services:
  s:
    command: ""
`, "command"},
		{"unknown-dependency", `version: "1"
services:
  s: { command: echo s, depends_on: [ghost] }
`, "ghost"},
		{"invalid-yaml", "version: \"1\"\nservices: [\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := harness.New(t)
			p := sb.WriteProject("bad-"+tc.name, tc.cfg, nil)
			r := p.CLI("start").MustFail(t)
			if tc.want != "" && !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("stderr missing %q:\n%s", tc.want, r)
			}
			if sb.DaemonRunning() {
				if st := sb.Daemon().State(); st.Project(p.ID) != nil && st.AllRunning(p.ID) {
					t.Errorf("invalid config started services: %s", st)
				}
			}
		})
	}
	t.Run("missing-file", func(t *testing.T) {
		t.Parallel()
		sb := harness.New(t)
		sb.CLI("--file", sb.Home+"/nope/devyard.yml", "start").MustFail(t)
	})
	t.Run("no-config-in-cwd", func(t *testing.T) {
		t.Parallel()
		sb := harness.New(t)
		sb.CLI("start").MustFail(t)
	})
}

func TestCLI_ProjectListJSON(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("listed", `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
	harness.Eventually(t, "project list shows the running project", func(c *harness.C) {
		r := sb.CLI("project", "list", "-o", "json")
		var list []harness.ProjectJSON
		if err := json.Unmarshal([]byte(r.Stdout), &list); err != nil {
			c.Fatalf("decode: %v\n%s", err, r)
		}
		for _, pr := range list {
			if pr.ID == "listed" {
				if pr.Status != "running" || pr.ConfigPath != p.ConfigPath {
					c.Errorf("entry: %+v (want status running, config %s)", pr, p.ConfigPath)
				}
				return
			}
		}
		c.Errorf("project missing: %s", r.Stdout)
	})
	if r := sb.CLI("project", "list").MustSucceed(t); !strings.Contains(r.Stdout, "listed") {
		t.Errorf("table output missing project:\n%s", r)
	}
}

func TestCLI_ProjectByIDFromAnyDir(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("reg", `version: "1"
services:
  app:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)

	// From HOME (no devyard.yml anywhere above it).
	sb.CLI("-p", "reg", "stop").MustSucceed(t)
	w.WaitFor(t, "stopped", func(s harness.State) bool { return s.ServiceIs("reg", "app", "stopped") })
	sb.CLI("-p", "reg", "start").MustSucceed(t)
	st := w.WaitFor(t, "running again", func(s harness.State) bool { return s.Running("reg", "app") })
	var list []harness.ServiceJSON
	sb.CLI("-p", "reg", "status", "-o", "json").MustSucceed(t).JSON(t, &list)
	if s := harness.FindService(list, "app"); s == nil || s.Pid != st.ServicePid("reg", "app") {
		t.Errorf("status -p reg: %+v", list)
	}
}

func TestCLI_ProjectAddAndStart(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("added", `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	sb.CLI("project", "add", p.ConfigPath).MustSucceed(t)
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
	sb.CLI("project", "stop", "added").MustSucceed(t)
	w.WaitFor(t, "stopped", func(s harness.State) bool { return s.AllServicesIn("added", "stopped") })
	sb.CLI("project", "restart", "added").MustSucceed(t)
	p.WaitRunning(w)
}

func TestCLI_KillDefaultSIGKILLAndSignalFlag(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("kl", `version: "1"
services:
  trap:
    command: {{fixture "sigtrap"}}
    stop_grace_period: 500ms
  tick:
    command: {{fixture "ticker"}} -interval 100ms
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	st := p.WaitRunning(w)
	trapPid, tickPid := st.ServicePid("kl", "trap"), st.ServicePid("kl", "tick")

	// Default signal is SIGKILL: works even though sigtrap ignores SIGTERM.
	p.CLI("kill", "trap").MustSucceed(t)
	w.WaitFor(t, "trap killed", func(s harness.State) bool {
		return s.ServiceIs("kl", "trap", "exited", "failed", "stopped") && !harness.GroupAlive(trapPid)
	})
	p.CLI("kill", "-s", "SIGTERM", "tick").MustSucceed(t)
	w.WaitFor(t, "tick terminated", func(s harness.State) bool {
		return s.ServiceIs("kl", "tick", "exited", "failed", "stopped") && !harness.GroupAlive(tickPid)
	})
	if ev := w.ServiceEvents("kl", "tick"); len(ev) == 0 {
		t.Errorf("no watch events for tick")
	}
}

func TestCLI_StopThenStartAndLazyStart(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("lazy", `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -interval 1s
  beta:
    command: {{fixture "ticker"}} -interval 1s
    depends_on:
      alpha: { condition: service_started }
  gamma:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
	p.CLI("stop").MustSucceed(t)
	w.WaitFor(t, "all stopped", func(s harness.State) bool { return s.AllServicesIn("lazy", "stopped") })

	// start <svc> on a stopped project starts it plus its dependency chain.
	p.CLI("start", "beta").MustSucceed(t)
	st := w.WaitFor(t, "alpha+beta running", func(s harness.State) bool { return s.AllRunning("lazy", "alpha", "beta") })
	harness.Consistently(t, "gamma stays stopped", time.Second, func(c *harness.C) {
		if s := w.State(); !s.ServiceIs("lazy", "gamma", "stopped") {
			c.Errorf("gamma %s", s.ServiceStatus("lazy", "gamma"))
		}
	})
	if d := st.Project("lazy").GetDesired(); d != "partial" {
		t.Errorf("desired after start beta = %q, want partial", d)
	}
	p.CLI("start").MustSucceed(t)
	st = p.WaitRunning(w)
	if d := st.Project("lazy").GetDesired(); d != "running" {
		t.Errorf("desired after start = %q, want running", d)
	}
}

func TestCLI_Top(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("top", `version: "1"
services:
  alpha:
    command: {{fixture "ticker"}} -interval 100ms
  bravo:
    command: {{fixture "ticker"}} -interval 100ms
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
	r := p.CLIWithTimeout(30*time.Second, "top")
	r.MustSucceed(t)
	for _, want := range []string{"alpha", "bravo"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("top missing %s:\n%s", want, r)
		}
	}
	r = p.CLIWithTimeout(30*time.Second, "top", "alpha").MustSucceed(t)
	if !strings.Contains(r.Stdout, "alpha") || strings.Contains(r.Stdout, "bravo") {
		t.Errorf("top alpha should show only alpha:\n%s", r)
	}
}

func TestCLI_WebPrintsDashboardURL(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	u := d.WebURLFromCLI()
	if !strings.HasPrefix(u, d.WebURL()) {
		t.Fatalf("devyard web printed %q, daemon web addr is %s", u, d.WebURL())
	}
}

func TestCLI_DaemonStatus(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	pid := d.Pid()
	r := sb.CLI("daemon", "status").MustSucceed(t)
	if !strings.Contains(r.Stdout, fmt.Sprint(pid)) {
		t.Errorf("daemon status does not mention pid %d:\n%s", pid, r)
	}
	d.Stop()
	r = sb.CLI("daemon", "status")
	if out := strings.ToLower(r.Output()); r.Code == 0 && !strings.Contains(out, "not running") && !strings.Contains(out, "stopped") {
		t.Errorf("daemon status after stop should report not running:\n%s", r)
	}
	if sb.DaemonRunning() {
		t.Errorf("`daemon status` started a daemon")
	}
}
