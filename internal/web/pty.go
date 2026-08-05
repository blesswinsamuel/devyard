package web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type ptySession struct {
	id     string
	cmd    *exec.Cmd
	ptmx   *os.File
	cancel context.CancelFunc
}

type ptyManager struct {
	mu       sync.Mutex
	sessions map[string]*ptySession
}

func newPTYManager() *ptyManager {
	return &ptyManager{
		sessions: make(map[string]*ptySession),
	}
}

func (m *ptyManager) spawn(ctx context.Context, id, dir string, cols, rows uint16, onOutput func(output string), onExit func()) error {
	m.mu.Lock()
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return nil
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
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start: %w", err)
	}

	if cols > 0 && rows > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
	}

	subCtx, cancel := context.WithCancel(ctx)
	sess := &ptySession{
		id:     id,
		cmd:    cmd,
		ptmx:   ptmx,
		cancel: cancel,
	}

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	go func() {
		defer func() {
			m.closeSession(id)
			onExit()
		}()

		buf := make([]byte, 4096)
		for {
			select {
			case <-subCtx.Done():
				return
			default:
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				onOutput(string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()

	return nil
}

func (m *ptyManager) write(id, data string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("pty session %q not found", id)
	}
	_, err := sess.ptmx.Write([]byte(data))
	return err
}

func (m *ptyManager) resize(id string, cols, rows uint16) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("pty session %q not found", id)
	}
	if cols > 0 && rows > 0 {
		return pty.Setsize(sess.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
	}
	return nil
}

func (m *ptyManager) closeSession(id string) {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if ok {
		sess.cancel()
		_ = sess.ptmx.Close()
		if sess.cmd.Process != nil {
			_ = sess.cmd.Process.Kill()
		}
	}
}

func (m *ptyManager) closeAll() {
	m.mu.Lock()
	sessions := make([]*ptySession, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}
	m.sessions = make(map[string]*ptySession)
	m.mu.Unlock()

	for _, sess := range sessions {
		sess.cancel()
		_ = sess.ptmx.Close()
		if sess.cmd.Process != nil {
			_ = sess.cmd.Process.Kill()
		}
	}
}
