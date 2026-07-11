//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/blesswinsamuel/local-compose/internal/project"
)

// DaemonFlag is the hidden flag the global daemon child is invoked with. The
// CLI root command handles it by running the global daemon (orchestrator)
// instead of dispatching a normal subcommand.
const DaemonFlag = "--daemon"

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

// SpawnDaemon re-execs the current binary as a daemonized global daemon: a
// new session leader (setsid) detached from the controlling terminal, with
// stdio repointed at the daemon log file. It writes the child's pidfile and
// returns the child's pid.
func SpawnDaemon(locs *project.DaemonLocations) (int, error) {
	if locs == nil {
		return 0, errors.New("daemon: DaemonLocations is required")
	}

	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: resolve executable: %w", err)
	}

	if err := locs.MkdirAll(); err != nil {
		return 0, err
	}

	logFile, err := os.OpenFile(locs.LogFile,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("daemon: open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(self, DaemonFlag)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: start daemon: %w", err)
	}
	pid := cmd.Process.Pid

	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("daemon: release daemon: %w", err)
	}

	if err := writePidfile(locs.Pidfile, pid); err != nil {
		return pid, err
	}
	return pid, nil
}

// DaemonRunning returns the pid of an existing global daemon, or 0 if none is
// running. A stale pidfile (dead pid) is treated as "not running".
func DaemonRunning(locs *project.DaemonLocations) (int, error) {
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

// RemoveDaemonPidfile removes the daemon pidfile, ignoring "not exist" errors.
func RemoveDaemonPidfile(locs *project.DaemonLocations) error {
	err := os.Remove(locs.Pidfile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
