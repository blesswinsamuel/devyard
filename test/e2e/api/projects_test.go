package api_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const twoMarked = `services:
  a:
    run: {{fixture "ticker"}} -interval 200ms
    env:
      DY_MARK: {{.Name}}-a
  b:
    run: {{fixture "ticker"}} -interval 200ms
    env:
      DY_MARK: {{.Name}}-b
`

func TestAPI_GetDaemonReportsBoundAddrs(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	info := d.Info()
	for name, addr := range map[string]string{"web_addr": info.GetWebAddr(), "proxy_addr": info.GetProxyAddr()} {
		if addr == "" || addr[len(addr)-2:] == ":0" {
			t.Errorf("%s = %q, want a bound host:port", name, addr)
		}
		if addr == "127.0.0.1:9090" || addr == "127.0.0.1:8080" {
			t.Errorf("%s = %q: port 0 was not honored (default port used)", name, addr)
		}
	}
	if info.GetPid() != int32(sb.PidfilePid()) {
		t.Errorf("pidfile %d != daemon pid %d", sb.PidfilePid(), info.GetPid())
	}
	resp, err := d.Client().GetGlobalConfig(d.Ctx(), connect.NewRequest(&v1.GetGlobalConfigRequest{}))
	harness.NoError(t, err, "GetGlobalConfig")
	if resp.Msg.GetPath() != sb.GlobalConfigPath() {
		t.Errorf("global config path %q, want %q", resp.Msg.GetPath(), sb.GlobalConfigPath())
	}
}

func TestAPI_AddProjectCapturesEnv(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("addenv", `services:
  s:
    run: sh -c 'echo who=$DY_WHO; exec {{fixture "ticker"}} -interval 1s'
`, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	env := append(sb.Env(), "DY_WHO=api-caller")
	resp, err := d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: p.ConfigPath, Env: env, Start: true}))
	harness.NoError(t, err, "AddProject")
	if resp.Msg.GetProject().GetId() != "addenv" {
		t.Errorf("project id %q", resp.Msg.GetProject().GetId())
	}
	w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("addenv", "s") })
	f := d.FollowLogs(&v1.LogsRequest{Project: "addenv", Sources: []*v1.LogSource{harness.Svc("s")}})
	f.WaitForText(t, "who=api-caller")
}

// O2: project status is derived from its services.
func TestLedger_O2_ProjectStatusTracksServices(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "o2", twoMarked)
	st := w.WaitFor(t, "project running", func(s harness.State) bool {
		p := s.Project("o2")
		return p.GetStatus() == "running" && p.GetServicesRunning() == 2 && p.GetServicesTotal() == 2
	})
	_ = st
	_, err := d.Client().StopService(d.Ctx(), connect.NewRequest(&v1.StopServiceRequest{Project: "o2", Service: "a"}))
	harness.NoError(t, err, "StopService")
	w.WaitFor(t, "one running", func(s harness.State) bool {
		p := s.Project("o2")
		return p.GetServicesRunning() == 1 && p.GetStatus() != "stopped"
	})
	_, err = d.Client().StopProject(d.Ctx(), connect.NewRequest(&v1.StopProjectRequest{Project: "o2"}))
	harness.NoError(t, err, "StopProject")
	w.WaitFor(t, "project stopped", func(s harness.State) bool {
		p := s.Project("o2")
		return p.GetStatus() == "stopped" && p.GetServicesRunning() == 0 && p.GetDesired() == "stopped"
	})
}

// O3: concurrent StartProject calls never duplicate processes.
func TestLedger_O3_ConcurrentStartProjectNoDuplicates(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("o3", twoMarked, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	_, err := d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: p.ConfigPath, Env: sb.Env()}))
	harness.NoError(t, err, "AddProject")
	sa, sbm := sampleGroups(sb, "o3-a"), sampleGroups(sb, "o3-b")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Client().StartProject(d.Ctx(), connect.NewRequest(&v1.StartProjectRequest{Project: "o3"}))
			if err != nil {
				t.Errorf("StartProject: %v", err)
			}
		}()
	}
	waitGroup(t, &wg, 60*time.Second, "10 concurrent StartProject")
	p.WaitRunning(w)
	harness.Consistently(t, "one process per service", time.Second, func(c *harness.C) {
		for _, m := range []string{"o3-a", "o3-b"} {
			if g := sb.LiveGroups("DY_MARK", m); len(g) != 1 {
				c.Errorf("%s: groups %v", m, g)
			}
		}
	})
	sa.finish(t)
	sbm.finish(t)
}

// O9: a stop issued while the project is still starting wins.
func TestLedger_O9_StopDuringStartIsNotLost(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("o9", `services:
  slow:
    run: {{fixture "ticker"}} -interval 200ms
    build: sleep 2
  after:
    run: {{fixture "ticker"}} -interval 200ms
    depends_on: [slow]
`, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	_, err := d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: p.ConfigPath, Env: sb.Env()}))
	harness.NoError(t, err, "AddProject")
	go func() {
		_, _ = d.Client().StartProject(context.Background(), connect.NewRequest(&v1.StartProjectRequest{Project: "o9", Build: true}))
	}()
	w.WaitFor(t, "project starting", func(s harness.State) bool {
		return s.ServiceIs("o9", "slow", "building", "starting", "waiting")
	})
	_, err = d.Client().StopProject(d.Ctx(), connect.NewRequest(&v1.StopProjectRequest{Project: "o9"}))
	harness.NoError(t, err, "StopProject")
	w.WaitFor(t, "stopped", func(s harness.State) bool {
		return s.AllServicesIn("o9", "stopped") && s.Project("o9").GetDesired() == "stopped"
	})
	harness.Consistently(t, "nothing starts after the stop", 3*time.Second, func(c *harness.C) {
		s := w.State()
		for _, sv := range s.ServicesOf("o9") {
			if sv.GetPid() != 0 || sv.GetStatus() == "running" {
				c.Errorf("%s started after stop: %s", sv.GetName(), harness.FormatService(sv))
			}
		}
	})
}

// O11: two config paths declaring the same name conflict.
func TestLedger_O11_SameNameDifferentPathAlreadyExists(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	cfg := `name: same
services:
  a:
    run: {{fixture "ticker"}} -interval 1s
`
	one := sb.WriteProject("same-one", cfg, nil)
	two := sb.WriteProject("same-two", cfg, nil)
	d := sb.Daemon()
	_, err := d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: one.ConfigPath, Env: sb.Env()}))
	harness.NoError(t, err, "AddProject one")
	_, err = d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: two.ConfigPath, Env: sb.Env()}))
	harness.RequireCode(t, err, connect.CodeAlreadyExists)
	// Re-adding the same path is idempotent.
	_, err = d.Client().AddProject(d.Ctx(), connect.NewRequest(&v1.AddProjectRequest{ConfigPath: one.ConfigPath, Env: sb.Env()}))
	harness.NoError(t, err, "AddProject one again")
	if got := d.State().Project("same").GetConfigPath(); got != one.ConfigPath {
		t.Errorf("config path %q, want %q", got, one.ConfigPath)
	}
}

// O5: RemoveProject validates ids and deletes nothing on bad input.
func TestLedger_O5_RemoveProjectValidatesIDs(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "o5", twoMarked)
	sb := d.Sandbox()
	p := w.WaitFor(t, "running", func(s harness.State) bool { return s.AllRunning("o5") })
	sentinel := filepath.Join(sb.AppStateDir(), "sentinel")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"..", "../..", ".", "a/b", "../o5", "O5", "/", "o5/../.."} {
		_, err := d.Client().RemoveProject(d.Ctx(), connect.NewRequest(&v1.RemoveProjectRequest{Project: id}))
		harness.RequireCode(t, err, connect.CodeInvalidArgument)
	}
	_, err := d.Client().RemoveProject(d.Ctx(), connect.NewRequest(&v1.RemoveProjectRequest{Project: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel deleted: %v", err)
	}
	now := d.State()
	if diff := harness.DiffServices(p, now); diff != "" {
		t.Errorf("services disturbed:\n%s", diff)
	}
}

// O15: errors carry proper Connect codes.
func TestLedger_O15_TypedErrors(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("o15", `services:
  a:
    run: {{fixture "ticker"}} -interval 1s
tasks:
  t: {{fixture "exiter"}}
`, nil)
	p.Start()
	d := sb.Daemon()
	c := d.Client()
	ctx := d.Ctx()
	_, err := c.StartProject(ctx, connect.NewRequest(&v1.StartProjectRequest{Project: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.StopProject(ctx, connect.NewRequest(&v1.StopProjectRequest{Project: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.StartService(ctx, connect.NewRequest(&v1.StartServiceRequest{Project: "o15", Service: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.RestartService(ctx, connect.NewRequest(&v1.RestartServiceRequest{Project: "o15", Service: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.RunTask(ctx, connect.NewRequest(&v1.RunTaskRequest{Project: "o15", Task: "ghost"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.StartProject(ctx, connect.NewRequest(&v1.StartProjectRequest{Project: "o15", Services: []string{"ghost"}}))
	harness.RequireCode(t, err, connect.CodeNotFound)
	_, err = c.KillService(ctx, connect.NewRequest(&v1.KillServiceRequest{Project: "o15", Service: "a", Signal: "SIGNOPE"}))
	harness.RequireCode(t, err, connect.CodeInvalidArgument)
	_, err = c.StartProject(ctx, connect.NewRequest(&v1.StartProjectRequest{Project: "../x"}))
	harness.RequireCode(t, err, connect.CodeInvalidArgument)
	_, err = c.AddProject(ctx, connect.NewRequest(&v1.AddProjectRequest{ConfigPath: filepath.Join(sb.Home, "nope", "devyard.yml")}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound && code != connect.CodeInvalidArgument && code != connect.CodeFailedPrecondition {
		t.Errorf("AddProject(missing file): code %v, want NotFound, InvalidArgument or FailedPrecondition (%v)", code, err)
	}
	_, err = c.GitLog(ctx, connect.NewRequest(&v1.GitLogRequest{Project: "o15"}))
	if code := connect.CodeOf(err); err != nil && code != connect.CodeFailedPrecondition {
		t.Errorf("GitLog on a non-repo: code %v, want FailedPrecondition (%v)", code, err)
	}
}

// O1: a briefly missing config never deletes the project or its logs; the
// project reports an error and recovers on reload.
func TestLedger_O1_MissingConfigKeepsProject(t *testing.T) {
	t.Parallel()
	sb, p, d, w := setup(t, "o1", `services:
  a:
    run: {{fixture "ticker"}} -prefix O1 -interval 100ms
`)
	w.WaitFor(t, "running", func(s harness.State) bool { return s.Running("o1", "a") })
	f := d.FollowLogs(&v1.LogsRequest{Project: "o1", Sources: []*v1.LogSource{harness.Svc("a")}})
	f.WaitForText(t, "O1 3")

	if err := os.Rename(p.ConfigPath, p.ConfigPath+".bak"); err != nil {
		t.Fatal(err)
	}
	harness.Eventually(t, "project reports the missing config", func(c *harness.C) {
		st := d.State()
		pr := st.Project("o1")
		if pr == nil {
			c.Fatalf("project vanished: %s", st)
		}
		if pr.GetError() == "" || pr.GetStatus() != "error" {
			c.Errorf("project: status=%q error=%q", pr.GetStatus(), pr.GetError())
		}
	})
	// Reads never delete anything.
	for i := 0; i < 5; i++ {
		if d.State().Project("o1") == nil {
			t.Fatalf("GetState #%d lost the project", i)
		}
		sb.CLI("project", "list").MustSucceed(t)
	}
	if _, err := os.Stat(filepath.Join(sb.ProjectStateDir("o1"), "project.json")); err != nil {
		t.Errorf("project.json gone: %v", err)
	}
	if lines := d.Logs(&v1.LogsRequest{Project: "o1", Sources: []*v1.LogSource{harness.Svc("a")}}); !harness.ContainsText(lines, "O1 1") {
		t.Errorf("logs lost while the config is missing")
	}

	if err := os.Rename(p.ConfigPath+".bak", p.ConfigPath); err != nil {
		t.Fatal(err)
	}
	_, err := d.Client().ReloadProject(d.Ctx(), connect.NewRequest(&v1.ReloadProjectRequest{Project: "o1"}))
	harness.NoError(t, err, "ReloadProject")
	w.WaitFor(t, "recovered", func(s harness.State) bool {
		pr := s.Project("o1")
		return pr.GetError() == "" && pr.GetStatus() == "running" && s.Running("o1", "a")
	})
}

func TestAPI_RestartProject(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "rp", twoMarked)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.AllRunning("rp") })
	a, b := st.ServicePid("rp", "a"), st.ServicePid("rp", "b")
	_, err := d.Client().RestartProject(d.Ctx(), connect.NewRequest(&v1.RestartProjectRequest{Project: "rp"}))
	harness.NoError(t, err, "RestartProject")
	w.WaitFor(t, "both restarted", func(s harness.State) bool {
		return s.AllRunning("rp") && s.ServicePid("rp", "a") != a && s.ServicePid("rp", "b") != b
	})
}

func TestAPI_RemoveProjectStopsAndForgets(t *testing.T) {
	t.Parallel()
	_, _, d, w := setup(t, "rm", twoMarked)
	st := w.WaitFor(t, "running", func(s harness.State) bool { return s.AllRunning("rm") })
	pids := []int{st.ServicePid("rm", "a"), st.ServicePid("rm", "b")}
	_, err := d.Client().RemoveProject(d.Ctx(), connect.NewRequest(&v1.RemoveProjectRequest{Project: "rm"}))
	harness.NoError(t, err, "RemoveProject")
	w.WaitFor(t, "removed", func(s harness.State) bool { return s.Project("rm") == nil && len(s.ServicesOf("rm")) == 0 })
	for _, pid := range pids {
		if harness.GroupAlive(pid) {
			t.Errorf("group %d survived RemoveProject", pid)
		}
	}
	_, err = d.Client().StartProject(d.Ctx(), connect.NewRequest(&v1.StartProjectRequest{Project: "rm"}))
	harness.RequireCode(t, err, connect.CodeNotFound)
}
