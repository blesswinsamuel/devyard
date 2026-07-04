//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/blesswinsamuel/local-compose/internal/project"
)

// SupervisorFlag is the hidden flag the daemonized child is invoked with. The
// CLI root command handles it by running the foreground supervisor instead of
// dispatching a normal subcommand. It is intentionally a bare flag (not
// registered with cobra) so it never shows up in help/completions.
const SupervisorFlag = "--supervisor"

// Options configures a daemon Spawn.
type Options struct {
	// Locations are the resolved runtime/state dirs. The pidfile is written to
	// Locations.Pidfile and the child's stdio is wired to a supervisor log
	// file under Locations.State.
	Locations *project.Locations

	// Project is the project name passed as --supervisor <project>. The child
	// uses it to re-resolve the same runtime/state dirs.
	Project string

	// ConfigPath, if non-empty, is forwarded to the child as -f <path> so a
	// non-default config file is honored by the daemonized supervisor.
	ConfigPath string

	// ExtraArgs are appended verbatim after the supervisor flag and project.
	// Reserved for future flags (e.g. --build).
	ExtraArgs []string
}

// Spawn re-execs the current binary as a daemonized supervisor: a new session
// leader (setsid) detached from the controlling terminal, with stdio repointed
// at the supervisor log file. It writes the child's pidfile and returns the
// child's pid. The parent is then free to exit (and typically does — `up -d`
// returns 0 right after). The child owns all supervised processes and the
// control socket.
//
// Go has no fork(2) binding, so this uses the standard re-exec-then-setsid
// idiom: exec.Command with SysProcAttr{Setsid: true} makes the child a session
// leader in one shot; when the parent exits the child is reparented to PID 1
// and survives. No double-fork is needed because setsid + releasing the child
// from the parent's wait set is sufficient on modern unices.
func Spawn(opts Options) (int, error) {
	if opts.Locations == nil {
		return 0, errors.New("daemon: Locations is required")
	}
	if strings.TrimSpace(opts.Project) == "" {
		return 0, errors.New("daemon: Project is required")
	}

	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: resolve executable: %w", err)
	}

	if err := opts.Locations.MkdirAll(); err != nil {
		return 0, err
	}

	logFile, err := os.OpenFile(supervisorLogPath(opts.Locations),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("daemon: open supervisor log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(self, buildChildArgs(opts)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Detach from the terminal: stdin from /dev/null, stdout/stderr into the
	// supervisor log so the daemon's own output is recoverable.
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Inherit the environment so XDG dirs, PATH, and user config survive re-exec.
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: start supervisor: %w", err)
	}
	pid := cmd.Process.Pid

	// Release the child so the parent doesn't keep it in its wait set; the
	// child survives parent exit because setsid made it a session leader.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("daemon: release supervisor: %w", err)
	}

	if err := writePidfile(opts.Locations.Pidfile, pid); err != nil {
		return pid, err
	}
	return pid, nil
}

// buildChildArgs assembles the argv for the re-exec'd supervisor.
func buildChildArgs(opts Options) []string {
	args := make([]string, 0, 4+len(opts.ExtraArgs))
	args = append(args, SupervisorFlag, opts.Project)
	if opts.ConfigPath != "" {
		args = append(args, "-f", opts.ConfigPath)
	}
	args = append(args, opts.ExtraArgs...)
	return args
}

// supervisorLogPath is where the daemon's own stdout/stderr land (separate
// from per-service logs, which live under Locations.LogsDir).
func supervisorLogPath(l *project.Locations) string {
	return filepath.Join(l.State, "supervisor.log")
}

// writePidfile writes pid (followed by a newline) to path with 0o644 perms.
func writePidfile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// ReadPidfile reads and parses the pidfile at path. It returns the pid and an
// error if the file is missing or malformed.
func ReadPidfile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, errors.New("daemon: empty pidfile")
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("daemon: parse pidfile %q: %w", s, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("daemon: invalid pid %d", pid)
	}
	return pid, nil
}

// IsAlive reports whether pid names a running process. A pid is considered
// alive if kill(pid, 0) succeeds or fails with EPERM (the process exists but
// belongs to another user). ESRCH (no such process) means dead.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// Running returns the pid of an existing supervisor for the project, or 0 if
// none is running. A stale pidfile (dead pid) is treated as "not running"; the
// caller may remove it via RemovePidfile.
func Running(locs *project.Locations) (int, error) {
	pid, err := ReadPidfile(locs.Pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !IsAlive(pid) {
		return 0, nil
	}
	return pid, nil
}

// RemovePidfile removes the pidfile, ignoring "not exist" errors.
func RemovePidfile(locs *project.Locations) error {
	err := os.Remove(locs.Pidfile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
