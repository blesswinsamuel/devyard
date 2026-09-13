//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/project"
)

// DaemonFlag is the hidden flag the global daemon child is invoked with. The
// CLI root command handles it by running the global daemon (orchestrator)
// instead of dispatching a normal subcommand.
const DaemonFlag = "--daemon"

// WritePidfile writes pid (followed by a newline) to path with 0o644 perms.
func WritePidfile(path string, pid int) error {
	return writePidfile(path, pid)
}

// writePidfile writes pid (followed by a newline) to path with 0o644 perms.
func writePidfile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// LockFilePath returns the path to the daemon lock file.
func LockFilePath(locs *project.DaemonLocations) string {
	return filepath.Join(locs.Runtime, "daemon.lock")
}

// lockAcquireTimeout bounds how long LockDaemon waits for the previous daemon
// to release its lock. It must comfortably exceed DefaultGracefulStopTimeout
// (20s) so a replacement daemon never gives up while the old one is still
// gracefully stopping services.
const lockAcquireTimeout = 30 * time.Second

// LockDaemon attempts to acquire an exclusive non-blocking flock on the daemon lock file.
// It returns the open *os.File holding the lock, which must remain open for the duration
// of the daemon process.
func LockDaemon(locs *project.DaemonLocations) (*os.File, error) {
	if locs == nil {
		return nil, errors.New("daemon: DaemonLocations is required")
	}
	if err := locs.MkdirAll(); err != nil {
		return nil, err
	}
	lockPath := LockFilePath(locs)
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock file: %w", err)
	}

	deadline := time.Now().Add(lockAcquireTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errors.New("another daemon process is already running")
		}
		return nil, fmt.Errorf("daemon: flock lock file: %w", err)
	}
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

// IsSocketResponsive checks if a Unix domain socket at path is actively accepting connections.
func IsSocketResponsive(path string, timeout time.Duration) bool {
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// DaemonRunning returns the pid of an existing global daemon, or 0 if none is
// running. A stale pidfile (dead pid or unreachable socket) is treated as "not running".
func DaemonRunning(locs *project.DaemonLocations) (int, error) {
	if locs == nil {
		return 0, nil
	}
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
	if locs.Socket != "" && !IsSocketResponsive(locs.Socket, 200*time.Millisecond) {
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

// RemovePidfileIfOurs removes the pidfile only when it still contains our own
// pid. During a daemon restart the replacement daemon may have already
// written its own pid to the pidfile by the time the old daemon exits; the
// old daemon must not delete it.
func RemovePidfileIfOurs(locs *project.DaemonLocations, pid int) error {
	if pid <= 0 {
		return nil
	}
	stored, err := ReadPidfile(locs.Pidfile)
	if err != nil {
		return nil
	}
	if stored != pid {
		return nil
	}
	return os.Remove(locs.Pidfile)
}
