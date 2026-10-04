package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

// projectIDs returns the ids of `project list` in the order printed.
func projectIDs(t *testing.T, sb *harness.Sandbox) []string {
	t.Helper()
	out := sb.CLI("project", "list").MustSucceed(t).Stdout
	var ids []string
	for i, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if i == 0 || strings.HasPrefix(line, "(no projects)") {
			continue
		}
		ids = append(ids, strings.Fields(line)[0])
	}
	return ids
}

func globalConfig(t *testing.T, sb *harness.Sandbox) string {
	t.Helper()
	data, err := os.ReadFile(sb.GlobalConfigPath())
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	return string(data)
}

// The project list lives in the global config: add appends, remove drops.
func TestProjectList_AddAndRemoveEditTheGlobalConfig(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	one := sb.WriteProject("listone", oneTicker, nil)
	two := sb.WriteProject("listtwo", oneTicker, nil)
	sb.Daemon()
	sb.CLI("add", one.Dir, "--no-start").MustSucceed(t)
	sb.CLI("add", two.Dir, "--no-start").MustSucceed(t)
	sb.CLI("add", two.Dir, "--no-start").MustSucceed(t) // idempotent
	cfg := globalConfig(t, sb)
	if strings.Count(cfg, "listone") != 1 || strings.Count(cfg, "listtwo") != 1 || !strings.Contains(cfg, "projects:") {
		t.Fatalf("global config after add:\n%s", cfg)
	}
	if strings.Index(cfg, "listone") > strings.Index(cfg, "listtwo") {
		t.Fatalf("projects are not in the order added:\n%s", cfg)
	}
	sb.CLI("project", "remove", "listone").MustSucceed(t)
	if cfg = globalConfig(t, sb); strings.Contains(cfg, "listone") || !strings.Contains(cfg, "listtwo") {
		t.Fatalf("global config after remove:\n%s", cfg)
	}
	if got := projectIDs(t, sb); len(got) != 1 || got[0] != "listtwo" {
		t.Fatalf("projects = %v", got)
	}
}

// A removed project can be added back: nothing about it is lost but state.
func TestProjectList_ReAddAfterRemove(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("readd", oneTicker, nil)
	sb.Daemon()
	sb.CLI("add", p.Dir).MustSucceed(t)
	sb.CLI("project", "remove", "readd").MustSucceed(t)
	if got := projectIDs(t, sb); len(got) != 0 {
		t.Fatalf("projects after remove = %v", got)
	}
	sb.CLI("add", p.Dir).MustSucceed(t)
	w := sb.Daemon().Watch(context.Background())
	p.WaitRunning(w)
}

// A directory without a devyard.yml is a project (git view, terminals).
func TestProjectList_DirectoryWithoutConfigIsAProject(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	dir := filepath.Join(sb.Root, "projects", "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("add", dir).MustSucceed(t)
	w.WaitFor(t, "notes registered", func(s harness.State) bool { return s.Project("notes") != nil })
	st := w.State().Project("notes")
	if st.GetHasConfig() || st.GetError() != "" || st.GetServicesTotal() != 0 {
		t.Fatalf("notes = %v", st)
	}
	if out := sb.CLI("project", "list").MustSucceed(t).Stdout; !strings.Contains(out, "no devyard.yml") {
		t.Fatalf("project list:\n%s", out)
	}
	// Adding a config later brings services in without re-adding.
	if err := os.WriteFile(filepath.Join(dir, "devyard.yml"), []byte("services:\n  a:\n    run: sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.WaitFor(t, "config picked up", func(s harness.State) bool {
		p := s.Project("notes")
		return p.GetHasConfig() && p.GetServicesTotal() == 1
	})
}

// The list is ordered, `project move` reorders it, and the order is what
// clients see and what the config file says.
func TestProjectList_MoveReorders(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	var dirs []string
	for _, n := range []string{"mva", "mvb", "mvc"} {
		dirs = append(dirs, sb.WriteProject(n, oneTicker, nil).Dir)
	}
	d := sb.Daemon()
	w := d.Watch(context.Background())
	for _, dir := range dirs {
		sb.CLI("add", dir, "--no-start").MustSucceed(t)
	}
	order := func(want ...string) {
		t.Helper()
		w.WaitFor(t, "positions "+strings.Join(want, ","), func(s harness.State) bool {
			for i, id := range want {
				if p := s.Project(id); p == nil || int(p.GetPosition()) != i {
					return false
				}
			}
			return true
		})
		if got := projectIDs(t, sb); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("project list order = %v, want %v", got, want)
		}
		cfg := globalConfig(t, sb)
		last := -1
		for _, id := range want {
			i := strings.Index(cfg, id)
			if i < last {
				t.Fatalf("global config order differs from %v:\n%s", want, cfg)
			}
			last = i
		}
	}
	order("mva", "mvb", "mvc")
	sb.CLI("project", "move", "mvc", "first").MustSucceed(t)
	order("mvc", "mva", "mvb")
	sb.CLI("project", "move", "mvc", "2").MustSucceed(t)
	order("mva", "mvc", "mvb")
	sb.CLI("project", "move", "mva", "last").MustSucceed(t)
	order("mvc", "mvb", "mva")
	sb.CLI("project", "move", "ghost", "1").MustFail(t)
	sb.CLI("project", "move", "mva", "zero").MustFail(t)
	// The order survives a daemon restart.
	d.Restart(false)
	order("mvc", "mvb", "mva")
}

// Groups start and stop several projects at once.
func TestProjectList_GroupStartAndStop(t *testing.T) {
	t.Parallel()
	sb := harness.New(t, harness.WithGlobalConfig(harness.DefaultGlobalConfig+"groups:\n  duo: [grpa, grpb]\n  solo: [grpa]\n"))
	a := sb.WriteProject("grpa", oneTicker, nil)
	b := sb.WriteProject("grpb", oneTicker, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("add", a.Dir, "--no-start").MustSucceed(t)
	sb.CLI("add", b.Dir, "--no-start").MustSucceed(t)
	if !strings.Contains(globalConfig(t, sb), "groups:") {
		t.Fatal("adding projects dropped the groups from the global config")
	}
	sb.CLI("start", "@duo").MustSucceed(t)
	a.WaitRunning(w)
	b.WaitRunning(w)
	sb.CLI("stop", "@solo").MustSucceed(t)
	w.WaitFor(t, "grpa stopped", func(s harness.State) bool { return s.ServiceIs("grpa", "a", "stopped") })
	if !w.State().Running("grpb", "a") {
		t.Fatal("stopping @solo stopped grpb too")
	}
	sb.CLI("restart", "@duo").MustSucceed(t)
	sb.CLI("stop", "@duo").MustSucceed(t)
	w.WaitFor(t, "all stopped", func(s harness.State) bool {
		return s.ServiceIs("grpa", "a", "stopped") && s.ServiceIs("grpb", "a", "stopped")
	})
	if out := sb.CLI("start", "@nope").MustFail(t).Output(); !strings.Contains(out, "unknown group") || !strings.Contains(out, "duo") {
		t.Fatalf("unknown group message: %s", out)
	}
}
