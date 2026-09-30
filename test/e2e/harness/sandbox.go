package harness

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

// DefaultGlobalConfig binds the dashboard and proxy to ephemeral loopback
// ports; the daemon reports the bound addresses through GetDaemon.
const DefaultGlobalConfig = `web:
  host: 127.0.0.1
  port: 0
proxy:
  host: 127.0.0.1
  port: 0
`

// Sandbox is one hermetic devyard installation: private dirs, env, daemon.
type Sandbox struct {
	t  TB
	ID string

	Root       string // everything except the runtime dir lives here
	Home       string
	Tmp        string
	Runtime    string // XDG_RUNTIME_DIR (short, under /tmp)
	StateHome  string // XDG_STATE_HOME
	ConfigHome string // XDG_CONFIG_HOME
	DataHome   string
	CacheHome  string
	CARoot     string
	GitConfig  string

	env  []string
	dirs dirs

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	client    devyardv1connect.DaemonServiceClient
	closeCli  func()
	tracked   map[int]string // pid -> description
	recorder  *Watcher
	lastPid   int
	projects  []*Project
	closing   bool
	noCleanup bool
}

type options struct {
	globalConfig string
	env          []string
}

// Option customizes New.
type Option func(*options)

// WithGlobalConfig replaces the global config.yml content.
func WithGlobalConfig(yaml string) Option {
	return func(o *options) { o.globalConfig = yaml }
}

// WithEnv adds KEY=VALUE entries to the environment of every child.
func WithEnv(kv ...string) Option {
	return func(o *options) { o.env = append(o.env, kv...) }
}

// New creates a sandbox and registers its teardown (daemon stop, leak
// check, sweep, dir removal) with t.Cleanup.
func New(t TB, opts ...Option) *Sandbox {
	t.Helper()
	a := current()
	if a == nil {
		t.Fatalf("harness: call harness.Main from TestMain (or harness.Setup) before harness.New")
		return nil
	}
	o := options{globalConfig: DefaultGlobalConfig}
	for _, fn := range opts {
		fn(&o)
	}
	sb := &Sandbox{
		t:       t,
		ID:      fmt.Sprintf("%s-%d", a.runPrefix, a.seq.Add(1)),
		tracked: map[int]string{},
	}
	sb.ctx, sb.cancel = context.WithCancel(context.Background())
	var err error
	if sb.Root, err = os.MkdirTemp("", "dye2e-sb-"); err != nil {
		t.Fatalf("sandbox root: %v", err)
	}
	// Resolve symlinks (/var -> /private/var on macOS) so paths reported by
	// the daemon compare equal to ours.
	if r, err := filepath.EvalSymlinks(sb.Root); err == nil {
		sb.Root = r
	}
	// The runtime dir holds unix sockets: keep it short (sun_path ~104).
	if sb.Runtime, err = os.MkdirTemp("/tmp", "dy-"); err != nil {
		t.Fatalf("sandbox runtime dir: %v", err)
	}
	if r, err := filepath.EvalSymlinks(sb.Runtime); err == nil {
		sb.Runtime = r
	}
	_ = os.Chmod(sb.Runtime, 0o700)
	t.Cleanup(sb.teardown)

	sb.Home = filepath.Join(sb.Root, "home")
	sb.Tmp = filepath.Join(sb.Root, "tmp")
	sb.StateHome = filepath.Join(sb.Root, "state")
	sb.ConfigHome = filepath.Join(sb.Root, "config")
	sb.DataHome = filepath.Join(sb.Root, "data")
	sb.CacheHome = filepath.Join(sb.Root, "cache")
	sb.CARoot = filepath.Join(sb.Root, "caroot")
	sb.GitConfig = filepath.Join(sb.Root, "gitconfig")
	for _, d := range []string{sb.Home, sb.Tmp, sb.StateHome, sb.ConfigHome, sb.DataHome, sb.CacheHome, sb.CARoot, filepath.Join(sb.Root, "projects")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	gitcfg := "[user]\n\tname = Devyard E2E\n\temail = e2e@devyard.invalid\n" +
		"[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n" +
		"[core]\n\tpager = cat\n\tautocrlf = false\n[advice]\n\tdetachedHead = false\n"
	if err := os.WriteFile(sb.GitConfig, []byte(gitcfg), 0o644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}

	sb.env = sb.buildEnv(a, o.env)
	sb.dirs, err = resolveDirs(envLookup(sb.env))
	if err != nil {
		t.Fatalf("resolve sandbox dirs: %v", err)
	}
	if err := sb.guard(); err != nil {
		t.Fatalf("SANDBOX GUARD: %v", err)
	}
	if err := os.MkdirAll(sb.dirs.Config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(sb.dirs.globalConfig(), []byte(o.globalConfig), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	return sb
}

// buildEnv is the allowlist: nothing from os.Environ passes except PATH,
// TERM and LANG.
func (sb *Sandbox) buildEnv(a *artifacts, extra []string) []string {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		term = "xterm-256color"
	}
	env := []string{
		"PATH=" + a.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM=" + term,
		"HOME=" + sb.Home,
		"TMPDIR=" + sb.Tmp,
		"XDG_RUNTIME_DIR=" + sb.Runtime,
		"XDG_STATE_HOME=" + sb.StateHome,
		"XDG_CONFIG_HOME=" + sb.ConfigHome,
		"XDG_DATA_HOME=" + sb.DataHome,
		"XDG_CACHE_HOME=" + sb.CacheHome,
		"CAROOT=" + sb.CARoot,
		"SHELL=/bin/sh",
		"GIT_CONFIG_GLOBAL=" + sb.GitConfig,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		SandboxIDVar + "=" + sb.ID,
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return append(env, extra...)
}

// guard aborts when the sandbox would reach the real daemon.
func (sb *Sandbox) guard() error {
	sock := sb.dirs.socket()
	if !strings.HasPrefix(sock, sb.Runtime+string(filepath.Separator)) {
		return fmt.Errorf("sandbox socket %s is outside the sandbox runtime dir %s", sock, sb.Runtime)
	}
	if len(sock) >= 100 {
		return fmt.Errorf("sandbox socket path too long (%d bytes): %s", len(sock), sock)
	}
	if real, err := resolveDirs(os.Getenv); err == nil {
		if filepath.Clean(real.socket()) == filepath.Clean(sock) {
			return fmt.Errorf("sandbox socket %s equals the real daemon socket", sock)
		}
		if filepath.Clean(real.State) == filepath.Clean(sb.dirs.State) {
			return fmt.Errorf("sandbox state dir %s equals the real state dir", sb.dirs.State)
		}
		if filepath.Clean(real.Config) == filepath.Clean(sb.dirs.Config) {
			return fmt.Errorf("sandbox config dir %s equals the real config dir", sb.dirs.Config)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Clean(sb.Home) {
		return fmt.Errorf("sandbox HOME equals the real HOME")
	}
	return nil
}

// T returns the test the sandbox belongs to.
func (sb *Sandbox) T() TB { return sb.t }

// Context is cancelled when the sandbox is torn down.
func (sb *Sandbox) Context() context.Context { return sb.ctx }

// Env returns a copy of the child environment.
func (sb *Sandbox) Env() []string { return append([]string(nil), sb.env...) }

// Getenv looks up a variable in the child environment.
func (sb *Sandbox) Getenv(k string) string { return envLookup(sb.env)(k) }

// SocketPath is the daemon control socket inside the sandbox.
func (sb *Sandbox) SocketPath() string { return sb.dirs.socket() }

// PidfilePath is the daemon pidfile inside the sandbox.
func (sb *Sandbox) PidfilePath() string { return sb.dirs.pidfile() }

// DaemonLogPath is the daemon's log file inside the sandbox.
func (sb *Sandbox) DaemonLogPath() string { return sb.dirs.daemonLog() }

// GlobalConfigPath is $XDG_CONFIG_HOME/devyard/config.yml.
func (sb *Sandbox) GlobalConfigPath() string { return sb.dirs.globalConfig() }

// AppStateDir is $XDG_STATE_HOME/devyard.
func (sb *Sandbox) AppStateDir() string { return sb.dirs.State }

// ProjectStateDir is the per-project state dir of project id.
func (sb *Sandbox) ProjectStateDir(id string) string {
	return filepath.Join(sb.dirs.projectsDir(), id)
}

// KeepOnExit disables dir removal at teardown (for debugging).
func (sb *Sandbox) KeepOnExit() {
	sb.mu.Lock()
	sb.noCleanup = true
	sb.mu.Unlock()
}

// Project is a devyard project directory inside the sandbox.
type Project struct {
	sb         *Sandbox
	ID         string
	Dir        string
	ConfigPath string

	mu    sync.Mutex
	ports map[string]int
}

// WriteProject creates <sandbox>/projects/<name>/devyard.yml from yaml plus
// the extra files (paths relative to the project dir). Both are Go
// templates with:
//
//	{{fixture "ticker"}}  absolute path of a fixture binary
//	{{port "web"}}        a free TCP port, stable per name within the project
//	{{.Dir}} {{.Name}}    the project dir and name
//	{{devyard}}           the devyard binary under test
//
// The project id defaults to name; configs that set `name:` should use the
// same value (or set p.ID).
func (sb *Sandbox) WriteProject(name, yaml string, files map[string]string) *Project {
	sb.t.Helper()
	return sb.WriteProjectAt(name, filepath.Join(sb.Root, "projects", name), yaml, files)
}

// WriteProjectAt is WriteProject with an explicit directory (e.g. a
// subdirectory of a shared git repository).
func (sb *Sandbox) WriteProjectAt(id, dir, yaml string, files map[string]string) *Project {
	sb.t.Helper()
	p := &Project{
		sb:    sb,
		ID:    id,
		Dir:   dir,
		ports: map[string]int{},
	}
	p.ConfigPath = filepath.Join(p.Dir, "devyard.yml")
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		sb.t.Fatalf("mkdir project: %v", err)
	}
	p.WriteConfig(yaml)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p.WriteFile(k, files[k])
	}
	sb.mu.Lock()
	sb.projects = append(sb.projects, p)
	sb.mu.Unlock()
	return p
}

// Render expands the harness template syntax (see WriteProject).
func (p *Project) Render(text string) string {
	p.sb.t.Helper()
	funcs := template.FuncMap{
		"fixture": FixturePath,
		"port":    p.Port,
		"devyard": func() string { bin, _ := DevyardPath(); return bin },
	}
	tmpl, err := template.New("cfg").Funcs(funcs).Option("missingkey=error").Parse(text)
	if err != nil {
		p.sb.t.Fatalf("parse template: %v\n%s", err, text)
	}
	var b bytes.Buffer
	data := struct{ Name, Dir string }{p.ID, p.Dir}
	if err := tmpl.Execute(&b, data); err != nil {
		p.sb.t.Fatalf("render template: %v", err)
	}
	return b.String()
}

// WriteConfig (re)writes devyard.yml.
func (p *Project) WriteConfig(yaml string) {
	p.sb.t.Helper()
	p.WriteFile("devyard.yml", yaml)
}

// WriteFile writes a templated file relative to the project dir.
func (p *Project) WriteFile(rel, content string) {
	p.sb.t.Helper()
	path := p.Path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.sb.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(p.Render(content)), 0o644); err != nil {
		p.sb.t.Fatalf("write %s: %v", path, err)
	}
}

// Path joins rel onto the project dir.
func (p *Project) Path(rel string) string { return filepath.Join(p.Dir, rel) }

// Port returns a free TCP port reserved for name (stable per project).
func (p *Project) Port(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if port, ok := p.ports[name]; ok {
		return port
	}
	port := FreePort(p.sb.t)
	p.ports[name] = port
	return port
}

// CLI runs devyard with cwd = the project dir.
func (p *Project) CLI(args ...string) Result {
	p.sb.t.Helper()
	return p.sb.CLIIn(p.Dir, args...)
}

// Sandbox returns the owning sandbox.
func (p *Project) Sandbox() *Sandbox { return p.sb }

// FreePort returns a currently unused loopback TCP port. Only fixtures use
// it: the daemon's own listeners use port 0.
func FreePort(t TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// WaitFile waits until path exists.
func WaitFile(t TB, path string) {
	t.Helper()
	Eventually(t, "file "+path+" exists", func(c *C) {
		if _, err := os.Stat(path); err != nil {
			c.Errorf("%v", err)
		}
	})
}

func (sb *Sandbox) diagTimeout() time.Duration { return 3 * time.Second }
