//go:build unix

// Package runner supervises exactly one process run on behalf of the daemon.
//
// Every service and task run is launched as a separate `devyard --runner`
// process (a new session, detached from the daemon). The runner:
//
//   - optionally runs a build command first,
//   - starts the child in its own process group (or, for TTY processes, its
//     own session with a pseudo-terminal),
//   - owns the child's stdio: output goes to a per-run logstore file and to
//     attached clients; input comes from attached clients,
//   - enforces stop semantics (SIGTERM, then SIGKILL after a grace period),
//   - records the final exit status in a status file,
//   - serves a small control protocol on a Unix socket.
//
// Because the runner, not the daemon, holds the child's pipes and PTY, the
// daemon can exit, crash or restart without affecting supervised processes:
// a new daemon simply reconnects to the runner sockets ("adoption").
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Flag is the hidden command-line flag that starts a runner process.
const Flag = "--runner"

// Phases of a run.
const (
	PhaseStarting = "starting"
	PhaseBuilding = "building"
	PhaseRunning  = "running"
	PhaseExited   = "exited"
)

// Spec describes one run. It is passed to the runner on stdin.
type Spec struct {
	Project string `json:"project"`
	Kind    string `json:"kind"` // "service" | "task"
	Name    string `json:"name"`
	Run     int64  `json:"run"`

	Command string   `json:"command"`
	Shell   string   `json:"shell"`
	Dir     string   `json:"dir"`
	Env     []string `json:"env"`
	TTY     bool     `json:"tty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`

	// Build, when set, runs to completion before Command. A failing build
	// ends the run without starting Command.
	Build *BuildSpec `json:"build,omitempty"`

	// ProcDir is the process's directory: logs (logstore) and status.json.
	ProcDir string `json:"proc_dir"`
	// Socket is the runner's control socket path.
	Socket string `json:"socket"`
	// StopGrace is how long Stop waits after SIGTERM before SIGKILL.
	StopGrace time.Duration `json:"stop_grace"`
	// Ephemeral runs (interactive terminals) keep no log files; output is
	// only available to attached clients and the replay buffer.
	Ephemeral bool `json:"ephemeral,omitempty"`
	// Hash identifies the definition the run was started from; it is
	// echoed in Status so an adopting daemon can detect config changes.
	Hash string `json:"hash,omitempty"`
}

// BuildSpec is a pre-start build command.
type BuildSpec struct {
	Command string   `json:"command"`
	Shell   string   `json:"shell"`
	Dir     string   `json:"dir"`
	Env     []string `json:"env"`
}

// Status is the runner's view of the run. It is written atomically to
// <ProcDir>/status.json by the runner (its only writer) and returned by the
// control protocol.
type Status struct {
	Project   string `json:"project"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Run       int64  `json:"run"`
	RunnerPID int    `json:"runner_pid"`
	Socket    string `json:"socket"`
	TTY       bool   `json:"tty"`
	Hash      string `json:"hash,omitempty"`

	Phase string `json:"phase"`
	PID   int    `json:"pid,omitempty"`
	PGID  int    `json:"pgid,omitempty"`

	ExitCode    int    `json:"exit_code"`
	Signal      string `json:"signal,omitempty"`
	BuildFailed bool   `json:"build_failed,omitempty"`
	// Stopped is true when the run ended because of a Stop request.
	Stopped bool `json:"stopped,omitempty"`
	// Lost is set by readers (never by the runner) when the runner died
	// without recording an exit status.
	Lost  bool   `json:"lost,omitempty"`
	Error string `json:"error,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Exited reports whether the run has finished.
func (s Status) Exited() bool { return s.Phase == PhaseExited }

// StatusPath returns the status file of a process directory.
func StatusPath(procDir string) string { return filepath.Join(procDir, "status.json") }

// ReadStatus reads a status file.
func ReadStatus(procDir string) (Status, error) {
	data, err := os.ReadFile(StatusPath(procDir))
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(data, &st); err != nil {
		return Status{}, fmt.Errorf("runner: parse status: %w", err)
	}
	return st, nil
}

func writeStatus(procDir string, st Status) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatusPath(procDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, StatusPath(procDir))
}

// Alive reports whether pid names a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

var signalNames = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT, "ALRM": syscall.SIGALRM, "BUS": syscall.SIGBUS,
	"CHLD": syscall.SIGCHLD, "CONT": syscall.SIGCONT, "FPE": syscall.SIGFPE,
	"HUP": syscall.SIGHUP, "ILL": syscall.SIGILL, "INT": syscall.SIGINT,
	"KILL": syscall.SIGKILL, "PIPE": syscall.SIGPIPE, "QUIT": syscall.SIGQUIT,
	"SEGV": syscall.SIGSEGV, "STOP": syscall.SIGSTOP, "TERM": syscall.SIGTERM,
	"TRAP": syscall.SIGTRAP, "TSTP": syscall.SIGTSTP, "TTIN": syscall.SIGTTIN,
	"TTOU": syscall.SIGTTOU, "URG": syscall.SIGURG, "USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2, "WINCH": syscall.SIGWINCH,
}

// ParseSignal converts "SIGTERM", "TERM" or "15" to a signal. The empty
// string means SIGKILL.
func ParseSignal(name string) (syscall.Signal, error) {
	if name == "" {
		return syscall.SIGKILL, nil
	}
	upper := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "SIG")
	if sig, ok := signalNames[upper]; ok {
		return sig, nil
	}
	if n, err := strconv.Atoi(upper); err == nil && n >= 1 && n <= 31 {
		return syscall.Signal(n), nil
	}
	return 0, fmt.Errorf("unknown signal %q (expected e.g. SIGTERM, SIGKILL or a number)", name)
}

// SignalName returns the conventional name of sig ("SIGTERM").
func SignalName(sig syscall.Signal) string {
	for name, s := range signalNames {
		if s == sig {
			return "SIG" + name
		}
	}
	return "signal " + strconv.Itoa(int(sig))
}
