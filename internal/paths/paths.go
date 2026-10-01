// Package paths resolves every on-disk location devyard uses from an explicit
// environment, and validates project ids before they are turned into paths.
//
// Layout (XDG):
//
//	$XDG_STATE_HOME/devyard/                 state (persisted)
//	  daemon.log
//	  projects/<id>/project.json              registration + desired state
//	  projects/<id>/procs/<kind>-<name>/      runner status + per-run logs
//	$XDG_RUNTIME_DIR/devyard/                runtime (sockets, pidfile, lock)
//	  daemon.sock daemon.pid daemon.lock
//	  r/<hash>.sock                           per-process runner sockets
//	$XDG_CONFIG_HOME/devyard/config.yml      global config
//
// When XDG_RUNTIME_DIR is unset (macOS), the runtime dir lives under the
// state dir ("run") so periodic /tmp cleanup cannot delete live sockets.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppDir is the per-application segment appended to the XDG base dirs.
const AppDir = "devyard"

// Getenv looks up an environment variable.
type Getenv func(string) string

// Dirs holds the resolved application directories.
type Dirs struct {
	State   string
	Runtime string
	Config  string
}

// ErrInvalidID reports a project id that is not a valid slug.
var ErrInvalidID = errors.New("invalid project id")

// Default resolves Dirs from the process environment.
func Default() (Dirs, error) {
	return Resolve(os.Getenv)
}

// Resolve derives Dirs from getenv.
func Resolve(getenv Getenv) (Dirs, error) {
	home := getenv("HOME")
	stateBase := getenv("XDG_STATE_HOME")
	if stateBase == "" {
		if home == "" {
			return Dirs{}, errors.New("paths: cannot resolve state dir: set $HOME or $XDG_STATE_HOME")
		}
		stateBase = filepath.Join(home, ".local", "state")
	}
	configBase := getenv("XDG_CONFIG_HOME")
	if configBase == "" {
		if home == "" {
			return Dirs{}, errors.New("paths: cannot resolve config dir: set $HOME or $XDG_CONFIG_HOME")
		}
		configBase = filepath.Join(home, ".config")
	}
	state := filepath.Join(stateBase, AppDir)
	runtime := filepath.Join(state, "run")
	if rt := getenv("XDG_RUNTIME_DIR"); rt != "" {
		runtime = filepath.Join(rt, AppDir)
	}
	return Dirs{
		State:   state,
		Runtime: runtime,
		Config:  filepath.Join(configBase, AppDir),
	}, nil
}

// Socket is the daemon's control socket.
func (d Dirs) Socket() string { return filepath.Join(d.Runtime, "daemon.sock") }

// Pidfile holds the pid of the daemon that owns the lock.
func (d Dirs) Pidfile() string { return filepath.Join(d.Runtime, "daemon.pid") }

// Lockfile is flock'ed by the one live daemon.
func (d Dirs) Lockfile() string { return filepath.Join(d.Runtime, "daemon.lock") }

// DaemonLog is the daemon's own log file.
func (d Dirs) DaemonLog() string { return filepath.Join(d.State, "daemon.log") }

// GlobalConfig is the user-level config file.
func (d Dirs) GlobalConfig() string { return filepath.Join(d.Config, "config.yml") }

// ProjectsDir holds one directory per registered project.
func (d Dirs) ProjectsDir() string { return filepath.Join(d.State, "projects") }

// RunnerSocket returns a short, unique socket path for the runner of one
// process. Hashing keeps it under the ~104 byte sun_path limit.
func (d Dirs) RunnerSocket(project, kind, name string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + kind + "\x00" + name))
	return filepath.Join(d.Runtime, "r", hex.EncodeToString(sum[:8])+".sock")
}

// Project returns the directories of one project after validating id.
func (d Dirs) Project(id string) (ProjectDirs, error) {
	if err := ValidateID(id); err != nil {
		return ProjectDirs{}, err
	}
	root := filepath.Join(d.ProjectsDir(), id)
	return ProjectDirs{Root: root}, nil
}

// MkdirAll creates the runtime and state directories.
func (d Dirs) MkdirAll() error {
	if err := os.MkdirAll(filepath.Join(d.Runtime, "r"), 0o700); err != nil {
		return fmt.Errorf("paths: create runtime dir: %w", err)
	}
	if err := os.MkdirAll(d.ProjectsDir(), 0o755); err != nil {
		return fmt.Errorf("paths: create state dir: %w", err)
	}
	return nil
}

// ProjectDirs holds the directories of one project.
type ProjectDirs struct {
	Root string
}

// File is the project's registration file.
func (p ProjectDirs) File() string { return filepath.Join(p.Root, "project.json") }

// Ports is the project's auto-port assignments file.
func (p ProjectDirs) Ports() string { return filepath.Join(p.Root, "ports.json") }

// Proc returns the directory of one supervised process (service or task).
func (p ProjectDirs) Proc(kind, name string) string {
	return filepath.Join(p.Root, "procs", kind+"-"+name)
}

// ValidateID reports whether id is a valid project slug: 1-63 characters of
// lowercase letters, digits, '-' and '_', starting with a letter or digit.
func ValidateID(id string) error {
	if id == "" || len(id) > 63 {
		return fmt.Errorf("%w %q: must be 1-63 characters", ErrInvalidID, id)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '-' || c == '_') && i > 0:
		default:
			return fmt.Errorf("%w %q: use lowercase letters, digits, '-' and '_'", ErrInvalidID, id)
		}
	}
	return nil
}

// Slugify turns an arbitrary project name into a valid id: lowercased, with
// runs of other characters collapsed to '-'. It returns "" when nothing
// usable remains.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			if r == '_' && b.Len() == 0 {
				continue
			}
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
