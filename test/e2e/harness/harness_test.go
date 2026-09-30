package harness

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// These self-tests exercise the harness itself and run without a working
// devyard binary.

func TestMain(m *testing.M) { Main(m) }

var allowedEnv = map[string]bool{
	"PATH": true, "TERM": true, "LANG": true, "HOME": true, "TMPDIR": true,
	"XDG_RUNTIME_DIR": true, "XDG_STATE_HOME": true, "XDG_CONFIG_HOME": true,
	"XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "CAROOT": true, "SHELL": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true, "GIT_TERMINAL_PROMPT": true,
	SandboxIDVar: true,
}

func TestSelf_EnvAllowlist(t *testing.T) {
	t.Setenv("DEVYARD_E2E_LEAK_CANARY", "leaked")
	t.Setenv("XDG_RUNTIME_DIR", "/should/not/leak")
	sb := New(t)
	for _, kv := range sb.Env() {
		k := kv[:strings.IndexByte(kv, '=')]
		if !allowedEnv[k] {
			t.Errorf("env key %q not in allowlist", k)
		}
	}
	r := sb.Exec(sb.Home, "/usr/bin/env")
	r.MustSucceed(t)
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			env[line[:i]] = line[i+1:]
		}
	}
	if _, ok := env["DEVYARD_E2E_LEAK_CANARY"]; ok {
		t.Errorf("host env leaked into child")
	}
	for k, want := range map[string]string{
		"HOME":              sb.Home,
		"XDG_RUNTIME_DIR":   sb.Runtime,
		"XDG_STATE_HOME":    sb.StateHome,
		"SHELL":             "/bin/sh",
		SandboxIDVar:        sb.ID,
		"GIT_CONFIG_GLOBAL": sb.GitConfig,
	} {
		if env[k] != want {
			t.Errorf("child %s=%q, want %q", k, env[k], want)
		}
	}
	if !strings.HasPrefix(env["PATH"], filepath.Dir(FixturePath("ticker"))+":") {
		t.Errorf("PATH does not start with the harness bin dir: %s", env["PATH"])
	}
	cfg, err := os.ReadFile(sb.GlobalConfigPath())
	if err != nil || !strings.Contains(string(cfg), "port: 0") {
		t.Errorf("global config not written with port 0: %v %q", err, cfg)
	}
}

func TestSelf_SocketGuard(t *testing.T) {
	sb := New(t)
	if n := len(sb.SocketPath()); n >= 100 {
		t.Fatalf("socket path too long (%d): %s", n, sb.SocketPath())
	}
	if !strings.HasPrefix(sb.SocketPath(), sb.Runtime) {
		t.Fatalf("socket %s not under runtime %s", sb.SocketPath(), sb.Runtime)
	}
	if err := sb.guard(); err != nil {
		t.Fatalf("guard rejected a fresh sandbox: %v", err)
	}
	// Pretend the real environment points at the sandbox: the guard must
	// refuse.
	t.Setenv("XDG_RUNTIME_DIR", sb.Runtime)
	if err := sb.guard(); err == nil {
		t.Fatalf("guard accepted a sandbox whose socket equals the real one")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", sb.StateHome)
	if err := sb.guard(); err == nil {
		t.Fatalf("guard accepted a sandbox whose state dir equals the real one")
	}
}

func TestSelf_RealSocketDiffers(t *testing.T) {
	sb := New(t)
	real, err := resolveDirs(os.Getenv)
	if err != nil {
		t.Skip("no real HOME")
	}
	if real.socket() == sb.SocketPath() {
		t.Fatalf("sandbox socket equals real socket %s", real.socket())
	}
}

func TestSelf_TagParsing(t *testing.T) {
	sb := New(t)
	owner, ok := TagOwner(sb.ID)
	if !ok || owner != os.Getpid() {
		t.Fatalf("TagOwner(%q) = %d, %v", sb.ID, owner, ok)
	}
	if StaleTag(sb.ID) {
		t.Fatalf("own tag reported stale")
	}
	if !StaleTag("dye2e-999999999-abcd-1") {
		t.Fatalf("dead owner not stale")
	}
	if StaleTag("something-else") || StaleTag("") {
		t.Fatalf("untagged value reported stale")
	}
	if _, err := ReapPrefix("dy"); err == nil {
		t.Fatalf("ReapPrefix accepted a broad prefix")
	}
}

func TestSelf_SweeperKillsOnlyTagged(t *testing.T) {
	sb := New(t)
	tagged := sb.Start(RunOpts{}, FixturePath("ticker"), "-interval", "1s")
	// An untagged process: same binary, host env without our tag.
	untagged := exec.Command(FixturePath("ticker"), "-interval", "1s")
	untagged.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sb.Home}
	untagged.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := untagged.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = untagged.Wait(); close(done) }()
	t.Cleanup(func() { _ = untagged.Process.Kill(); <-done })

	Eventually(t, "tagged ticker visible with its tag", func(c *C) {
		procs := sb.TaggedProcesses()
		found := false
		for _, p := range procs {
			if p.Pid == tagged.Pid() {
				found = true
			}
			if p.Pid == untagged.Process.Pid {
				c.Fatalf("untagged process reported as tagged: %s", p)
			}
		}
		if !found {
			c.Errorf("tagged pid %d not found among %v", tagged.Pid(), procs)
		}
	})
	victims, err := Reap(func(p ProcInfo) bool { return p.SandboxID() == sb.ID })
	if err != nil {
		t.Fatal(err)
	}
	killed := map[int]bool{}
	for _, v := range victims {
		killed[v.Pid] = true
	}
	if !killed[tagged.Pid()] {
		t.Errorf("tagged process %d not reaped; victims=%v", tagged.Pid(), victims)
	}
	if killed[untagged.Process.Pid] || killed[os.Getpid()] {
		t.Fatalf("reaped an untagged/protected process: %v", victims)
	}
	select {
	case <-done:
		t.Fatalf("untagged process died")
	case <-time.After(200 * time.Millisecond):
	}
	tagged.Wait(t, 5*time.Second)
}

func TestSelf_ExpandTagged(t *testing.T) {
	self := os.Getpid()
	all := []ProcInfo{
		{Pid: self, PPid: os.Getppid(), Pgid: 500},
		{Pid: 600, PPid: self, Pgid: 500, env: []string{SandboxIDVar + "=x"}}, // tagged CLI in our group
		{Pid: 700, PPid: 1, Pgid: 700, env: []string{SandboxIDVar + "=x"}},    // tagged runner
		{Pid: 701, PPid: 700, Pgid: 701},                                      // hidden-env sh under runner
		{Pid: 702, PPid: 701, Pgid: 701},                                      // its child
		{Pid: 703, PPid: 1, Pgid: 701},                                        // orphan in the same group
		{Pid: 800, PPid: 1, Pgid: 800, env: []string{SandboxIDVar + "=y"}},    // other sandbox
		{Pid: 801, PPid: self, Pgid: 500},                                     // untagged sibling in our group
	}
	got := map[int]bool{}
	for _, p := range expandTagged(all, func(p ProcInfo) bool { return p.SandboxID() == "x" }) {
		got[p.Pid] = true
	}
	for _, want := range []int{600, 700, 701, 702, 703} {
		if !got[want] {
			t.Errorf("pid %d not selected", want)
		}
	}
	for _, not := range []int{self, 800, 801} {
		if got[not] {
			t.Errorf("pid %d wrongly selected", not)
		}
	}
}

func TestSelf_Eventually(t *testing.T) {
	n := 0
	Eventually(t, "third attempt", func(c *C) {
		n++
		if n < 3 {
			c.Fatalf("attempt %d", n)
		}
	})
	if n != 3 {
		t.Fatalf("attempts = %d", n)
	}
	Consistently(t, "stays true", 200*time.Millisecond, func(c *C) {})
}

func TestSelf_StateApply(t *testing.T) {
	s := StateFromSnapshot(1, &v1.Snapshot{
		Projects: []*v1.Project{{Id: "p"}},
		Services: []*v1.Service{{Project: "p", Name: "a", Status: "running", Pid: 10}, {Project: "p", Name: "b"}},
		Tasks:    []*v1.Task{{Project: "p", Name: "t"}},
	})
	s.apply(2, &v1.Change{Change: &v1.Change_Service{Service: &v1.Service{Project: "p", Name: "b", Status: "running", Pid: 11}}})
	if !s.AllRunning("p") {
		t.Fatalf("not all running: %s", s)
	}
	s.apply(3, &v1.Change{Change: &v1.Change_Removed{Removed: &v1.EntityRef{Kind: "service", Project: "p", Name: "a"}}})
	if s.Service("p", "a") != nil {
		t.Fatalf("service not removed")
	}
	s.apply(4, &v1.Change{Change: &v1.Change_Removed{Removed: &v1.EntityRef{Kind: "project", Project: "p"}}})
	if len(s.Services) != 0 || len(s.Tasks) != 0 || len(s.Projects) != 0 || s.Revision != 4 {
		t.Fatalf("project removal incomplete: %s", s)
	}
}

func TestSelf_ProjectTemplating(t *testing.T) {
	sb := New(t)
	p := sb.WriteProject("tmpl", `services:
  a:
    command: {{fixture "ticker"}} -prefix {{.Name}}
    port: {{port "a"}}
`, map[string]string{".env": "P={{port \"a\"}}\n"})
	cfg, _ := os.ReadFile(p.ConfigPath)
	env, _ := os.ReadFile(p.Path(".env"))
	if !strings.Contains(string(cfg), FixturePath("ticker")+" -prefix tmpl") {
		t.Errorf("fixture not rendered: %s", cfg)
	}
	port := strconv.Itoa(p.Port("a"))
	if !strings.Contains(string(cfg), "port: "+port) || strings.TrimSpace(string(env)) != "P="+port {
		t.Errorf("port not stable: %s / %s", cfg, env)
	}
}

func TestSelf_GitConfigSandboxed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	sb := New(t)
	if got := sb.Git(sb.Home, "config", "--global", "user.email"); got != "e2e@devyard.invalid" {
		t.Fatalf("user.email = %q", got)
	}
	origins := sb.Git(sb.Home, "config", "--list", "--show-origin")
	if home, err := os.UserHomeDir(); err == nil && strings.Contains(origins, home+"/.gitconfig") {
		t.Fatalf("git reads the real ~/.gitconfig:\n%s", origins)
	}
	if strings.Contains(origins, "autosetupremote") {
		t.Fatalf("user git settings leaked:\n%s", origins)
	}
}

func TestSelf_FixtureHTTPEcho(t *testing.T) {
	sb := New(t)
	port := FreePort(t)
	health := filepath.Join(sb.Tmp, "healthy")
	sb.Start(RunOpts{Env: []string{"PORT=" + strconv.Itoa(port), "NAME=web", "HEALTH_FILE=" + health, "SECRET=s3"}}, FixturePath("httpecho"))
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	Eventually(t, "httpecho answers", func(c *C) {
		r := HTTPDo(nil, http.MethodGet, base+"/x", "web.p.localhost", nil, nil)
		if r.Err != nil || !strings.Contains(r.Body, "name=web host=web.p.localhost path=/x") {
			c.Errorf("%d %q %v", r.Code, r.Body, r.Err)
		}
	})
	if r := HTTPDo(nil, http.MethodGet, base+"/health", "", nil, nil); r.Code != 503 {
		t.Errorf("health without file: %d", r.Code)
	}
	_ = os.WriteFile(health, nil, 0o644)
	if r := HTTPDo(nil, http.MethodGet, base+"/health", "", nil, nil); r.Code != 200 {
		t.Errorf("health with file: %d", r.Code)
	}
	if r := HTTPDo(nil, http.MethodGet, base+"/env?k=SECRET", "", nil, nil); r.Body != "s3" {
		t.Errorf("env: %q", r.Body)
	}
}

func TestSelf_FixtureTickerLongLines(t *testing.T) {
	sb := New(t)
	r := sb.Exec(sb.Home, FixturePath("ticker"), "-interval", "0", "-count", "3", "-line-size", "200000", "-exit-code", "5")
	if r.Code != 5 {
		t.Fatalf("exit %d", r.Code)
	}
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	if len(lines) != 5 || len(lines[1]) != 200000 {
		t.Fatalf("got %d lines, line1 len %d", len(lines), len(lines[1]))
	}
}

func TestSelf_FixtureExiter(t *testing.T) {
	sb := New(t)
	cf := filepath.Join(sb.Tmp, "count")
	r := sb.Exec(sb.Home, FixturePath("exiter"), "-code", "7", "-after", "10ms", "-count-file", cf)
	if r.Code != 7 {
		t.Fatalf("exit %d", r.Code)
	}
	data, _ := os.ReadFile(cf)
	if strings.Count(string(data), "\n") != 1 {
		t.Fatalf("count file: %q", data)
	}
}

func TestSelf_FixturePrompter(t *testing.T) {
	sb := New(t)
	if r := sb.Exec(sb.Home, FixturePath("prompter")); r.Code != 3 || !strings.Contains(r.Stdout, "not a tty") {
		t.Fatalf("non-tty: %s", r)
	}
	cmd := exec.Command(FixturePath("prompter"), "-size")
	cmd.Env = sb.Env()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := newOutputBuffer()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			_, _ = out.Write(buf[:n])
			if err != nil {
				out.close()
				return
			}
		}
	}()
	if err := out.expect("size=100x30", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := out.expect("Name? ", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("bob\r"))
	if err := out.expect("hello bob", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("prompter exit: %v", err)
	}
}

func TestSelf_FixtureSigtrap(t *testing.T) {
	sb := New(t)
	p := sb.Start(RunOpts{}, FixturePath("sigtrap"))
	p.WaitOutput(t, "sigtrap ready")
	p.Signal(syscall.SIGTERM)
	p.WaitOutput(t, "sigtrap got terminated")
	time.Sleep(200 * time.Millisecond)
	if p.Exited() {
		t.Fatalf("sigtrap exited on SIGTERM")
	}
	p.Signal(syscall.SIGKILL)
	if r := p.Wait(t, 5*time.Second); r.Code != 128+int(syscall.SIGKILL) {
		t.Fatalf("exit %d", r.Code)
	}
}

func TestSelf_FixtureForkerEscapesGroup(t *testing.T) {
	sb := New(t)
	pidfile := filepath.Join(sb.Tmp, "gc.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, FixturePath("forker"), "-exit-after", "100ms", "-hold", "30s", "-pidfile", pidfile)
	cmd.Env = sb.Env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	gcPid := 0
	t.Cleanup(func() {
		if gcPid > 0 {
			_ = syscall.Kill(gcPid, syscall.SIGKILL)
		}
	})
	eof := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := stdout.Read(buf); err != nil {
				close(eof)
				return
			}
		}
	}()
	WaitFile(t, pidfile)
	data, _ := os.ReadFile(pidfile)
	gcPid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	pgid, err := syscall.Getpgid(gcPid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == cmd.Process.Pid {
		t.Fatalf("grandchild stayed in the parent's group")
	}
	select {
	case <-eof:
		t.Fatalf("stdout reached EOF while the grandchild holds it")
	case <-time.After(500 * time.Millisecond):
	}
	_ = syscall.Kill(gcPid, syscall.SIGKILL)
	select {
	case <-eof:
	case <-time.After(5 * time.Second):
		t.Fatalf("no EOF after killing the grandchild")
	}
	_ = cmd.Wait()
	// The grandchild carried our tag; nothing tagged may remain.
	Eventually(t, "no tagged processes", func(c *C) {
		if procs := sb.TaggedProcesses(); len(procs) > 0 {
			c.Errorf("%v", procs)
		}
	})
}

func TestSelf_TeardownReportsLeaks(t *testing.T) {
	// A tagged process that outlives its sandbox must be reported and
	// reaped by teardown. Drive teardown through a fake TB.
	ft := &fakeTB{name: "leaky"}
	sb := New(ft)
	cmd := exec.Command(FixturePath("sigtrap"))
	cmd.Env = sb.Env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	Eventually(t, "sigtrap visible", func(c *C) {
		if !containsPid(sb.TaggedProcesses(), cmd.Process.Pid) {
			c.Errorf("not yet")
		}
	})
	ft.runCleanups()
	if !ft.failed || !strings.Contains(strings.Join(ft.errors, "\n"), "LEAK") {
		t.Fatalf("teardown did not report the leak: %v", ft.errors)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatalf("leaked process not killed by teardown")
	}
	if _, err := os.Stat(sb.Root); !os.IsNotExist(err) {
		t.Fatalf("sandbox root not removed: %v", err)
	}
}

func containsPid(procs []ProcInfo, pid int) bool {
	for _, p := range procs {
		if p.Pid == pid {
			return true
		}
	}
	return false
}

type fakeTB struct {
	name     string
	cleanups []func()
	errors   []string
	failed   bool
}

var sprintf = fmt.Sprintf

func (f *fakeTB) Helper()           {}
func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Errorf(format string, args ...any) {
	f.failed = true
	f.errors = append(f.errors, sprintf(format, args...))
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
	panic("fakeTB.Fatalf: " + sprintf(format, args...))
}
func (f *fakeTB) Logf(string, ...any) {}
func (f *fakeTB) Name() string        { return f.name }
func (f *fakeTB) Failed() bool        { return f.failed }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}
