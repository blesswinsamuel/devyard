package web

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

const (
	maxHistorySize = 512 << 10 // 512KB of replayable output per session
	maxSessions    = 8         // per connection
)

// ptySession is one interactive shell bound to a browser terminal pane.
type ptySession struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File

	mu        sync.Mutex
	history   []byte
	closeOnce sync.Once

	onOutput func(output string)
	onExit   func()
}

type ptyManager struct {
	mu       sync.Mutex
	sessions map[string]*ptySession
}

func newPTYManager() *ptyManager {
	return &ptyManager{sessions: make(map[string]*ptySession)}
}

// get returns the live session with id, if any.
func (m *ptyManager) get(id string) (*ptySession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	return sess, ok
}

// spawn starts a shell in a new PTY rooted at dir and streams its output to
// onOutput until it exits or is closed. If a session with id already exists,
// it updates the output/exit callbacks, resizes if requested, and replays
// buffered session history.
func (m *ptyManager) spawn(dir, id string, cols, rows uint16, onOutput func(output string), onExit func()) error {
	m.mu.Lock()
	if sess, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		if cols > 0 && rows > 0 {
			_ = pty.Setsize(sess.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		}
		sess.mu.Lock()
		sess.onOutput = onOutput
		sess.onExit = onExit
		var hist string
		if len(sess.history) > 0 {
			hist = string(sess.history)
		}
		sess.mu.Unlock()
		if hist != "" && onOutput != nil {
			onOutput(hist)
		}
		return nil
	}
	if len(m.sessions) >= maxSessions {
		m.mu.Unlock()
		return fmt.Errorf("web: too many terminals (max %d)", maxSessions)
	}
	m.mu.Unlock()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/bash"
		}
	}

	cmd := exec.Command(shell)
	cmd.Dir = dir
	// No explicit Setpgid here: creack/pty starts the child as a session
	// leader (setsid), which makes its process group id equal to its own pid
	// — exactly what closeSession's killpg teardown needs. Setting Setpgid
	// additionally fails with EPERM on macOS once the child is already a
	// session leader.
	cmd.Env = terminalEnv()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("web: start shell: %w", err)
	}
	if cols > 0 && rows > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
	}

	sess := &ptySession{id: id, cmd: cmd, ptmx: ptmx, onOutput: onOutput, onExit: onExit}

	m.mu.Lock()
	if existing, dup := m.sessions[id]; dup {
		m.mu.Unlock()
		closeSession(sess)
		// Another concurrent spawn succeeded; attach to existing.
		if cols > 0 && rows > 0 {
			_ = pty.Setsize(existing.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		}
		existing.mu.Lock()
		existing.onOutput = onOutput
		existing.onExit = onExit
		var hist string
		if len(existing.history) > 0 {
			hist = string(existing.history)
		}
		existing.mu.Unlock()
		if hist != "" && onOutput != nil {
			onOutput(hist)
		}
		return nil
	}
	m.sessions[id] = sess
	m.mu.Unlock()

	go func() {
		defer func() {
			m.remove(id)
			closeSession(sess)
			sess.mu.Lock()
			exitCb := sess.onExit
			sess.mu.Unlock()
			if exitCb != nil {
				exitCb()
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf) // unblocked by closeSession closing ptmx
			if n > 0 {
				sess.mu.Lock()
				sess.history = append(sess.history, buf[:n]...)
				if len(sess.history) > maxHistorySize {
					sess.history = sess.history[len(sess.history)-maxHistorySize:]
				}
				cb := sess.onOutput
				sess.mu.Unlock()
				if cb != nil {
					cb(string(buf[:n]))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return nil
}

func (m *ptyManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *ptyManager) write(id, data string) error {
	sess, ok := m.get(id)
	if !ok {
		return fmt.Errorf("web: terminal %q not found", id)
	}
	_, err := sess.ptmx.Write([]byte(data))
	return err
}

func (m *ptyManager) resize(id string, cols, rows uint16) error {
	sess, ok := m.get(id)
	if !ok {
		return fmt.Errorf("web: terminal %q not found", id)
	}
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(sess.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

func (m *ptyManager) closeSession(id string) {
	if sess, ok := m.get(id); ok {
		m.remove(id)
		closeSession(sess)
	}
}

func (m *ptyManager) closeAll() {
	m.mu.Lock()
	sessions := make([]*ptySession, 0, len(m.sessions))
	for id, sess := range m.sessions {
		sessions = append(sessions, sess)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, sess := range sessions {
		closeSession(sess)
	}
}

// closeSession tears down one session exactly once, no matter which path
// gets here first (shell exit or connection close): it closes the PTY master
// (SIGHUPs the foreground job), kills the whole process group so
// background jobs don't leak, and reaps the shell.
func closeSession(sess *ptySession) {
	sess.closeOnce.Do(func() {
		_ = sess.ptmx.Close()
		if sess.cmd.Process != nil && sess.cmd.Process.Pid > 0 {
			// The child leads its own process group via setsid; signal the
			// group, not just the shell pid.
			_ = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = sess.cmd.Wait()
	})
}

// terminalEnv builds the environment for spawned shells.
func terminalEnv() []string {
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}
	envMap["TERM"] = "xterm-256color"
	envMap["COLORTERM"] = "truecolor"
	if lang, ok := envMap["LANG"]; !ok || lang == "" || lang == "C" || lang == "POSIX" {
		envMap["LANG"] = "en_US.UTF-8"
	}
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}
