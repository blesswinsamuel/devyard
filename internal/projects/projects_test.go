package projects_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/projects"
)

// nopObserver ignores every published change.
type nopObserver struct{}

func (nopObserver) ProjectChanged(engine.ProjectState)                     {}
func (nopObserver) ProjectRemoved(string)                                  {}
func (nopObserver) ServiceChanged(*engine.ProcessDef, engine.ServiceState) {}
func (nopObserver) ServiceRemoved(string, string)                          {}
func (nopObserver) TaskChanged(*engine.ProcessDef, engine.TaskState)       {}
func (nopObserver) TaskRemoved(string, string)                             {}

type fixture struct {
	t    *testing.T
	home string
	dirs paths.Dirs
	mgr  *engine.Manager
	svc  *projects.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs := paths.Dirs{State: filepath.Join(home, "state"), Runtime: filepath.Join(home, "run"), Config: filepath.Join(home, "config")}
	if err := dirs.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirs.Config, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := engine.NewManager(dirs, engine.RunnerLauncher{}, nopObserver{}, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background(), false) })
	return &fixture{t: t, home: home, dirs: dirs, mgr: mgr, svc: projects.New(mgr, dirs, nil)}
}

// project creates a project directory with the given config ("" for none).
func (f *fixture) project(name, config string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "dev", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "devyard.yml"), []byte(config), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	return dir
}

func (f *fixture) writeConfig(content string) {
	f.t.Helper()
	if err := os.WriteFile(f.dirs.GlobalConfig(), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) config() *globalconfig.Config {
	f.t.Helper()
	cfg, err := globalconfig.Load(f.dirs.GlobalConfig(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return cfg
}

func (f *fixture) ids() []string {
	var out []string
	for _, p := range f.mgr.List() {
		out = append(out, p.ID())
	}
	return out
}

const svcConfig = "services:\n  a:\n    run: sleep 30\n"

func TestStartAppliesTheList(t *testing.T) {
	f := newFixture(t)
	a, b := f.project("a", svcConfig), f.project("b", svcConfig)
	for _, d := range []string{a, b} {
		if _, err := f.mgr.Add(context.Background(), engine.AddOptions{ConfigPath: filepath.Join(d, "devyard.yml")}); err != nil {
			t.Fatal(err)
		}
	}
	// Without a `projects` key the config does not say which projects
	// exist: what is registered stays, and nothing is written.
	if err := f.svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("projects = %v", got)
	}
	if _, err := os.Stat(f.dirs.GlobalConfig()); err == nil {
		t.Fatal("Start wrote the global config")
	}
	// With the key, the list is authoritative: unlisted projects go away.
	f.writeConfig("projects:\n  - ~/dev/a\n")
	if err := f.svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("projects after Start = %v", got)
	}
}

func TestReconcileAddsRemovesAndOrders(t *testing.T) {
	f := newFixture(t)
	f.project("a", svcConfig)
	f.project("b", "")
	f.project("c", svcConfig)
	f.writeConfig("projects: [~/dev/c, ~/dev/a, ~/dev/b]\n")
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("order = %v", got)
	}
	b, _ := f.mgr.Get("b")
	if b.View().HasConfig {
		t.Fatal("b has no devyard.yml but reports a config")
	}
	f.writeConfig("projects: [~/dev/b, ~/dev/c]\n")
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("after hand edit = %v", got)
	}
	// An absent key leaves everything alone.
	f.writeConfig("web: {}\n")
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("absent key changed projects: %v", got)
	}
	// Removed entries show up as recent suggestions.
	recent, _ := f.svc.Suggestions("")
	if len(recent) != 1 || recent[0].Path != filepath.Join(f.home, "dev", "a") || !recent[0].HasConfig {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestReconcileShowsEntriesWithAMissingDirectoryAsErrors(t *testing.T) {
	f := newFixture(t)
	f.project("good", svcConfig)
	f.writeConfig("projects: [~/dev/missing, ~/dev/good]\n")
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"missing", "good"}) {
		t.Fatalf("projects = %v", got)
	}
	p, _ := f.mgr.Get("missing")
	if !strings.Contains(p.View().LoadErr, "does not exist") {
		t.Fatalf("missing project error = %q", p.View().LoadErr)
	}
	// The user can remove it from the dashboard or the list.
	if err := f.svc.Remove(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
	if got := f.config().Projects; !reflect.DeepEqual(got, []string{"~/dev/good"}) {
		t.Fatalf("list = %v", got)
	}
}

func TestReconcileKeepsProjectWithBrokenConfigVisible(t *testing.T) {
	f := newFixture(t)
	f.project("broken", "services: [oops\n")
	f.writeConfig("projects: [~/dev/broken]\n")
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	p, err := f.mgr.Get("broken")
	if err != nil || p.View().LoadErr == "" {
		t.Fatalf("broken project: %v %v", p, err)
	}
}

func TestAddEditsTheListAndRegisters(t *testing.T) {
	f := newFixture(t)
	f.writeConfig("# my projects\nprojects:\n  - ~/dev/a # first\n")
	f.project("a", svcConfig)
	dir := f.project("b", svcConfig)
	if err := f.svc.Reconcile(context.Background(), f.config()); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Add(context.Background(), projects.AddOptions{Path: dir})
	if err != nil || p.ID() != "b" {
		t.Fatalf("add: %v %v", p, err)
	}
	// Adding again is idempotent.
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: dir + "/devyard.yml"}); err != nil {
		t.Fatal(err)
	}
	cfg := f.config()
	if !reflect.DeepEqual(cfg.Projects, []string{"~/dev/a", "~/dev/b"}) {
		t.Fatalf("list = %v", cfg.Projects)
	}
	if data, _ := os.ReadFile(f.dirs.GlobalConfig()); !strings.Contains(string(data), "# my projects") || !strings.Contains(string(data), "# first") {
		t.Fatalf("comments lost:\n%s", data)
	}
}

func TestAddRollsBackFailedProjects(t *testing.T) {
	f := newFixture(t)
	bad := f.project("bad", "services: [oops\n")
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: bad}); !errors.Is(err, engine.ErrConfig) {
		t.Fatalf("invalid config: %v", err)
	}
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: filepath.Join(f.home, "nowhere")}); !errors.Is(err, engine.ErrConfig) {
		t.Fatalf("missing dir: %v", err)
	}
	// Two checkouts declaring the same name: the second must not stay listed.
	first := f.project("one", "name: same\n"+svcConfig)
	second := f.project("two", "name: same\n"+svcConfig)
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: first}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: second}); !errors.Is(err, engine.ErrAlreadyExists) {
		t.Fatalf("name clash: %v", err)
	}
	if got := f.config().Projects; !reflect.DeepEqual(got, []string{"~/dev/one"}) {
		t.Fatalf("list after failures = %v", got)
	}
}

func TestRemoveAndMove(t *testing.T) {
	f := newFixture(t)
	var dirs []string
	for _, n := range []string{"a", "b", "c"} {
		dirs = append(dirs, f.project(n, svcConfig))
		if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: dirs[len(dirs)-1]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.Move(context.Background(), "c", 0); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("order after move = %v", got)
	}
	if got := f.config().Projects; !reflect.DeepEqual(got, []string{"~/dev/c", "~/dev/a", "~/dev/b"}) {
		t.Fatalf("list after move = %v", got)
	}
	if err := f.svc.Remove(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if got := f.ids(); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("after remove = %v", got)
	}
	if got := f.config().Projects; !reflect.DeepEqual(got, []string{"~/dev/c", "~/dev/b"}) {
		t.Fatalf("list after remove = %v", got)
	}
	if err := f.svc.Remove(context.Background(), "a"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
	if err := f.svc.Move(context.Background(), "nope", 0); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("moving an unknown project: %v", err)
	}
	// Re-adding a removed project works and drops it from "recent".
	recent, _ := f.svc.Suggestions("")
	if len(recent) != 1 || recent[0].Path != dirs[0] {
		t.Fatalf("recent = %+v", recent)
	}
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: recent[0].Path}); err != nil {
		t.Fatal(err)
	}
	if recent, _ = f.svc.Suggestions(""); len(recent) != 0 {
		t.Fatalf("recent after re-add = %+v", recent)
	}
}

func TestSuggestionsCompleteDirectories(t *testing.T) {
	f := newFixture(t)
	f.project("alpha", svcConfig)
	f.project("alps", "")
	if err := os.MkdirAll(filepath.Join(f.home, "dev", "alps", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.project("beta", "")
	if err := os.MkdirAll(filepath.Join(f.home, "dev", ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "dev", "alfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add(context.Background(), projects.AddOptions{Path: filepath.Join(f.home, "dev", "alpha")}); err != nil {
		t.Fatal(err)
	}
	_, got := f.svc.Suggestions("~/dev/al")
	if len(got) != 2 {
		t.Fatalf("completions = %+v", got)
	}
	alpha, alps := got[0], got[1]
	if alpha.Path != filepath.Join(f.home, "dev", "alpha") || !alpha.HasConfig || alpha.IsGit || !alpha.Listed {
		t.Errorf("alpha = %+v", alpha)
	}
	if alps.Path != filepath.Join(f.home, "dev", "alps") || alps.HasConfig || !alps.IsGit || alps.Listed {
		t.Errorf("alps = %+v", alps)
	}
	if _, got = f.svc.Suggestions("~/dev/"); len(got) != 3 {
		t.Errorf("hidden directories must be skipped: %+v", got)
	}
	if _, got = f.svc.Suggestions("~/dev/."); len(got) != 1 {
		t.Errorf("a dot prefix shows hidden directories: %+v", got)
	}
	if _, got = f.svc.Suggestions("~/nope/x"); len(got) != 0 {
		t.Errorf("unreadable parent: %+v", got)
	}
}
