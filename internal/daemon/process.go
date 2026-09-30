//go:build unix

// Package daemon runs the devyard daemon: it owns the project manager, the
// control socket, the web dashboard and the reverse proxy.
//
// Exactly one daemon runs per user (flock on the runtime lock file). The
// pidfile is written by the daemon that holds the lock, never by the process
// that spawned it, so it always names the live daemon. A restart hands over
// by releasing every listener and the lock before the replacement starts, so
// two daemons never overlap.
package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blesswinsamuel/devyard/internal/paths"
)

// Flag is the hidden command-line flag that runs the daemon.
const Flag = "--daemon"

// ErrLocked is returned when another daemon holds the lock.
var ErrLocked = errors.New("daemon: another daemon is running")

// lock acquires the daemon lock, waiting up to wait for a previous daemon to
// release it.
func lock(dirs paths.Dirs, wait time.Duration) (*os.File, error) {
	if err := dirs.MkdirAll(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dirs.Lockfile(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("daemon: lock: %w", err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLocked
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ReadPid returns the pid recorded by the live daemon (0 when none).
func ReadPid(dirs paths.Dirs) int {
	data, err := os.ReadFile(dirs.Pidfile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func writePid(dirs paths.Dirs) error {
	tmp := dirs.Pidfile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dirs.Pidfile())
}

func removePidIfOurs(dirs paths.Dirs) {
	if ReadPid(dirs) == os.Getpid() {
		_ = os.Remove(dirs.Pidfile())
	}
}

// Spawn starts a detached daemon process (new session, stdio to the daemon
// log) and returns its pid. The caller should then wait for the daemon's
// control socket to answer.
func Spawn(dirs paths.Dirs, env []string) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: resolve executable: %w", err)
	}
	if err := dirs.MkdirAll(); err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(dirs.DaemonLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("daemon: open log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	cmd := exec.Command(self, Flag)
	cmd.Dir = "/"
	cmd.Env = env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: start: %w", err)
	}
	pid := cmd.Process.Pid
	// Reap the daemon if it exits while this process is still alive (e.g.
	// startup failure); otherwise it is reparented when we exit.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// Alive reports whether pid is a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// LastError extracts the most recent error line from the daemon log, for
// reporting startup failures to the CLI.
func LastError(dirs paths.Dirs) string {
	data, err := os.ReadFile(dirs.DaemonLog())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-50; i-- {
		line := lines[i]
		if idx := strings.Index(line, `error="`); idx >= 0 && strings.Contains(line, "level=ERROR") {
			msg := line[idx+len(`error="`):]
			if end := strings.LastIndex(msg, `"`); end >= 0 {
				msg = msg[:end]
			}
			return msg
		}
	}
	return ""
}
