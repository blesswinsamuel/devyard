package harness

import (
	"errors"
	"path/filepath"
)

// dirs mirrors internal/paths.Resolve (the harness must not import internal
// packages). If the backend's layout changes, update this too; the socket
// guard depends on it.
type dirs struct {
	State   string
	Runtime string
	Config  string
}

func resolveDirs(getenv func(string) string) (dirs, error) {
	home := getenv("HOME")
	stateBase := getenv("XDG_STATE_HOME")
	if stateBase == "" {
		if home == "" {
			return dirs{}, errors.New("cannot resolve state dir")
		}
		stateBase = filepath.Join(home, ".local", "state")
	}
	configBase := getenv("XDG_CONFIG_HOME")
	if configBase == "" {
		if home == "" {
			return dirs{}, errors.New("cannot resolve config dir")
		}
		configBase = filepath.Join(home, ".config")
	}
	state := filepath.Join(stateBase, "devyard")
	runtime := filepath.Join(state, "run")
	if rt := getenv("XDG_RUNTIME_DIR"); rt != "" {
		runtime = filepath.Join(rt, "devyard")
	}
	return dirs{State: state, Runtime: runtime, Config: filepath.Join(configBase, "devyard")}, nil
}

func (d dirs) socket() string       { return filepath.Join(d.Runtime, "daemon.sock") }
func (d dirs) pidfile() string      { return filepath.Join(d.Runtime, "daemon.pid") }
func (d dirs) daemonLog() string    { return filepath.Join(d.State, "daemon.log") }
func (d dirs) globalConfig() string { return filepath.Join(d.Config, "config.yml") }
func (d dirs) projectsDir() string  { return filepath.Join(d.State, "projects") }

func envLookup(env []string) func(string) string {
	return func(k string) string {
		v := ""
		for _, kv := range env {
			if len(kv) > len(k) && kv[len(k)] == '=' && kv[:len(k)] == k {
				v = kv[len(k)+1:]
			}
		}
		return v
	}
}
