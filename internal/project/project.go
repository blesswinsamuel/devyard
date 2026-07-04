package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRuntimeBase is used when XDG_RUNTIME_DIR is unset. Compose-style
// tools normally live under $XDG_RUNTIME_DIR, but that var is not set on
// macOS by default, so we fall back to /tmp/local-compose.
const DefaultRuntimeBase = "/tmp/local-compose"

// DefaultStateBase is used when XDG_STATE_HOME is unset. Per the XDG spec
// the default is ~/.local/state.
const DefaultStateBase = ".local/state"

// AppDir is the per-application segment appended to the XDG base dirs.
const AppDir = "local-compose"

// Locations holds the resolved runtime and state paths for one project.
//
//   - Runtime dir holds the control socket and pidfiles (transient, may be
//     cleared on reboot). Never persisted across reboots.
//   - State dir holds per-service log files and the "stopped" marker used by
//     unless-stopped restart policy. Persisted across reboots.
type Locations struct {
	Name    string
	Runtime string
	State   string
	Socket  string
	Pidfile string
	LogsDir string
}

// Resolve derives the runtime and state directories for the given project
// name using the process environment. name must be non-empty.
func Resolve(name string) (*Locations, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	return resolve(name, os.Getenv, homeDir)
}

// homeDir returns the user's home directory, preferring $HOME.
func homeDir() (string, error) {
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h, nil
	}
	return "", fmt.Errorf("could not determine home directory: set $HOME")
}

// envGetter is a small abstraction so resolution can be unit-tested without
// mutating the real process environment.
type envGetter func(string) string

func resolve(name string, getenv envGetter, home func() (string, error)) (*Locations, error) {
	runtime := runtimeDir(getenv)
	if runtime == "" {
		runtime = filepath.Join(DefaultRuntimeBase, name)
	} else {
		runtime = filepath.Join(runtime, AppDir, name)
	}

	state, err := stateDir(getenv, home)
	if err != nil {
		return nil, err
	}
	state = filepath.Join(state, AppDir, name)

	return &Locations{
		Name:    name,
		Runtime: runtime,
		State:   state,
		Socket:  filepath.Join(runtime, "supervisor.sock"),
		Pidfile: filepath.Join(runtime, "supervisor.pid"),
		LogsDir: filepath.Join(state, "logs"),
	}, nil
}

func runtimeDir(getenv envGetter) string {
	return getenv("XDG_RUNTIME_DIR")
}

func stateDir(getenv envGetter, home func() (string, error)) (string, error) {
	if s := getenv("XDG_STATE_HOME"); s != "" {
		return s, nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, DefaultStateBase), nil
}

// MkdirAll creates the runtime and state directories (including logs) with
// appropriate permissions. Runtime files (socket, pidfile) live under
// 0700 dirs since they grant control over the supervised processes; state
// logs live under 0755.
func (l *Locations) MkdirAll() error {
	if err := os.MkdirAll(l.Runtime, 0o700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	if err := os.MkdirAll(l.LogsDir, 0o755); err != nil {
		return fmt.Errorf("create state logs dir: %w", err)
	}
	return nil
}
