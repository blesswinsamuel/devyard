package cli_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const driftV1 = `services:
  a:
    run: {{fixture "ticker"}} -interval 200ms
    env: {TOKEN: secret-one}
`

const driftV2 = `services:
  a:
    run: {{fixture "ticker"}} -interval 200ms
    env: {TOKEN: secret-two}
  b:
    run: {{fixture "ticker"}} -interval 300ms
`

func writeGlobalConfig(t *testing.T, sb *harness.Sandbox, content string) {
	t.Helper()
	if err := os.WriteFile(sb.GlobalConfigPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func driftState(s harness.State, id string) string { return s.Project(id).GetDrift().GetState() }

// Editing devyard.yml shows a pending change and touches nothing until it is
// applied with reload.
func TestDrift_PendingThenReload(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("driftp", driftV1, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("add", p.Dir).MustSucceed(t)
	p.WaitRunning(w)
	pid := w.State().ServicePid("driftp", "a")

	p.WriteConfig(driftV2)
	w.WaitFor(t, "drift pending", func(s harness.State) bool { return driftState(s, "driftp") == "pending" })
	drift := w.State().Project("driftp").GetDrift()
	var sawB, sawA bool
	for _, c := range drift.GetChanges() {
		switch c.GetName() {
		case "b":
			sawB = c.GetOp() == "added" && c.GetKind() == "service"
		case "a":
			sawA = c.GetOp() == "changed" && c.GetRestart() && strings.Join(c.GetDetails(), ",") == "env TOKEN changed"
		}
	}
	if !sawA || !sawB {
		t.Fatalf("changes = %v", drift.GetChanges())
	}
	if w.State().Project("driftp").GetReloadPolicy() != "prompt" {
		t.Errorf("policy = %q", w.State().Project("driftp").GetReloadPolicy())
	}
	if w.State().Service("driftp", "b") != nil || w.State().ServicePid("driftp", "a") != pid {
		t.Fatal("the running config changed before it was applied")
	}

	out := sb.CLIIn(p.Dir, "diff").MustSucceed(t).Stdout
	for _, want := range []string{"2 pending changes", "+ service b", "~ service a", "(restarts)", "env TOKEN changed", "+  b:", "devyard reload"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff output lacks %q:\n%s", want, out)
		}
	}
	// The details name variables only; devyard.yml itself is diffed as written.
	if details := out[:strings.Index(out, "+++")]; strings.Contains(details, "secret") {
		t.Errorf("the change list must not show env values:\n%s", details)
	}
	if st := sb.CLIIn(p.Dir, "status").MustSucceed(t); !strings.Contains(st.Stderr, "the config changed (2 changes)") {
		t.Errorf("status shows no drift note:\n%s", st.Output())
	}

	sb.CLIIn(p.Dir, "reload").MustSucceed(t)
	w.WaitFor(t, "applied", func(s harness.State) bool {
		return driftState(s, "driftp") == "" && s.Running("driftp", "b") && s.ServicePid("driftp", "a") != pid && s.Running("driftp", "a")
	})
	if out := sb.CLIIn(p.Dir, "diff").MustSucceed(t).Stdout; !strings.Contains(out, "match what is running") {
		t.Errorf("diff after reload:\n%s", out)
	}
}

// A config that does not load is reported; what runs is untouched; fixing
// the file clears the report.
func TestDrift_InvalidConfig(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("driftbad", driftV1, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("add", p.Dir).MustSucceed(t)
	p.WaitRunning(w)
	pid := w.State().ServicePid("driftbad", "a")

	p.WriteConfig("services:\n  a:\n    run: x\n    restart: sometimes\n")
	w.WaitFor(t, "drift invalid", func(s harness.State) bool { return driftState(s, "driftbad") == "invalid" })
	st := w.State()
	if e := st.Project("driftbad").GetDrift().GetError(); !strings.Contains(e, "restart") {
		t.Errorf("error = %q", e)
	}
	if st.Project("driftbad").GetError() != "" || st.ServicePid("driftbad", "a") != pid || !st.Running("driftbad", "a") {
		t.Fatalf("an invalid config disturbed the project: %v", st.Project("driftbad"))
	}
	if out := sb.CLIIn(p.Dir, "diff").MustSucceed(t).Stdout; !strings.Contains(out, "do not load") || !strings.Contains(out, "restart") {
		t.Errorf("diff output:\n%s", out)
	}
	p.WriteConfig(driftV1)
	w.WaitFor(t, "back in sync", func(s harness.State) bool { return driftState(s, "driftbad") == "" })
}

// reload: auto applies valid changes by itself; a per-project override wins
// over the default; the policy changes live.
func TestDrift_ReloadPolicies(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	auto := sb.WriteProject("driftauto", driftV1, nil)
	off := sb.WriteProject("driftoff", driftV1, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"reload: auto\nprojects:\n  - "+auto.Dir+"\n  - path: "+off.Dir+"\n    reload: off\n")
	w.WaitFor(t, "projects with policies", func(s harness.State) bool {
		return s.Project("driftauto").GetReloadPolicy() == "auto" && s.Project("driftoff").GetReloadPolicy() == "off"
	})
	sb.CLI("project", "start", "driftauto").MustSucceed(t)
	sb.CLI("project", "start", "driftoff").MustSucceed(t)
	auto.WaitRunning(w)
	off.WaitRunning(w)
	offPid := w.State().ServicePid("driftoff", "a")

	auto.WriteConfig(driftV2)
	off.WriteConfig(driftV2)
	w.WaitFor(t, "auto applied by itself", func(s harness.State) bool {
		return s.Running("driftauto", "b") && driftState(s, "driftauto") == ""
	})
	// off ignores the change: no drift, nothing applied.
	harness.Consistently(t, "off ignores the change", 3_000_000_000, func(c *harness.C) {
		s := w.State()
		if driftState(s, "driftoff") != "" || s.Service("driftoff", "b") != nil || s.ServicePid("driftoff", "a") != offPid {
			c.Errorf("policy off acted on the change: %v", s.Project("driftoff"))
		}
	})
	// Dropping the override makes it follow the default and apply.
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"reload: auto\nprojects:\n  - "+auto.Dir+"\n  - "+off.Dir+"\n")
	w.WaitFor(t, "off project applied once it follows auto", func(s harness.State) bool {
		return s.Project("driftoff").GetReloadPolicy() == "auto" && s.Running("driftoff", "b")
	})
}
