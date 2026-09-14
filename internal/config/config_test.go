package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
)

func TestLoadBuildStringAndObject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  api:
    command: echo api
    build: cargo build
  web:
    command: echo web
    build:
      command: pnpm build
      working_dir: ./web
      env:
        NODE_ENV: production
      shell: bash
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	api := file.Services["api"]
	if api.Build == nil || api.Build.Spec.Command != "cargo build" || api.Build.Spec.Shell != "sh" {
		t.Fatalf("api build: %+v", api.Build)
	}
	web := file.Services["web"]
	if web.Build == nil || web.Build.Spec.Command != "pnpm build" || web.Build.Spec.WorkingDir != "./web" {
		t.Fatalf("web build: %+v", web.Build)
	}
	if web.Build.Spec.Env["NODE_ENV"] != "production" || web.Build.Spec.Shell != "bash" {
		t.Fatalf("web build env/shell: %+v", web.Build.Spec)
	}
}

func TestDependsOnListAndMap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  db:
    command: echo db
    healthcheck:
      test: ["CMD-SHELL", "true"]
  api:
    command: echo api
    depends_on:
      db: { condition: service_healthy }
  web:
    command: echo web
    depends_on: [api]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	api := file.Services["api"]
	if api.DependsOn.Entries["db"].Condition != config.ConditionServiceHealthy {
		t.Fatalf("api depends_on db condition: %+v", api.DependsOn)
	}
	web := file.Services["web"]
	if web.DependsOn.Entries["api"].Condition != config.ConditionServiceStarted {
		t.Fatalf("web depends_on api: %+v", web.DependsOn)
	}
}

func TestHealthcheckDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  api:
    command: echo api
    healthcheck:
      test: ["CMD", "true"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hc := file.Services["api"].Healthcheck
	if hc.Interval != 5*time.Second || hc.Timeout != 2*time.Second || hc.Retries != 3 {
		t.Fatalf("defaults: interval=%v timeout=%v retries=%d", hc.Interval, hc.Timeout, hc.Retries)
	}
}

func TestTTYField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  web:
    command: npm start
    tty: true
  api:
    command: echo api
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !file.Services["web"].TTY {
		t.Fatal("expected web.tty = true")
	}
	if file.Services["api"].TTY {
		t.Fatal("expected api.tty = false (default)")
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
	if len(vars) != len(want) {
		t.Fatalf("vars = %v, want %v", vars, want)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("vars[%q] = %q, want %q", k, vars[k], v)
		}
	}
}

func TestParseDotEnvMalformed(t *testing.T) {
	t.Parallel()
	if _, err := config.ParseDotEnv([]byte("NOKEY\n")); err == nil {
		t.Fatal("ParseDotEnv with a non-assignment line: expected error, got nil")
	}
}

func TestLoadWithEnvInterpolation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  api:
    command: echo port=${PORT:-8080}
    env:
      PATH: ${PATH}:/custom/bin
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dotenv := map[string]string{"PORT": "9090"}
	file, err := config.LoadWithEnv(path, dotenv)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	api := file.Services["api"]
	if api.Command != "echo port=9090" {
		t.Errorf("command = %q, want %q", api.Command, "echo port=9090")
	}
	if !strings.HasSuffix(api.Env["PATH"], ":/custom/bin") {
		t.Errorf("env PATH = %q, want suffix %q", api.Env["PATH"], ":/custom/bin")
	}
}

func TestBuildEnvOver(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin", "FOO=base"}
	svc := map[string]string{"FOO": "svc", "BAR": "new"}
	out := config.BuildEnvOver(base, svc)
	got := map[string]string{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["FOO"] != "svc" {
		t.Errorf("FOO = %q, want svc", got["FOO"])
	}
	if got["BAR"] != "new" {
		t.Errorf("BAR = %q, want new", got["BAR"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want /usr/bin", got["PATH"])
	}
}

func TestTasksParsingAndValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local-compose.yml")
	content := `version: "1"
services:
  db:
    command: echo db
    healthcheck:
      test: ["CMD-SHELL", "true"]
tasks:
  migrate: npx prisma db push
  seed:
    command: node scripts/seed.js
    working_dir: ./backend
    env:
      NODE_ENV: development
    depends_on:
      db: { condition: service_healthy }
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(file.Tasks) != 2 {
		t.Fatalf("Tasks length = %d, want 2", len(file.Tasks))
	}
	migrate := file.Tasks["migrate"]
	if migrate.Spec.Command != "npx prisma db push" || migrate.Spec.Shell != "sh" {
		t.Errorf("migrate task: %+v", migrate.Spec)
	}
	seed := file.Tasks["seed"]
	if seed.Spec.Command != "node scripts/seed.js" || seed.Spec.WorkingDir != "./backend" || seed.Spec.Env["NODE_ENV"] != "development" {
		t.Errorf("seed task: %+v", seed.Spec)
	}
	if seed.Spec.DependsOn.Entries["db"].Condition != config.ConditionServiceHealthy {
		t.Errorf("seed depends_on db condition: %+v", seed.Spec.DependsOn)
	}
}
