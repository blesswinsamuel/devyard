//go:build unix

package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/logstore"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == runner.Flag {
		if err := runner.Main(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// recorder is an Observer that keeps the latest state of every entity.
type recorder struct {
	mu       sync.Mutex
	services map[string]ServiceState
	tasks    map[string]TaskState
	projects map[string]ProjectState
	history  map[string][]ServiceState
	changed  chan struct{}
}

func newRecorder() *recorder {
	return &recorder{
		services: map[string]ServiceState{},
		tasks:    map[string]TaskState{},
		projects: map[string]ProjectState{},
		history:  map[string][]ServiceState{},
		changed:  make(chan struct{}),
	}
}

func (r *recorder) notify() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *recorder) ProjectChanged(st ProjectState) {
	r.mu.Lock()
	r.projects[st.ID] = st
	r.notify()
	r.mu.Unlock()
}
func (r *recorder) ProjectRemoved(id string) {
	r.mu.Lock()
	delete(r.projects, id)
	r.notify()
	r.mu.Unlock()
}
func (r *recorder) ServiceChanged(def *ProcessDef, st ServiceState) {
	r.mu.Lock()
	k := def.Project + "/" + def.Name
	r.services[k] = st
	r.history[k] = append(r.history[k], st)
	r.notify()
	r.mu.Unlock()
}
func (r *recorder) ServiceRemoved(project, name string) {
	r.mu.Lock()
	delete(r.services, project+"/"+name)
	r.notify()
	r.mu.Unlock()
}
func (r *recorder) TaskChanged(def *ProcessDef, st TaskState) {
	r.mu.Lock()
	r.tasks[def.Project+"/"+def.Name] = st
	r.notify()
	r.mu.Unlock()
}
func (r *recorder) TaskRemoved(project, name string) {
	r.mu.Lock()
	delete(r.tasks, project+"/"+name)
	r.notify()
	r.mu.Unlock()
}

func (r *recorder) service(key string) ServiceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.services[key]
}

func (r *recorder) waitFor(t *testing.T, desc string, timeout time.Duration, pred func(r *recorder) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		r.mu.Lock()
		ok := pred(r)
		ch := r.changed
		r.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-ch:
		case <-deadline:
			r.mu.Lock()
			defer r.mu.Unlock()
			t.Fatalf("timed out waiting for %s\nservices: %+v\ntasks: %+v\nprojects: %+v", desc, r.services, r.tasks, r.projects)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *recorder) waitService(t *testing.T, key, status string) ServiceState {
	t.Helper()
	r.waitFor(t, key+" "+status, 15*time.Second, func(r *recorder) bool { return r.services[key].Status == status })
	return r.service(key)
}

type env struct {
	t    *testing.T
	dirs paths.Dirs
	obs  *recorder
	m    *Manager
	root string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	rt, err := os.MkdirTemp("/tmp", "eng")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	dirs := paths.Dirs{State: filepath.Join(root, "state"), Runtime: rt, Config: filepath.Join(root, "config")}
	if err := dirs.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dirs: dirs, obs: newRecorder(), root: root}
	e.m = e.newManager()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = e.m.Shutdown(ctx, true)
	})
	return e
}

func (e *env) newManager() *Manager {
	exe, err := os.Executable()
	if err != nil {
		e.t.Fatal(err)
	}
	return NewManager(e.dirs, RunnerLauncher{Options: runner.LaunchOptions{Exe: exe}}, e.obs, nil)
}

func (e *env) project(name, yaml string) string {
	e.t.Helper()
	dir := filepath.Join(e.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	path := filepath.Join(dir, "devyard.yml")
	if err := os.WriteFile(path, []byte("version: \"1\"\nname: "+name+"\n"+yaml), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *env) add(path string, start bool) *Project {
	e.t.Helper()
	p, err := e.m.Add(context.Background(), AddOptions{ConfigPath: path, Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.root}, Start: start})
	if err != nil {
		e.t.Fatalf("add: %v", err)
	}
	return p
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func groupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestStartStopProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p1", `services:
  a:
    command: sleep 30
  b:
    command: sleep 30
    depends_on: [a]
`)
	p := e.add(path, true)
	a := e.obs.waitService(t, "p1/a", StatusRunning)
	b := e.obs.waitService(t, "p1/b", StatusRunning)
	e.obs.waitFor(t, "project running", 10*time.Second, func(r *recorder) bool { return r.projects["p1"].Status == ProjectRunning })
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if st := e.obs.service("p1/a"); st.Status != StatusStopped {
		t.Fatalf("a after stop: %+v", st)
	}
	if groupAlive(a.PID) || groupAlive(b.PID) {
		t.Fatal("processes survived project stop")
	}
	e.obs.waitFor(t, "project stopped", 5*time.Second, func(r *recorder) bool { return r.projects["p1"].Status == ProjectStopped })
}

func TestServiceHealthyDependency(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p2", `services:
  db:
    command: sleep 0.5 && touch ready && sleep 30
    healthcheck:
      test: ["CMD-SHELL", "test -f ready"]
      interval: 100ms
      retries: 100
  api:
    command: sleep 30
    depends_on:
      db: {condition: service_healthy}
`)
	e.add(path, true)
	e.obs.waitFor(t, "api waiting", 10*time.Second, func(r *recorder) bool { return r.services["p2/api"].Status == StatusWaiting })
	e.obs.waitService(t, "p2/api", StatusRunning)
	if db := e.obs.service("p2/db"); db.Health != HealthHealthy {
		t.Fatalf("api started before db was healthy: %+v", db)
	}
}

func TestLedgerS2ConcurrentRestartsSingleProcess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p3", `services:
  a:
    command: sleep 30
    stop_grace_period: 1s
`)
	p := e.add(path, true)
	e.obs.waitService(t, "p3/a", StatusRunning)
	svc, err := p.Service("a")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Restart(ctx(t), false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st := e.obs.waitService(t, "p3/a", StatusRunning)
	// Every pid ever reported except the current one must be gone.
	e.obs.mu.Lock()
	hist := append([]ServiceState(nil), e.obs.history["p3/a"]...)
	e.obs.mu.Unlock()
	for _, h := range hist {
		if h.PID != 0 && h.PID != st.PID && groupAlive(h.PID) {
			t.Fatalf("stale process %d still alive (current %d)", h.PID, st.PID)
		}
	}
	if !groupAlive(st.PID) {
		t.Fatal("current process not alive")
	}
}

func TestLedgerS3StopDuringBackoff(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p4", `services:
  crash:
    command: exit 1
    restart: always
`)
	p := e.add(path, true)
	e.obs.waitFor(t, "backoff", 15*time.Second, func(r *recorder) bool {
		st := r.services["p4/crash"]
		return st.Status == StatusBackoff && st.Restarts >= 2
	})
	svc, _ := p.Service("crash")
	start := time.Now()
	if err := svc.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop during backoff took %v", d)
	}
	if st := e.obs.service("p4/crash"); st.Status != StatusStopped {
		t.Fatalf("status %+v", st)
	}
	time.Sleep(1500 * time.Millisecond)
	if st := e.obs.service("p4/crash"); st.Status != StatusStopped {
		t.Fatalf("restarted after stop: %+v", st)
	}
}

func TestLedgerS7KillDuringStop(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p5", `services:
  stubborn:
    command: trap "" TERM; echo ready; while true; do sleep 0.1; done
    stop_grace_period: 30s
`)
	p := e.add(path, true)
	e.obs.waitService(t, "p5/stubborn", StatusRunning)
	waitLog(t, e, "p5", "stubborn", "ready")
	svc, _ := p.Service("stubborn")
	stopped := make(chan error, 1)
	go func() { stopped <- svc.Stop(context.Background()) }()
	e.obs.waitService(t, "p5/stubborn", StatusStopping)
	if err := svc.Kill(ctx(t), "SIGKILL"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not finish after kill")
	}
	if st := e.obs.service("p5/stubborn"); st.Status != StatusStopped {
		t.Fatalf("status %+v", st)
	}
}

func TestStopEscalatesAfterGrace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p6", `services:
  stubborn:
    command: trap "" TERM; echo ready; while true; do sleep 0.1; done
    stop_grace_period: 300ms
`)
	p := e.add(path, true)
	st := e.obs.waitService(t, "p6/stubborn", StatusRunning)
	waitLog(t, e, "p6", "stubborn", "ready")
	start := time.Now()
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stop took %v", d)
	}
	if groupAlive(st.PID) {
		t.Fatal("process survived")
	}
}

func TestLedgerS5AdoptionAcrossManagers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p7", `services:
  ticker:
    command: while true; do echo tick; sleep 0.1; done
`)
	e.add(path, true)
	before := e.obs.waitService(t, "p7/ticker", StatusRunning)
	// The "daemon" goes away without stopping services.
	if err := e.m.Shutdown(ctx(t), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !groupAlive(before.PID) {
		t.Fatal("service died with the manager")
	}
	e.obs = newRecorder()
	e.m = e.newManager()
	if err := e.m.Load(); err != nil {
		t.Fatal(err)
	}
	after := e.obs.waitService(t, "p7/ticker", StatusRunning)
	if after.PID != before.PID || after.Run != before.Run {
		t.Fatalf("not adopted: before %+v after %+v", before, after)
	}
	// Output keeps flowing into the same run's log.
	pd, _ := e.dirs.Project("p7")
	dir := pd.Proc("service", "ticker")
	n1, _, _ := logstore.Tail(dir, after.Run, 0, 0)
	time.Sleep(400 * time.Millisecond)
	n2, _, _ := logstore.Tail(dir, after.Run, 0, 0)
	if len(n2) <= len(n1) {
		t.Fatalf("log stopped growing after adoption (%d -> %d lines)", len(n1), len(n2))
	}
	p, _ := e.m.Get("p7")
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if groupAlive(after.PID) {
		t.Fatal("adopted service survived stop")
	}
}

func TestExitWhileManagerAwayIsReported(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p8", `services:
  short:
    command: sleep 0.5; exit 3
`)
	e.add(path, true)
	e.obs.waitService(t, "p8/short", StatusRunning)
	if err := e.m.Shutdown(ctx(t), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	e.obs = newRecorder()
	e.m = e.newManager()
	if err := e.m.Load(); err != nil {
		t.Fatal(err)
	}
	st := e.obs.waitService(t, "p8/short", StatusExited)
	if st.ExitCode != 3 {
		t.Fatalf("exit code %d", st.ExitCode)
	}
	time.Sleep(500 * time.Millisecond)
	if st := e.obs.service("p8/short"); st.Status != StatusExited {
		t.Fatalf("restart: no service was restarted: %+v", st)
	}
}

func TestStoppedProjectStaysStoppedAcrossManagers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p9", `services:
  a:
    command: sleep 30
`)
	p := e.add(path, true)
	e.obs.waitService(t, "p9/a", StatusRunning)
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	_ = e.m.Shutdown(ctx(t), false)
	e.obs = newRecorder()
	e.m = e.newManager()
	_ = e.m.Load()
	time.Sleep(500 * time.Millisecond)
	if st := e.obs.service("p9/a"); st.Status != StatusStopped {
		t.Fatalf("stopped project autostarted: %+v", st)
	}
}

func TestDaemonStopThenAutostart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p10", `services:
  a:
    command: sleep 30
`)
	e.add(path, true)
	e.obs.waitService(t, "p10/a", StatusRunning)
	// `daemon stop`: stop services but keep the desired state.
	if err := e.m.Shutdown(ctx(t), true); err != nil {
		t.Fatal(err)
	}
	e.obs = newRecorder()
	e.m = e.newManager()
	_ = e.m.Load()
	e.obs.waitService(t, "p10/a", StatusRunning)
}

func TestReloadReconciles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p11", `services:
  keep:
    command: sleep 30
  change:
    command: sleep 30
  drop:
    command: sleep 30
`)
	p := e.add(path, true)
	keep := e.obs.waitService(t, "p11/keep", StatusRunning)
	change := e.obs.waitService(t, "p11/change", StatusRunning)
	drop := e.obs.waitService(t, "p11/drop", StatusRunning)
	if err := os.WriteFile(path, []byte(`version: "1"
name: p11
services:
  keep:
    command: sleep 30
  change:
    command: sleep 31
  added:
    command: sleep 30
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Reload(ctx(t), nil, false); err != nil {
		t.Fatal(err)
	}
	e.obs.waitService(t, "p11/added", StatusRunning)
	e.obs.waitFor(t, "change restarted", 10*time.Second, func(r *recorder) bool {
		st := r.services["p11/change"]
		return st.Status == StatusRunning && st.PID != change.PID
	})
	if st := e.obs.service("p11/keep"); st.PID != keep.PID {
		t.Fatalf("unchanged service restarted: %+v vs %+v", st, keep)
	}
	e.obs.waitFor(t, "drop removed", 10*time.Second, func(r *recorder) bool { _, ok := r.services["p11/drop"]; return !ok })
	if groupAlive(drop.PID) {
		t.Fatal("removed service still running")
	}
}

func TestLedgerO1MissingConfigKeepsProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p12", `services:
  a:
    command: sleep 30
`)
	p := e.add(path, true)
	a := e.obs.waitService(t, "p12/a", StatusRunning)
	if err := os.Rename(path, path+".bak"); err != nil {
		t.Fatal(err)
	}
	if err := p.Reload(ctx(t), nil, false); !errors.Is(err, ErrConfig) {
		t.Fatalf("reload err = %v", err)
	}
	e.obs.waitFor(t, "project error", 5*time.Second, func(r *recorder) bool { return r.projects["p12"].Status == ProjectError })
	if !groupAlive(a.PID) {
		t.Fatal("service killed because config went missing")
	}
	if _, err := e.m.Get("p12"); err != nil {
		t.Fatalf("project deregistered: %v", err)
	}
	// A daemon restart while the config is missing keeps the project and
	// still adopts (and can stop) the running service.
	_ = e.m.Shutdown(ctx(t), false)
	e.obs = newRecorder()
	e.m = e.newManager()
	_ = e.m.Load()
	e.obs.waitService(t, "p12/a", StatusRunning)
	if err := os.Rename(path+".bak", path); err != nil {
		t.Fatal(err)
	}
	p, _ = e.m.Get("p12")
	if err := p.Reload(ctx(t), nil, false); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "project running", 5*time.Second, func(r *recorder) bool { return r.projects["p12"].Status == ProjectRunning })
	if st := e.obs.service("p12/a"); st.PID != a.PID {
		t.Fatalf("service restarted on recovery: %+v vs %+v", st, a)
	}
}

func TestLedgerO11SameNameDifferentPath(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("dup", "services:\n  a:\n    command: sleep 30\n")
	e.add(path, false)
	other := filepath.Join(e.root, "other", "devyard.yml")
	_ = os.MkdirAll(filepath.Dir(other), 0o755)
	_ = os.WriteFile(other, []byte("version: \"1\"\nname: dup\nservices:\n  b:\n    command: sleep 30\n"), 0o644)
	_, err := e.m.Add(ctx(t), AddOptions{ConfigPath: other})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("err = %v", err)
	}
}

func TestLedgerO5InvalidIDs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, id := range []string{"..", "../x", "", "a/b"} {
		if _, err := e.m.Get(id); !errors.Is(err, ErrInvalid) {
			t.Errorf("Get(%q) err = %v", id, err)
		}
		if err := e.m.Remove(ctx(t), id); !errors.Is(err, ErrInvalid) {
			t.Errorf("Remove(%q) err = %v", id, err)
		}
	}
}

func TestTaskRunAndConcurrency(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p13", `services:
  a:
    command: sleep 30
tasks:
  slow:
    command: echo start; sleep 30
  quick:
    command: echo "args:"
    tty: false
`)
	p := e.add(path, false)
	run, err := p.RunTask(ctx(t), "slow", nil)
	if err != nil || run != 1 {
		t.Fatalf("run %d err %v", run, err)
	}
	e.obs.waitFor(t, "slow running", 10*time.Second, func(r *recorder) bool { return r.tasks["p13/slow"].Status == StatusRunning })
	if _, err := p.RunTask(ctx(t), "slow", nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second run err = %v", err)
	}
	if _, err := p.RunTask(ctx(t), "quick", []string{"x y", "it's"}); err != nil {
		t.Fatal(err)
	}
	e.obs.waitFor(t, "quick exited", 10*time.Second, func(r *recorder) bool { return r.tasks["p13/quick"].Status == StatusExited })
	pd, _ := e.dirs.Project("p13")
	lines, _, _ := logstore.Tail(pd.Proc("task", "quick"), 1, 0, 0)
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	if !strings.Contains(strings.Join(out, "\n"), "args: x y it's") {
		t.Fatalf("task output: %v", out)
	}
	// Stopping the project stops running tasks.
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if st := e.obs.tasks["p13/slow"]; st.Status == StatusRunning || st.Status == StatusStopping {
		t.Fatalf("task still running after project stop: %+v", st)
	}
}

func TestStartServiceOnStoppedProjectStartsDependencies(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p14", `services:
  db:
    command: sleep 30
  api:
    command: sleep 30
    depends_on: [db]
  other:
    command: sleep 30
`)
	p := e.add(path, false)
	if err := p.StartService(ctx(t), "api", false); err != nil {
		t.Fatal(err)
	}
	e.obs.waitService(t, "p14/api", StatusRunning)
	e.obs.waitService(t, "p14/db", StatusRunning)
	time.Sleep(300 * time.Millisecond)
	if st := e.obs.service("p14/other"); st.Status != StatusStopped {
		t.Fatalf("unselected service started: %+v", st)
	}
	if d := p.View().Reg.Desired; d != DesiredPartial {
		t.Fatalf("desired %s", d)
	}
}

func TestConcurrentStartsNoDuplicates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p15", "services:\n  a:\n    command: sleep 30\n")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.m.Add(context.Background(), AddOptions{ConfigPath: path, Env: []string{"PATH=" + os.Getenv("PATH")}, Start: true}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st := e.obs.waitService(t, "p15/a", StatusRunning)
	e.obs.mu.Lock()
	hist := append([]ServiceState(nil), e.obs.history["p15/a"]...)
	e.obs.mu.Unlock()
	pids := map[int]bool{}
	for _, h := range hist {
		if h.PID != 0 {
			pids[h.PID] = true
		}
	}
	if len(pids) != 1 || !pids[st.PID] {
		t.Fatalf("expected a single process, saw pids %v", pids)
	}
}

func TestRandomizedOperationsInvariants(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	t.Parallel()
	e := newEnv(t)
	path := e.project("chaos", `services:
  a:
    command: sleep 30
    stop_grace_period: 500ms
  b:
    command: sleep 30
    depends_on: [a]
    restart: always
    stop_grace_period: 500ms
`)
	p := e.add(path, true)
	rng := rand.New(rand.NewPCG(1, 2))
	names := []string{"a", "b"}
	for i := range 40 {
		name := names[rng.IntN(2)]
		svc, _ := p.Service(name)
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var err error
		switch rng.IntN(6) {
		case 0:
			err = svc.Start(c, false)
		case 1:
			err = svc.Stop(c)
		case 2:
			err = svc.Restart(c, false)
		case 3:
			_ = svc.Kill(c, "SIGKILL")
		case 4:
			err = p.Start(c, nil, false)
		case 5:
			go func() { _ = svc.Restart(context.Background(), false) }()
		}
		cancel()
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		time.Sleep(time.Duration(rng.IntN(80)) * time.Millisecond)
	}
	if err := p.Stop(ctx(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	e.obs.mu.Lock()
	defer e.obs.mu.Unlock()
	for _, key := range []string{"chaos/a", "chaos/b"} {
		if st := e.obs.services[key]; st.Status != StatusStopped {
			t.Errorf("%s not stopped: %+v", key, st)
		}
		for _, h := range e.obs.history[key] {
			if h.PID != 0 && groupAlive(h.PID) {
				t.Errorf("%s: process %d leaked", key, h.PID)
			}
		}
	}
}

func TestConfigChangedWhileAwayRestartsOnAdoption(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p16", "services:\n  a:\n    command: sleep 30\n  b:\n    command: sleep 30\n")
	e.add(path, true)
	a := e.obs.waitService(t, "p16/a", StatusRunning)
	b := e.obs.waitService(t, "p16/b", StatusRunning)
	_ = e.m.Shutdown(ctx(t), false)
	_ = os.WriteFile(path, []byte("version: \"1\"\nname: p16\nservices:\n  a:\n    command: sleep 31\n  b:\n    command: sleep 30\n"), 0o644)
	e.obs = newRecorder()
	e.m = e.newManager()
	_ = e.m.Load()
	e.obs.waitFor(t, "a restarted with new config", 10*time.Second, func(r *recorder) bool {
		st := r.services["p16/a"]
		return st.Status == StatusRunning && st.PID != a.PID
	})
	if st := e.obs.waitService(t, "p16/b", StatusRunning); st.PID != b.PID {
		t.Fatalf("unchanged service restarted")
	}
}

func TestDependentWaitsThroughTransientUnhealthy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := e.project("p17", `services:
  db:
    command: sleep 1 && touch ready && sleep 30
    healthcheck:
      test: ["CMD-SHELL", "test -f ready"]
      interval: 100ms
      retries: 1
  api:
    command: sleep 30
    depends_on:
      db: {condition: service_healthy}
`)
	e.add(path, true)
	e.obs.waitFor(t, "db unhealthy while booting", 10*time.Second, func(r *recorder) bool { return r.services["p17/db"].Health == HealthUnhealthy })
	e.obs.waitService(t, "p17/api", StatusRunning)
}

func waitLog(t *testing.T, e *env, project, service, text string) {
	t.Helper()
	pd, _ := e.dirs.Project(project)
	dir := pd.Proc("service", service)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		lines, _, _ := logstore.Tail(dir, logstore.LatestRun(dir), 0, 0)
		for _, l := range lines {
			if l.Text == text {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s/%s never logged %q", project, service, text)
}
