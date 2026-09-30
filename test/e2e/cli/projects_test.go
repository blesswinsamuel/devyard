package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const oneTicker = `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 200ms
`

// O5: -p with an unknown id is an error and never falls back to the cwd's
// project.
func TestLedger_O5_UnknownProjectFlagIsError(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("real", oneTicker, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	pid := p.WaitRunning(w).ServicePid("real", "a")

	for _, args := range [][]string{
		{"-p", "nope", "stop"},
		{"-p", "nope", "restart"},
		{"-p", "nope", "kill", "a"},
		{"-p", "nope", "status"},
		{"-p", "nope", "logs"},
	} {
		r := p.CLI(args...).MustFail(t)
		if !strings.Contains(r.Stderr, "nope") {
			t.Errorf("%v: error should name the unknown project:\n%s", args, r)
		}
	}
	harness.Consistently(t, "cwd project untouched", time.Second, func(c *harness.C) {
		s := w.State()
		if !s.Running("real", "a") || s.ServicePid("real", "a") != pid {
			c.Errorf("real/a changed: %s", harness.FormatService(s.Service("real", "a")))
		}
	})
}

// O5: `project remove` rejects traversal ids and deletes nothing.
func TestLedger_O5_ProjectRemoveRejectsTraversal(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("keep", oneTicker, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	pid := p.WaitRunning(w).ServicePid("keep", "a")

	sentinels := []string{
		filepath.Join(sb.StateHome, "sentinel"),
		filepath.Join(sb.AppStateDir(), "sentinel"),
		filepath.Join(sb.AppStateDir(), "projects", "sentinel"),
		filepath.Join(sb.Home, "sentinel"),
	}
	for _, s := range sentinels {
		if err := os.MkdirAll(filepath.Dir(s), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"..", "../..", ".", "../keep", "keep/..", "keep/../..", "/", "projects/..", "KEEP", " keep"} {
		sb.CLI("project", "remove", id).MustFail(t)
	}
	for _, s := range sentinels {
		if _, err := os.Stat(s); err != nil {
			t.Errorf("sentinel %s deleted: %v", s, err)
		}
	}
	if _, err := os.Stat(sb.ProjectStateDir("keep")); err != nil {
		t.Errorf("project state dir gone: %v", err)
	}
	s := w.State()
	if s.Project("keep") == nil || s.ServicePid("keep", "a") != pid {
		t.Errorf("project keep disturbed: %s", s)
	}
}

// O5: removing an unknown project reports failure.
func TestLedger_O5_ProjectRemoveUnknownFails(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	sb.Daemon()
	sb.CLI("project", "remove", "ghost").MustFail(t)
}

// O17: `project add --no-start` registers without ever starting services.
func TestLedger_O17_ProjectAddNoStart(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("nostart", `version: "1"
services:
  a:
    command: {{fixture "exiter"}} -count-file {{.Dir}}/launches -after 1m
`, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("project", "add", p.ConfigPath, "--no-start").MustSucceed(t)
	w.WaitFor(t, "project registered", func(s harness.State) bool { return s.Project("nostart") != nil })
	harness.Consistently(t, "no service started", 1500*time.Millisecond, func(c *harness.C) {
		s := w.State()
		if sv := s.Service("nostart", "a"); sv.GetPid() != 0 || s.Running("nostart", "a") {
			c.Errorf("service started: %s", harness.FormatService(sv))
		}
		if _, err := os.Stat(p.Path("launches")); err == nil {
			c.Errorf("service process was launched")
		}
	})
	for _, e := range w.ServiceEvents("nostart", "a") {
		if e.Pid != 0 || e.Status == "running" || e.Status == "starting" {
			t.Errorf("transient start observed: %s", e)
		}
	}
	if d := w.State().Project("nostart").GetDesired(); d != "stopped" {
		t.Errorf("desired = %q, want stopped", d)
	}
	sb.CLI("project", "start", "nostart").MustSucceed(t)
	p.WaitRunning(w)
}

// Two checkouts declaring the same name: the second fails loudly (O11).
func TestLedger_O11_SameNameSecondCheckoutFails(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	cfg := `version: "1"
name: dup
services:
  a:
    command: {{fixture "ticker"}} -interval 200ms
`
	first := sb.WriteProject("dup-a", cfg, nil)
	second := sb.WriteProject("dup-b", cfg, nil)
	first.ID, second.ID = "dup", "dup"
	first.Start()
	w := sb.Daemon().Watch(context.Background())
	pid := first.WaitRunning(w).ServicePid("dup", "a")
	r := second.CLI("start").MustFail(t)
	if !strings.Contains(strings.ToLower(r.Stderr), "exists") && !strings.Contains(r.Stderr, first.ConfigPath) {
		t.Errorf("error should explain the conflict:\n%s", r)
	}
	s := w.State()
	if s.Project("dup").GetConfigPath() != first.ConfigPath || s.ServicePid("dup", "a") != pid {
		t.Errorf("first project disturbed: %s", s)
	}
}

// .env next to the config feeds interpolation and the child env; an explicit
// --env-file replaces it.
func TestCLI_DotEnvAndInterpolation(t *testing.T) {
	t.Parallel()
	cfg := `version: "1"
services:
  web:
    command: sh -c 'echo PORT=$PORT; echo INTERP=${PORT}; echo DEFAULTED=${MISSING:-fallback}; echo APP_NAME=$APP_NAME; echo OVERRIDDEN=$OVERRIDDEN; exec {{fixture "ticker"}} -interval 1s'
    env:
      OVERRIDDEN: svc
`
	t.Run("dotenv", func(t *testing.T) {
		t.Parallel()
		sb := harness.New(t)
		p := sb.WriteProject("dotenv", cfg, map[string]string{".env": "PORT=9099\nAPP_NAME=myapp\nOVERRIDDEN=dotenv\n"})
		p.Start()
		harness.Eventually(t, "logs show env", func(c *harness.C) {
			out := p.CLI("logs", "web").Stdout
			for _, want := range []string{"PORT=9099", "INTERP=9099", "DEFAULTED=fallback", "APP_NAME=myapp", "OVERRIDDEN=svc"} {
				if !strings.Contains(out, want) {
					c.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	})
	t.Run("env-file-flag", func(t *testing.T) {
		t.Parallel()
		sb := harness.New(t)
		p := sb.WriteProject("envfile", cfg, map[string]string{".env": "PORT=1111\n", "custom.env": "PORT=7070\n"})
		p.CLI("--env-file", p.Path("custom.env"), "start").MustSucceed(t)
		harness.Eventually(t, "logs show custom env", func(c *harness.C) {
			if out := p.CLI("logs", "web").Stdout; !strings.Contains(out, "PORT=7070") {
				c.Errorf("%s", out)
			}
		})
	})
	t.Run("missing-explicit-env-file", func(t *testing.T) {
		t.Parallel()
		sb := harness.New(t)
		p := sb.WriteProject("noenvfile", cfg, nil)
		p.CLI("--env-file", p.Path("nope.env"), "start").MustFail(t)
	})
}

// O10: services get the environment of the CLI that ran `start`, not the
// daemon's, and keep it across a daemon restart that restarts services.
func TestLedger_O10_LaunchEnvCapturedFromCLI(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	sb.CLIWith(harness.RunOpts{Env: []string{"DY_WHO=daemon"}}, "daemon", "start").MustSucceed(t)
	p := sb.WriteProject("envcap", `version: "1"
services:
  s:
    command: sh -c 'echo who=$DY_WHO; exec {{fixture "ticker"}} -interval 1s'
`, nil)
	p.CLIEnv([]string{"DY_WHO=cli"}, "start").MustSucceed(t)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	pid := p.WaitRunning(w).ServicePid("envcap", "s")
	harness.Eventually(t, "service sees the CLI env", func(c *harness.C) {
		out := p.CLI("logs", "s").Stdout
		if !strings.Contains(out, "who=cli") || strings.Contains(out, "who=daemon") {
			c.Errorf("%s", out)
		}
	})
	d.Restart(true)
	w.WaitFor(t, "restarted with a new pid", func(s harness.State) bool {
		return s.Running("envcap", "s") && s.ServicePid("envcap", "s") != pid
	})
	harness.Eventually(t, "restarted service still sees the captured env", func(c *harness.C) {
		out := p.CLI("logs", "s").Stdout
		if !strings.Contains(out, "who=cli") {
			c.Errorf("%s", out)
		}
	})
}

// O10: reload diffs the config: unchanged services keep running, changed
// ones restart, removed ones stop, new ones start.
func TestLedger_O10_ReloadDiffsConfig(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("rl", `version: "1"
services:
  keep:
    command: {{fixture "ticker"}} -prefix K -interval 200ms
  change:
    command: {{fixture "ticker"}} -prefix C1 -interval 200ms
  drop:
    command: {{fixture "ticker"}} -prefix D -interval 200ms
`, nil)
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	st := p.WaitRunning(w)
	keepPid, changePid, dropPid := st.ServicePid("rl", "keep"), st.ServicePid("rl", "change"), st.ServicePid("rl", "drop")

	p.WriteConfig(`version: "1"
services:
  keep:
    command: {{fixture "ticker"}} -prefix K -interval 200ms
  change:
    command: {{fixture "ticker"}} -prefix C2 -interval 200ms
  added:
    command: {{fixture "ticker"}} -prefix A -interval 200ms
`)
	p.CLI("reload").MustSucceed(t)
	st = w.WaitFor(t, "reload applied", func(s harness.State) bool {
		return s.Running("rl", "change") && s.ServicePid("rl", "change") != changePid &&
			s.Running("rl", "added") && s.Service("rl", "drop") == nil
	})
	if got := st.ServicePid("rl", "keep"); got != keepPid {
		t.Errorf("unchanged service restarted: pid %d -> %d", keepPid, got)
	}
	if harness.GroupAlive(dropPid) {
		t.Errorf("removed service still running (pgid %d)", dropPid)
	}
	harness.Eventually(t, "changed service runs the new spec", func(c *harness.C) {
		if out := p.CLI("logs", "change").Stdout; !strings.Contains(out, "C2 1") {
			c.Errorf("%s", out)
		}
	})
	if sv := st.Service("rl", "change"); !strings.Contains(sv.GetSpec().GetCommand(), "C2") {
		t.Errorf("spec not updated: %q", sv.GetSpec().GetCommand())
	}
}

// O10: the --env-file given at start is remembered by reload and by daemon
// restarts.
func TestLedger_O10_ReloadRemembersEnvFile(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	cfg := func(ver string) string {
		return `version: "1"
services:
  s:
    command: sh -c 'echo val=$VAL; exec {{fixture "ticker"}} -interval 1s'
    env:
      VER: "` + ver + `"
`
	}
	p := sb.WriteProject("rlenv", cfg("1"), map[string]string{".env": "VAL=dotenv\n", "custom.env": "VAL=custom\n"})
	p.CLI("--env-file", p.Path("custom.env"), "start").MustSucceed(t)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	st := p.WaitRunning(w)
	if got := st.Project("rlenv").GetEnvFile(); got != p.Path("custom.env") {
		t.Errorf("project env_file = %q, want %s", got, p.Path("custom.env"))
	}
	waitLog := func(desc string) {
		t.Helper()
		harness.Eventually(t, desc, func(c *harness.C) {
			out := p.CLI("logs", "s").Stdout
			if !strings.Contains(out, "val=custom") || strings.Contains(out, "val=dotenv") {
				c.Errorf("%s", out)
			}
		})
	}
	waitLog("started with the custom env file")

	pid := st.ServicePid("rlenv", "s")
	p.WriteConfig(cfg("2"))
	p.CLI("reload").MustSucceed(t)
	st = w.WaitFor(t, "changed spec restarted", func(s harness.State) bool {
		return s.Running("rlenv", "s") && s.ServicePid("rlenv", "s") != pid
	})
	waitLog("reload kept the env file")

	pid = st.ServicePid("rlenv", "s")
	d.Restart(true)
	w.WaitFor(t, "restarted by daemon restart -r", func(s harness.State) bool {
		return s.Running("rlenv", "s") && s.ServicePid("rlenv", "s") != pid
	})
	waitLog("daemon restart kept the env file")
}
