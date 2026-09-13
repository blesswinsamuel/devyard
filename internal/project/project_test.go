package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blesswinsamuel/local-compose/internal/project"
)

func TestResolveDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	loc, err := project.Resolve("myapp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Name != "myapp" {
		t.Fatalf("name: %q", loc.Name)
	}
	wantState := filepath.Join(home, ".local", "state", "local-compose", "myapp")
	if loc.State != wantState {
		t.Fatalf("state: got %q want %q", loc.State, wantState)
	}
	wantRuntime := filepath.Join(wantState, "run")
	if loc.Runtime != wantRuntime {
		t.Fatalf("runtime: got %q want %q", loc.Runtime, wantRuntime)
	}
	if loc.LogsDir != filepath.Join(loc.State, "logs") {
		t.Fatalf("logs dir: %q", loc.LogsDir)
	}
}

func TestResolveDaemonDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	loc, err := project.ResolveDaemon()
	if err != nil {
		t.Fatalf("ResolveDaemon: %v", err)
	}
	wantState := filepath.Join(home, ".local", "state", "local-compose")
	if loc.State != wantState {
		t.Fatalf("state: got %q want %q", loc.State, wantState)
	}
	wantRuntime := filepath.Join(wantState, "run")
	if loc.Runtime != wantRuntime {
		t.Fatalf("runtime: got %q want %q", loc.Runtime, wantRuntime)
	}
	if loc.Socket != filepath.Join(wantRuntime, "daemon.sock") {
		t.Fatalf("socket: got %q want %q", loc.Socket, filepath.Join(wantRuntime, "daemon.sock"))
	}
	if loc.Pidfile != filepath.Join(wantRuntime, "daemon.pid") {
		t.Fatalf("pidfile: got %q want %q", loc.Pidfile, filepath.Join(wantRuntime, "daemon.pid"))
	}
	if loc.LogFile != filepath.Join(wantState, "daemon.log") {
		t.Fatalf("log file: got %q want %q", loc.LogFile, filepath.Join(wantState, "daemon.log"))
	}
}

func TestResolveXDGHonored(t *testing.T) {
	rt := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("XDG_STATE_HOME", st)
	t.Setenv("HOME", t.TempDir())

	loc, err := project.Resolve("api")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Runtime != filepath.Join(rt, "local-compose", "api") {
		t.Fatalf("runtime: %q", loc.Runtime)
	}
	if loc.State != filepath.Join(st, "local-compose", "api") {
		t.Fatalf("state: %q", loc.State)
	}
}

func TestResolveEmptyName(t *testing.T) {
	if _, err := project.Resolve("  "); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestResolveNoHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := project.Resolve("myapp"); err == nil {
		t.Fatal("expected error when HOME unset and no XDG_STATE_HOME")
	}
}

func TestMkdirAllCreatesDirs(t *testing.T) {
	rt := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("XDG_STATE_HOME", st)
	t.Setenv("HOME", t.TempDir())

	loc, err := project.Resolve("myapp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := loc.MkdirAll(); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, p := range []string{loc.Runtime, loc.LogsDir} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s not a dir", p)
		}
	}
}
