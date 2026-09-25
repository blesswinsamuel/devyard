package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultStateBase is used when XDG_STATE_HOME is unset. Per the XDG spec
// the default is ~/.local/state.
const DefaultStateBase = ".local/state"

// AppDir is the per-application segment appended to the XDG base dirs.
const AppDir = "devyard"

// Locations holds the resolved runtime and state paths for one project.
//
//   - Runtime dir holds transient files (cleared on reboot). When
//     XDG_RUNTIME_DIR is unset (e.g. on macOS), it falls back to a "run"
//     subdirectory within the user's state dir so it is immune to OS /tmp
//     periodic sweeps.
//   - State dir holds per-service log files and the project-level ".stopped"
//     marker used to opt a project out of daemon autostart. Persisted across
//     reboots.
type Locations struct {
	Name    string
	Runtime string
	State   string
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
	state, err := stateDir(getenv, home)
	if err != nil {
		return nil, err
	}
	state = filepath.Join(state, AppDir, name)

	runtime := runtimeDir(getenv)
	if runtime == "" {
		runtime = filepath.Join(state, "run")
	} else {
		runtime = filepath.Join(runtime, AppDir, name)
	}

	return &Locations{
		Name:    name,
		Runtime: runtime,
		State:   state,
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

// GetRegisteredConfigPath reads the persisted config-path for the project if it exists.
func GetRegisteredConfigPath(name string) (string, error) {
	locs, err := Resolve(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(locs.State, "config-path")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config-path not found for project %q: %w", name, err)
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", fmt.Errorf("empty config-path for project %q", name)
	}
	return s, nil
}

// DaemonLocations holds the resolved paths for the global daemon (not
// per-project). The daemon socket and pidfile live at the app-level XDG
// runtime dir; the daemon log lives at the app-level XDG state dir.
type DaemonLocations struct {
	Runtime string
	State   string
	Socket  string
	Pidfile string
	LogFile string
}

// ResolveDaemon derives the daemon-level paths using the process environment.
// These are shared across all projects managed by the daemon.
func ResolveDaemon() (*DaemonLocations, error) {
	return resolveDaemon(os.Getenv, homeDir)
}

func resolveDaemon(getenv envGetter, home func() (string, error)) (*DaemonLocations, error) {
	st, err := stateDir(getenv, home)
	if err != nil {
		return nil, err
	}
	st = filepath.Join(st, AppDir)

	rt := runtimeDir(getenv)
	if rt == "" {
		rt = filepath.Join(st, "run")
	} else {
		rt = filepath.Join(rt, AppDir)
	}

	return &DaemonLocations{
		Runtime: rt,
		State:   st,
		Socket:  filepath.Join(rt, "daemon.sock"),
		Pidfile: filepath.Join(rt, "daemon.pid"),
		LogFile: filepath.Join(st, "daemon.log"),
	}, nil
}

// MkdirAll creates the daemon's runtime and state directories.
func (d *DaemonLocations) MkdirAll() error {
	if err := os.MkdirAll(d.Runtime, 0o700); err != nil {
		return fmt.Errorf("create daemon runtime dir: %w", err)
	}
	if err := os.MkdirAll(d.State, 0o755); err != nil {
		return fmt.Errorf("create daemon state dir: %w", err)
	}
	return nil
}
