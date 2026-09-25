//go:build unix

package shim

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Flag is the command-line flag identifying a shim child process.
const Flag = "--shim"

// LogTimestampFormat is RFC3339 with nanosecond precision in UTC.
const LogTimestampFormat = "2006-01-02T15:04:05.000000000Z"

// Config is the configuration passed to a shim process.
type Config struct {
	Project    string   `json:"project"`
	Service    string   `json:"service"`
	Command    string   `json:"command"`
	Shell      string   `json:"shell,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	Env        []string `json:"env,omitempty"`
	LogPath    string   `json:"log_path"`
	StatePath  string   `json:"state_path"`
	TTY        bool     `json:"tty,omitempty"`
}

// State is the runtime state persisted by the shim to StatePath.
type State struct {
	Project    string    `json:"project"`
	Service    string    `json:"service"`
	ShimPID    int       `json:"shim_pid"`
	ChildPID   int       `json:"child_pid"`
	PGID       int       `json:"pgid"`
	Status     string    `json:"status"` // "running", "exited"
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Save atomic-writes State to path.
func (s *State) Save(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState reads State from path.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Run executes the shim logic for the given config.
func Run(cfg *Config) error {
	if cfg == nil {
		return errors.New("shim: config is required")
	}

	shell := cfg.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell, "-c", cfg.Command)
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755); err != nil {
		return fmt.Errorf("shim: mkdir logs: %w", err)
	}

	logFile, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("shim: open log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	var logMu sync.Mutex
	writeLine := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		ts := time.Now().UTC().Format(LogTimestampFormat)
		record := ts + " " + line + "\n"
		_, _ = logFile.WriteString(record)
	}

	writeLine("$ " + cfg.Command)

	var ptyMaster *os.File
	var stdoutPipe, stderrPipe io.ReadCloser
	var pipeWG sync.WaitGroup

	if cfg.TTY {
		ptym, err := pty.Start(cmd)
		if err != nil {
			return fmt.Errorf("shim: start pty: %w", err)
		}
		ptyMaster = ptym
		defer func() { _ = ptyMaster.Close() }()

		pipeWG.Add(1)
		go func() {
			defer pipeWG.Done()
			readLines(ptyMaster, writeLine)
		}()
	} else {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("shim: stdout pipe: %w", err)
		}
		stdoutPipe = stdout

		stderr, err := cmd.StderrPipe()
		if err != nil {
			_ = stdoutPipe.Close()
			return fmt.Errorf("shim: stderr pipe: %w", err)
		}
		stderrPipe = stderr

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("shim: start command: %w", err)
		}

		pipeWG.Add(2)
		go func() {
			defer pipeWG.Done()
			readLines(stdoutPipe, writeLine)
		}()
		go func() {
			defer pipeWG.Done()
			readLines(stderrPipe, writeLine)
		}()
	}

	childPid := cmd.Process.Pid
	pgid := childPid
	now := time.Now()

	state := &State{
		Project:   cfg.Project,
		Service:   cfg.Service,
		ShimPID:   os.Getpid(),
		ChildPID:  childPid,
		PGID:      pgid,
		Status:    "running",
		StartedAt: now,
	}
	_ = state.Save(cfg.StatePath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	doneCh := make(chan error, 1)
	go func() {
		pipeWG.Wait()
		doneCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case sig := <-sigCh:
		// Forward signal to child process group
		if pgid > 0 {
			_ = syscall.Kill(-pgid, sig.(syscall.Signal))
		}
		select {
		case waitErr = <-doneCh:
		case <-time.After(10 * time.Second):
			if pgid > 0 {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
			waitErr = <-doneCh
		}
	case waitErr = <-doneCh:
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		writeLine(fmt.Sprintf("devyard: exited with exit code %d", exitCode))
	}

	state.Status = "exited"
	state.ChildPID = 0
	state.PGID = 0
	state.ExitCode = exitCode
	state.FinishedAt = time.Now()
	_ = state.Save(cfg.StatePath)

	return nil
}

func readLines(r io.Reader, fn func(string)) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			fn(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return
		}
	}
}
