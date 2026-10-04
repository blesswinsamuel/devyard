package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

// writeProject writes devyard.yml (and any extra files) into a temp dir and
// returns the config path.
func writeProject(t *testing.T, content string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T, content string, files map[string]string) *config.Project {
	t.Helper()
	p, err := config.Load(writeProject(t, content, files), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

func loadErr(t *testing.T, content, want string) {
	t.Helper()
	_, err := config.Load(writeProject(t, content, nil), nil)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Load err = %v, want it to contain %q", err, want)
	}
}

func mustResolve(t *testing.T, p *config.Project, launch []string) *config.Resolved {
	t.Helper()
	next := 40000
	r, err := p.Resolve(launch, nil, func() (int, error) { next++; return next, nil })
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func TestEmptyConfigIsAProject(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, "", nil)
	if p.ID == "" || len(p.File.Services) != 0 {
		t.Fatalf("project = %+v", p)
	}
}

func TestLoadAllowMissing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "My Notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.FileName)
	if _, err := config.Load(path, nil); err == nil {
		t.Fatal("Load of a missing config must fail")
	}
	p, err := config.LoadAllowMissing(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Missing || p.ID != "my-notes" || len(p.File.Services) != 0 || p.DotEnv["A"] != "1" {
		t.Fatalf("git-only project = %+v", p)
	}
	if err := os.WriteFile(path, []byte("name: notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err = config.LoadAllowMissing(path, nil); err != nil || p.Missing || p.ID != "notes" {
		t.Fatalf("existing config: %+v %v", p, err)
	}
}

func TestProjectIDFromName(t *testing.T) {
	t.Parallel()
	if p := mustLoad(t, "name: My App\n", nil); p.ID != "my-app" {
		t.Fatalf("id = %q, want my-app", p.ID)
	}
	dir := filepath.Join(t.TempDir(), "Some Dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "some-dir" {
		t.Fatalf("id = %q, want some-dir", p.ID)
	}
}

func TestRunStringAndList(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
services:
  short: bun run dev
  list: [cargo, run, --bin, "my api"]
  long:
    run: echo hi
tasks:
  t1: echo t1
  t2: [make, test]
`, nil)
	s := p.File.Services
	if got := s["short"].Run.Args(); !reflect.DeepEqual(got, []string{"sh", "-c", "bun run dev"}) {
		t.Errorf("short args = %q", got)
	}
	if got := s["list"].Run.Args("x y"); !reflect.DeepEqual(got, []string{"cargo", "run", "--bin", "my api", "x y"}) {
		t.Errorf("list args = %q", got)
	}
	if got := s["list"].Run.String(); got != "cargo run --bin 'my api'" {
		t.Errorf("list display = %q", got)
	}
	if got := p.File.Tasks["t1"].Run.Args("a b"); !reflect.DeepEqual(got, []string{"sh", "-c", "echo t1 'a b'"}) {
		t.Errorf("t1 args = %q", got)
	}
	if got := p.File.Tasks["t2"].Run.Args(); !reflect.DeepEqual(got, []string{"make", "test"}) {
		t.Errorf("t2 args = %q", got)
	}
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, "services:\n  api: ./api\n", nil)
	api := p.File.Services["api"]
	if api.Restart != config.RestartOnFailure {
		t.Errorf("restart = %q, want on-failure", api.Restart)
	}
	if api.Stop.Timeout != config.DefaultStopTimeout || api.Stop.Signal != "" {
		t.Errorf("stop = %+v", api.Stop)
	}
	if !api.Starts() || api.TTY {
		t.Errorf("autostart/tty defaults wrong: %+v", api)
	}
}

func TestTaskTTYDefaultsTrue(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
tasks:
  short: echo short
  long:
    run: echo long
  plain:
    run: echo plain
    tty: false
`, nil)
	tasks := p.File.Tasks
	if !tasks["short"].IsTTY() || !tasks["long"].IsTTY() || tasks["plain"].IsTTY() {
		t.Fatal("unexpected tty defaults")
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content, want string
	}{
		{"missing run", "services:\n  api:\n    dir: x\n", "run is required"},
		{"null service", "services:\n  api:\n", "run is required"},
		{"bad service name", "services:\n  My_Api: x\n", "must be lowercase"},
		{"unknown dep", "services:\n  api:\n    run: x\n    depends_on: [db]\n", `unknown service "db"`},
		{"self dep", "services:\n  api:\n    run: x\n    depends_on: [api]\n", "itself"},
		{"dup dep", "services:\n  db: x\n  api:\n    run: x\n    depends_on: [db, db]\n", "twice"},
		{"bad restart", "services:\n  api:\n    run: x\n    restart: no\n", "invalid restart policy"},
		{"port and ports", "services:\n  api:\n    run: x\n    port: 1\n    ports: {http: 2}\n", "mutually exclusive"},
		{"bad port", "services:\n  api:\n    run: x\n    port: 70000\n", "between 1 and 65535"},
		{"bad port word", "services:\n  api:\n    run: x\n    port: any\n", `"auto"`},
		{"bad port name", "services:\n  api:\n    run: x\n    ports: {Http: 1}\n", "must be lowercase"},
		{"bad host", "services:\n  api:\n    run: x\n    port: 1\n    host: a.b\n", "host"},
		{"primary unknown", "primary: web\nservices:\n  api: x\n", "unknown service"},
		{"primary portless", "primary: api\nservices:\n  api: x\n", "has no port"},
		{"ready none", "services:\n  api:\n    run: x\n    ready: {}\n", "exactly one"},
		{"ready two", "services:\n  api:\n    run: x\n    port: 1\n    ready: {tcp: {}, exec: true}\n", "exactly one"},
		{"ready no port", "services:\n  api:\n    run: x\n    ready: {tcp: {}}\n", "has no port"},
		{"ready bad port name", "services:\n  api:\n    run: x\n    port: 1\n    ready: {http: {port: metrics}}\n", `unknown port "metrics"`},
		{"ready bad path", "services:\n  api:\n    run: x\n    port: 1\n    ready: {http: {path: health}}\n", "start with /"},
		{"negative stop", "services:\n  api:\n    run: x\n    stop: {timeout: -1s}\n", "negative"},
		{"build missing run", "services:\n  api:\n    run: x\n    build: {dir: x}\n", "build.run"},
		{"task missing run", "tasks:\n  t:\n    dir: x\n", "run is required"},
		{"task unknown dep", "tasks:\n  t:\n    run: x\n    depends_on: [db]\n", `unknown service "db"`},
		{"ref unknown service", "services:\n  api:\n    run: echo ${db.port}\n", `unknown service "db"`},
		{"ref portless", "services:\n  db: x\n  api:\n    run: x\n    env: {X: \"${db.url}\"}\n", "has no port"},
		{"ref unknown port", "services:\n  db:\n    run: x\n    port: 1\n  api:\n    run: echo ${db.ports.admin}\n", `no port "admin"`},
		{"bad link", "links:\n  docs: not a url\n", "absolute url"},
		{"bad env name", "env:\n  \"A B\": x\n", "invalid env name"},
		{"run map", "services:\n  api:\n    run: {a: b}\n", "string or a list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			loadErr(t, c.content, c.want)
		})
	}
}

func TestReadyProbes(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
services:
  web:
    run: x
    ports: {http: 3000, admin: auto}
    ready:
      http: {path: /health, port: admin, status: 204}
      interval: 1s
  db:
    run: x
    port: 5432
    ready: {tcp: {}}
  q:
    run: x
    ready:
      exec: pg_isready -q
`, nil)
	web := p.File.Services["web"].Ready
	if web.Interval != time.Second || web.Timeout != config.DefaultTimeout || web.Retries != config.DefaultRetries {
		t.Errorf("web ready defaults = %+v", web)
	}
	r := mustResolve(t, p, nil)
	if got := r.Services["web"].Ready; got.URL != "http://127.0.0.1:40001/health" || got.Status != 204 || got.Kind() != "http" {
		t.Errorf("web probe = %+v", got)
	}
	if got := r.Services["db"].Ready; got.Addr != "127.0.0.1:5432" || got.Kind() != "tcp" {
		t.Errorf("db probe = %+v", got)
	}
	if got := r.Services["q"].Ready; !reflect.DeepEqual(got.Exec, []string{"sh", "-c", "pg_isready -q"}) || got.Kind() != "exec" {
		t.Errorf("q probe = %+v", got)
	}
}

func TestPortsAndAutoAllocation(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
services:
  web:
    run: x
    port: auto
  api:
    run: x
    ports:
      http: 3000
      metrics: auto
  db: x
`, nil)
	assigned := config.PortAssignments{"web": 41000, "gone": 42000}
	next := 41000
	r, err := p.Resolve(nil, assigned, func() (int, error) { next++; return next, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Services["web"].Ports; !reflect.DeepEqual(got, []config.ResolvedPort{{Port: 41000, Auto: true}}) {
		t.Errorf("web ports = %+v (kept assignment expected)", got)
	}
	// 41001 is free; 41000 is taken by web.
	want := []config.ResolvedPort{{Name: "http", Port: 3000}, {Name: "metrics", Port: 41001, Auto: true}}
	if got := r.Services["api"].Ports; !reflect.DeepEqual(got, want) {
		t.Errorf("api ports = %+v, want %+v", got, want)
	}
	if want := (config.PortAssignments{"web": 41000, "api.metrics": 41001}); !reflect.DeepEqual(r.Ports, want) {
		t.Errorf("assignments = %v, want %v (stale entries dropped)", r.Ports, want)
	}
	env := config.EnvMap(r.Services["api"].Env)
	if env["PORT"] != "3000" || env["PORT_HTTP"] != "3000" || env["PORT_METRICS"] != "41001" {
		t.Errorf("api port env = %v", env)
	}
	if _, ok := config.EnvMap(r.Services["db"].Env)["PORT"]; ok {
		t.Error("portless service got PORT")
	}
}

func TestServiceReferences(t *testing.T) {
	t.Parallel()
	path := writeProject(t, `
services:
  api:
    run: x
    ports: {http: 8080, admin-ui: 9000}
  my-web:
    run: [serve, "--api=${api.url}"]
    env:
      API: ${api.port}
      ADMIN: http://localhost:${api.ports.admin-ui}/
      HOME_DIR: ${HOME}
tasks:
  seed: curl ${api.url}/seed
`, nil)
	p, err := config.Load(path, []string{"HOME=/home/me"})
	if err != nil {
		t.Fatal(err)
	}
	r := mustResolve(t, p, nil)
	web := r.Services["my-web"]
	if want := []string{"serve", "--api=http://127.0.0.1:8080"}; !reflect.DeepEqual(web.Cmd.Argv, want) {
		t.Errorf("argv = %q, want %q", web.Cmd.Argv, want)
	}
	env := config.EnvMap(web.Env)
	if env["API"] != "8080" || env["ADMIN"] != "http://localhost:9000/" || env["HOME_DIR"] != "/home/me" {
		t.Errorf("env = %v", env)
	}
	if got := r.Tasks["seed"].Cmd.Script; got != "curl http://127.0.0.1:8080/seed" {
		t.Errorf("task = %q", got)
	}
}

func TestEnvFilesAndLayering(t *testing.T) {
	t.Parallel()
	path := writeProject(t, `
env:
  LEVEL: project
  SHARED: project
services:
  api:
    run: echo ${FROM_DOTENV}
    dir: api
    env_files: [api/.env]
    env:
      OWN: svc
    build:
      run: make
      env: {BUILD: yes}
tasks:
  t:
    run: x
    env: {SHARED: task}
`, map[string]string{
		".env":       "FROM_DOTENV=dot\nSHARED=dotenv\nLOCAL=base\n",
		".env.local": "LOCAL=override\n",
		"api/.env":   "SHARED=api-file\nOWN=file\n",
	})
	p, err := config.Load(path, []string{"PATH=/bin", "LEVEL=launch"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if want := []string{filepath.Join(dir, ".env"), filepath.Join(dir, ".env.local")}; !reflect.DeepEqual(p.EnvFiles, want) {
		t.Errorf("env files = %v, want %v", p.EnvFiles, want)
	}
	if p.File.Services["api"].Run.Script != "echo dot" {
		t.Errorf("interpolation from .env failed: %q", p.File.Services["api"].Run.Script)
	}
	r := mustResolve(t, p, []string{"PATH=/bin", "LEVEL=launch"})
	api := r.Services["api"]
	env := config.EnvMap(api.Env)
	want := map[string]string{
		"PATH": "/bin", "LEVEL": "project", "FROM_DOTENV": "dot", "LOCAL": "override",
		"SHARED": "api-file", "OWN": "svc",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("api %s = %q, want %q", k, env[k], v)
		}
	}
	if api.Dir != filepath.Join(dir, "api") {
		t.Errorf("dir = %q", api.Dir)
	}
	if slicesContains(api.EnvKeys, "PATH") || !slicesContains(api.EnvKeys, "OWN") {
		t.Errorf("env keys = %v (launch env excluded, own included)", api.EnvKeys)
	}
	benv := config.EnvMap(api.Build.Env)
	if benv["BUILD"] != "yes" || benv["OWN"] != "svc" || api.Build.Dir != api.Dir {
		t.Errorf("build = %+v", api.Build)
	}
	if got := config.EnvMap(r.Tasks["t"].Env)["SHARED"]; got != "task" {
		t.Errorf("task SHARED = %q", got)
	}
}

func TestEnvFilesExplicitAndInterpolationOrder(t *testing.T) {
	t.Parallel()
	path := writeProject(t, `
env_files: [config/dev.env, missing.env]
services:
  api: echo ${A} ${B:-none}
`, map[string]string{
		".env":           "B=ignored\n",
		"config/dev.env": "A=dev\n",
	})
	p, err := config.Load(path, []string{"A=launch"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.File.Services["api"].Run.Script; got != "echo launch none" {
		t.Errorf("run = %q (launch env wins, default .env not loaded)", got)
	}
	if len(p.EnvFiles) != 1 {
		t.Errorf("env files = %v", p.EnvFiles)
	}
}

func TestLocalOverlay(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
name: app
services:
  api:
    run: ./api
    env: {A: "1", B: "2"}
    depends_on: [db]
  db: postgres
`, map[string]string{config.LocalFileName: `
services:
  api:
    env: {B: local}
    depends_on: []
  extra: ./extra
`})
	api := p.File.Services["api"]
	if api.Run.Script != "./api" || api.Env["A"] != "1" || api.Env["B"] != "local" || len(api.DependsOn) != 0 {
		t.Errorf("api = %+v", api)
	}
	if p.File.Services["extra"] == nil || p.File.Services["db"] == nil {
		t.Error("services not merged")
	}
}

func TestInterpolatedNumbers(t *testing.T) {
	t.Parallel()
	path := writeProject(t, "services:\n  api:\n    run: x\n    port: ${API_PORT:-4000}\n", nil)
	p, err := config.Load(path, []string{"API_PORT=4123"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.File.Services["api"].Port.Number; got != 4123 {
		t.Errorf("port = %d", got)
	}
}

func TestUnknownFieldWarnings(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
version: "1"
services:
  api:
    run: x
    comand: typo
    port: 1
    ready:
      tcp: {prot: 1}
  short: echo
tasks:
  t:
    run: x
    shell: bash
`, nil)
	got := strings.Join(p.Warnings, "\n")
	for _, want := range []string{`"version"`, `"comand" at services.api.comand`, `"prot" at services.api.ready.tcp.prot`, `"shell" at tasks.t.shell`} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings %q missing %s", got, want)
		}
	}
	if len(p.Warnings) != 4 {
		t.Errorf("warnings = %q, want 4", p.Warnings)
	}
}

func TestLinksAndPrimary(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
primary: web
links:
  Zeta: https://z.example.com
  Alpha: http://localhost:3001
services:
  web:
    run: x
    port: 3000
`, nil)
	want := config.Links{{Name: "Zeta", URL: "https://z.example.com"}, {Name: "Alpha", URL: "http://localhost:3001"}}
	if !reflect.DeepEqual(p.File.Links, want) {
		t.Errorf("links = %+v (order must be preserved)", p.File.Links)
	}
	if p.File.Primary != "web" {
		t.Errorf("primary = %q", p.File.Primary)
	}
}

func TestBuildForms(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, `
services:
  a:
    run: x
    build: cargo build
  b:
    run: x
    build: [make, all]
  c:
    run: x
    dir: c
    build:
      run: pnpm build
      dir: web
      sources: ["src/**/*.ts", package.json]
`, nil)
	s := p.File.Services
	if s["a"].Build.Run.Script != "cargo build" || s["b"].Build.Run.Argv[1] != "all" {
		t.Errorf("build shorthands = %+v %+v", s["a"].Build, s["b"].Build)
	}
	r := mustResolve(t, p, nil)
	c := r.Services["c"].Build
	if c.Dir != filepath.Join(p.Dir, "web") || c.Sources[0] != filepath.Join(p.Dir, "src/**/*.ts") {
		t.Errorf("c build = %+v", c)
	}
}

func TestAutostart(t *testing.T) {
	t.Parallel()
	p := mustLoad(t, "services:\n  sb:\n    run: x\n    autostart: false\n", nil)
	if p.File.Services["sb"].Starts() {
		t.Fatal("autostart: false ignored")
	}
}

func TestInterpolate(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"PORT":  "8080",
		"EMPTY": "",
		"PATH":  "/usr/bin",
	}
	cases := []struct {
		in   string
		want string
		warn int
	}{
		{in: "port=${PORT}", want: "port=8080", warn: 0},
		{in: "d=${MISSING}", want: "d=", warn: 1},
		{in: "d=${MISSING:-9}", want: "d=9", warn: 0},
		{in: "d=${MISSING-9}", want: "d=9", warn: 0},
		{in: "e=${EMPTY:-9}", want: "e=9", warn: 0},
		{in: "e=${EMPTY-9}", want: "e=", warn: 0},
		{in: "p=${PATH}:/bin", want: "p=/usr/bin:/bin", warn: 0},
		{in: "$$literal", want: "$literal", warn: 0},
		{in: "$HOME-bare", want: "$HOME-bare", warn: 0},
		{in: "nested ${PORT}", want: "nested 8080", warn: 0},
		{in: "unterminated ${PORT", want: "unterminated ${PORT", warn: 0},
		{in: "ref ${api.port} ${my-api.ports.http}", want: "ref ${api.port} ${my-api.ports.http}", warn: 0},
	}
	for i, c := range cases {
		warns := 0
		got := config.Interpolate(c.in, env, func(string) { warns++ })
		if got != c.want {
			t.Errorf("case %d: Interpolate(%q) = %q, want %q", i, c.in, got, c.want)
		}
		if warns != c.warn {
			t.Errorf("case %d: warnings = %d, want %d", i, warns, c.warn)
		}
	}
}

func TestParseDotEnv(t *testing.T) {
	t.Parallel()
	data := []byte("# comment\n\nFOO=bar\nSPACED=hello world\n" +
		"QUOTED=\"double\"\nSINGLE='single'\nexport EXPORTED=yes\nEMPTY=\n")
	vars, err := config.ParseDotEnv(data)
	if err != nil {
		t.Fatalf("ParseDotEnv: %v", err)
	}
	want := map[string]string{
		"FOO":      "bar",
		"SPACED":   "hello world",
		"QUOTED":   "double",
		"SINGLE":   "single",
		"EXPORTED": "yes",
		"EMPTY":    "",
	}
	if !reflect.DeepEqual(vars, want) {
		t.Fatalf("vars = %v, want %v", vars, want)
	}
}

func TestParseDotEnvMalformed(t *testing.T) {
	t.Parallel()
	if _, err := config.ParseDotEnv([]byte("NOKEY\n")); err == nil {
		t.Fatal("ParseDotEnv with a non-assignment line: expected error, got nil")
	}
}

func TestPortAssignmentsRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sub", "ports.json")
	a, err := config.ReadPortAssignments(path)
	if err != nil || len(a) != 0 {
		t.Fatalf("missing file: %v %v", a, err)
	}
	want := config.PortAssignments{"web": 1234, "api.metrics": 2345}
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := config.ReadPortAssignments(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v, want %v", got, err, want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"plain": "plain", "": "''", "a b": "'a b'", "it's": `'it'\''s'`, "--x=1": "--x=1"} {
		if got := config.ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
