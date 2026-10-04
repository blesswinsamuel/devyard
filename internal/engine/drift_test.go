package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

func TestUnifiedDiff(t *testing.T) {
	t.Parallel()
	a := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	b := "a\nb\nc\nD\ne\nf\ng\nh\ni\nj\nk\nl\nm\n"
	want := `--- p
+++ p
@@ -1,7 +1,7 @@
 a
 b
 c
-d
+D
 e
 f
 g
@@ -10,3 +10,4 @@
 j
 k
 l
+m
`
	if got := unifiedDiff("p", a, b); got != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", got, want)
	}
	if got := unifiedDiff("p", "x\n", "x\n"); got != "--- p\n+++ p\n" {
		t.Fatalf("identical files: %q", got)
	}
	if got := unifiedDiff("p", "", "new\n"); !strings.Contains(got, "@@ -1,0 +1,1 @@\n+new\n") {
		t.Fatalf("new file: %q", got)
	}
	if got := unifiedDiff("p", "old\n", ""); !strings.Contains(got, "-old\n") {
		t.Fatalf("emptied file: %q", got)
	}
}

func TestTextDiffSkipsEqualFilesAndTruncates(t *testing.T) {
	t.Parallel()
	got := textDiff(map[string]string{"/a.yml": "x\n", "/b.yml": "same\n"}, map[string]string{"/a.yml": "y\n", "/b.yml": "same\n"})
	if !strings.Contains(got, "/a.yml") || strings.Contains(got, "/b.yml") {
		t.Fatalf("diff:\n%s", got)
	}
	big := strings.Repeat(strings.Repeat("l", 100)+"\n", 3000)
	other := strings.Repeat(strings.Repeat("L", 100)+"\n", 3000)
	if d := textDiff(map[string]string{"/f": big}, map[string]string{"/f": other}); len(d) > maxDiffBytes+100 || !strings.Contains(d, "truncated") {
		t.Fatalf("a huge diff must be truncated (len %d)", len(d))
	}
	if d := textDiff(map[string]string{"/f": strings.Repeat("x\n", maxDiffLines+1)}, map[string]string{"/f": "y\n"}); !strings.Contains(d, "too large") {
		t.Fatalf("too large: %q", d)
	}
}

func TestEnvChangesNeverIncludeValues(t *testing.T) {
	t.Parallel()
	got := envChanges([]string{"A=secret1", "B=same", "GONE=x"}, []string{"A=secret2", "B=same", "NEW=hunter2"})
	want := []string{"env A changed", "env +NEW", "env −GONE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envChanges = %q, want %q", got, want)
	}
	for _, d := range got {
		if strings.Contains(d, "secret") || strings.Contains(d, "hunter2") {
			t.Fatalf("value leaked: %q", d)
		}
	}
}

func TestDiffDef(t *testing.T) {
	t.Parallel()
	base := func() *ProcessDef {
		return &ProcessDef{Cmd: config.Command{Script: "run a"}, Dir: "/d", Restart: config.RestartOnFailure, Autostart: true,
			Ports: []config.ResolvedPort{{Name: "http", Port: 3000}}}
	}
	if d := diffDef(base(), base()); len(d) != 0 {
		t.Fatalf("identical: %v", d)
	}
	b := base()
	b.Cmd = config.Command{Script: "run b"}
	b.Dir = "/e"
	b.Restart = config.RestartAlways
	b.Deps = []string{"db"}
	b.Ports = []config.ResolvedPort{{Name: "http", Port: 3001}}
	b.Autostart = false
	d := strings.Join(diffDef(base(), b), "|")
	for _, want := range []string{"run run a → run b", "dir /d → /e", "restart on-failure → always", "depends_on [] → [db]", "ports http:3000 → http:3001", "autostart true → false"} {
		if !strings.Contains(d, want) {
			t.Errorf("missing %q in %s", want, d)
		}
	}
	// An auto port's number is allocated, not written: it is no change.
	x, y := base(), base()
	x.Ports = []config.ResolvedPort{{Name: "http", Port: 4000, Auto: true}}
	y.Ports = []config.ResolvedPort{{Name: "http", Port: 4999, Auto: true}}
	if d := diffDef(x, y); len(d) != 0 {
		t.Errorf("auto port numbers differ: %v", d)
	}
}

func (e *env) writeConfig(path, yaml string) {
	e.t.Helper()
	if err := os.WriteFile(path, []byte("name: "+filepath.Base(filepath.Dir(path))+"\n"+yaml), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (r *recorder) drift(id string) Drift {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.projects[id].Drift
}

func (r *recorder) project(id string) ProjectState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.projects[id]
}

func (r *recorder) hasService(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.services[key]
	return ok
}

const driftWait = 15 * time.Second

func TestDriftPendingThenApplied(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("dp", "services:\n  a:\n    run: sleep 30\n    env: {TOKEN: one}\n  b:\n    run: sleep 31\n")
	p := e.add(path, true)
	a := e.obs.waitService(t, "dp/a", StatusRunning)
	b := e.obs.waitService(t, "dp/b", StatusRunning)

	e.writeConfig(path, "services:\n  a:\n    run: sleep 30\n    env: {TOKEN: two}\n  b:\n    run: sleep 31\n  c:\n    run: sleep 32\n")
	e.obs.waitFor(t, "drift pending", driftWait, func(r *recorder) bool { return r.projects["dp"].Drift.State == DriftPending })
	d := e.obs.drift("dp")
	got := map[string]Change{}
	for _, c := range d.Changes {
		got[c.Name] = c
	}
	if c := got["c"]; c.Op != "added" || c.Kind != "service" {
		t.Errorf("c = %+v", c)
	}
	if c := got["a"]; c.Op != "changed" || !c.Restart || strings.Join(c.Details, ",") != "env TOKEN changed" {
		t.Errorf("a = %+v", c)
	}
	if _, ok := got["b"]; ok {
		t.Errorf("unchanged service b listed: %+v", got["b"])
	}
	if !strings.Contains(d.Diff, "+  c:") || !strings.Contains(d.Diff, "-    env: {TOKEN: one}") {
		t.Errorf("diff:\n%s", d.Diff)
	}
	// Nothing changed yet: the running config is the old one.
	if st := e.obs.service("dp/a"); st.PID != a.PID || st.Status != StatusRunning {
		t.Errorf("a restarted before apply: %+v", st)
	}
	if e.obs.hasService("dp/c") {
		t.Error("c exists before apply")
	}
	if st := e.obs.project("dp"); st.ReloadPolicy != ReloadPrompt {
		t.Errorf("policy = %q", st.ReloadPolicy)
	}

	if err := p.Reload(ctx(t), nil, false); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "drift cleared", driftWait, func(r *recorder) bool { return r.projects["dp"].Drift.State == DriftNone })
	e.obs.waitFor(t, "a restarted, c running", driftWait, func(r *recorder) bool {
		return r.services["dp/a"].PID != a.PID && r.services["dp/a"].Status == StatusRunning && r.services["dp/c"].Status == StatusRunning
	})
	if st := e.obs.service("dp/b"); st.PID != b.PID {
		t.Errorf("unchanged b was restarted: %+v", st)
	}
}

func TestDriftInvalidKeepsRunningThenRecovers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	good := "services:\n  a:\n    run: sleep 30\n"
	path := e.project("di", good)
	e.add(path, true)
	a := e.obs.waitService(t, "di/a", StatusRunning)
	e.writeConfig(path, "services:\n  a:\n    run: sleep 30\n    restart: sometimes\n")
	e.obs.waitFor(t, "drift invalid", driftWait, func(r *recorder) bool { return r.projects["di"].Drift.State == DriftInvalid })
	if d := e.obs.drift("di"); !strings.Contains(d.Error, "restart") {
		t.Errorf("error = %q", d.Error)
	}
	if st := e.obs.service("di/a"); st.PID != a.PID || st.Status != StatusRunning {
		t.Errorf("a disturbed by an invalid config: %+v", st)
	}
	if st := e.obs.project("di"); st.Status == ProjectError || st.Error != "" {
		t.Errorf("the project must not be in error: %+v", st)
	}
	// Reverting to what runs clears the drift without applying anything.
	e.writeConfig(path, good)
	e.obs.waitFor(t, "drift cleared", driftWait, func(r *recorder) bool { return r.projects["di"].Drift.State == DriftNone })
	if st := e.obs.service("di/a"); st.PID != a.PID {
		t.Errorf("a restarted: %+v", st)
	}
}

func TestDriftIgnoresCommentsAndFormatting(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("dc", "services:\n  a:\n    run: sleep 30\n")
	e.add(path, true)
	e.obs.waitService(t, "dc/a", StatusRunning)
	e.writeConfig(path, "# a comment\nservices:\n  a:\n    run:   sleep 30   # trailing\n")
	// Give the checks time to notice and decide there is nothing to apply.
	time.Sleep(3500 * time.Millisecond)
	if d := e.obs.drift("dc"); d.State != DriftNone {
		t.Fatalf("a comment-only edit drifted: %+v", d)
	}
	// And the new text is the baseline: a real change diffs against it.
	e.writeConfig(path, "# a comment\nservices:\n  a:\n    run:   sleep 33   # trailing\n")
	e.obs.waitFor(t, "drift pending", driftWait, func(r *recorder) bool { return r.projects["dc"].Drift.State == DriftPending })
	if d := e.obs.drift("dc"); !strings.Contains(d.Diff, "# a comment") || !strings.Contains(d.Diff, "sleep 33") {
		t.Errorf("diff:\n%s", d.Diff)
	}
}

func TestDriftEnvFileChangeMasksValues(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("de", "services:\n  a:\n    run: sleep 30\n")
	dotenv := filepath.Join(filepath.Dir(path), ".env")
	if err := os.WriteFile(dotenv, []byte("API_KEY=hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.add(path, true)
	e.obs.waitService(t, "de/a", StatusRunning)
	if err := os.WriteFile(dotenv, []byte("API_KEY=hunter3\nNEW=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "drift pending", driftWait, func(r *recorder) bool { return r.projects["de"].Drift.State == DriftPending })
	d := e.obs.drift("de")
	if len(d.Changes) != 1 || strings.Join(d.Changes[0].Details, ",") != "env API_KEY changed,env +NEW" {
		t.Errorf("changes = %+v", d.Changes)
	}
	if strings.Contains(d.Diff, "hunter") {
		t.Errorf("env values must stay out of the diff:\n%s", d.Diff)
	}
	// Deleting the env file counts too: the project's file list and the
	// variables it supplied both change.
	if err := os.Remove(dotenv); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "removals", driftWait, func(r *recorder) bool {
		var project, service bool
		for _, c := range r.projects["de"].Drift.Changes {
			details := strings.Join(c.Details, ",")
			project = project || (c.Kind == "project" && strings.Contains(details, "env files .env → none"))
			service = service || (c.Kind == "service" && strings.Contains(details, "env −API_KEY"))
		}
		return project && service
	})
}

func TestDriftAutoAppliesAndOffIgnores(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	autoPath := e.project("da", "services:\n  a:\n    run: sleep 30\n  b:\n    run: sleep 31\n")
	offPath := e.project("do", "services:\n  a:\n    run: sleep 30\n")
	e.m.SetReloadPolicy(func(configPath string) string {
		if strings.HasSuffix(filepath.Dir(configPath), "/do") {
			return ReloadOff
		}
		return ReloadAuto
	})
	e.add(autoPath, true)
	e.add(offPath, true)
	a := e.obs.waitService(t, "da/a", StatusRunning)
	b := e.obs.waitService(t, "da/b", StatusRunning)
	e.obs.waitService(t, "do/a", StatusRunning)

	e.writeConfig(autoPath, "services:\n  a:\n    run: sleep 30\n  b:\n    run: sleep 34\n  c:\n    run: sleep 35\n")
	e.writeConfig(offPath, "services:\n  a:\n    run: sleep 36\n")
	e.obs.waitFor(t, "auto applied", driftWait, func(r *recorder) bool {
		return r.services["da/b"].PID != b.PID && r.services["da/b"].Status == StatusRunning && r.services["da/c"].Status == StatusRunning
	})
	if st := e.obs.service("da/a"); st.PID != a.PID {
		t.Errorf("unchanged a restarted under auto: %+v", st)
	}
	if d := e.obs.drift("da"); d.State != DriftNone {
		t.Errorf("drift after auto apply: %+v", d)
	}
	if st := e.obs.project("da"); st.ReloadPolicy != ReloadAuto {
		t.Errorf("policy = %q", st.ReloadPolicy)
	}
	time.Sleep(3 * time.Second)
	if d := e.obs.drift("do"); d.State != DriftNone {
		t.Errorf("policy off still reports drift: %+v", d)
	}
	// Switching the policy to auto applies the pending change.
	before := e.obs.service("do/a").PID
	e.m.SetReloadPolicy(func(string) string { return ReloadAuto })
	e.obs.waitFor(t, "do applied after the policy change", driftWait, func(r *recorder) bool {
		return r.projects["do"].ReloadPolicy == ReloadAuto && r.services["do/a"].Status == StatusRunning && r.services["do/a"].PID != before
	})
}

func TestBrokenConfigReloadsItselfWhenFixed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	dir := filepath.Join(e.root, "fixme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "devyard.yml")
	if err := os.WriteFile(path, []byte("services: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Add(ctx(t), AddOptions{ConfigPath: path, Tolerant: true}); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "error state", driftWait, func(r *recorder) bool { return r.projects["fixme"].Error != "" })
	if err := os.WriteFile(path, []byte("services:\n  a:\n    run: sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "recovered by itself", driftWait, func(r *recorder) bool {
		return r.projects["fixme"].Error == "" && r.projects["fixme"].ServicesTotal == 1
	})
}
