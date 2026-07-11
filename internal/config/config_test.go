package config_test

import (
	"os"
	"path/filepath"
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
