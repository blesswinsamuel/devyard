// Package sessions opens interactive sessions: project terminals and
// attachments to running tasks and TTY services.
//
// Terminals run under runners exactly like services, so a terminal survives
// browser reloads, websocket drops and daemon restarts; clients reattach by
// session id and get the recent output replayed. Output is not persisted.
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// Target kinds.
const (
	KindTerminal = "terminal"
	KindTask     = "task"
	KindService  = "service"
)

// maxTerminals bounds live terminals per daemon.
const maxTerminals = 64

// ErrSessionNotFound is returned when reattaching to an unknown or ended
// terminal.
var ErrSessionNotFound = errors.New("session not found")

var sessionIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

// Target identifies what to attach to.
type Target struct {
	Kind      string
	Project   string
	Name      string
	SessionID string
}

// Session is an open interactive attachment.
type Session struct {
	ID    string
	TTY   bool
	Stdin bool
	att   *runner.Attachment
	kill  func(context.Context) error
}

// Read returns the next output chunk. When the process exits it returns a
// *runner.ExitError.
func (s *Session) Read() ([]byte, error) { return s.att.Read() }

// Write sends input.
func (s *Session) Write(p []byte) error {
	_, err := s.att.Write(p)
	return err
}

// Resize changes the terminal size.
func (s *Session) Resize(cols, rows int) error { return s.att.Resize(cols, rows) }

// CloseStdin sends EOF to a non-TTY process.
func (s *Session) CloseStdin() error { return s.att.CloseStdin() }

// Detach closes the attachment; the process keeps running.
func (s *Session) Detach() error { return s.att.Close() }

// Kill ends a terminal session (not supported for tasks and services, which
// are stopped through their own commands).
func (s *Session) Kill(ctx context.Context) error {
	if s.kill == nil {
		return fmt.Errorf("%w: only terminal sessions can be closed", engine.ErrInvalid)
	}
	return s.kill(ctx)
}

// Manager opens sessions.
type Manager struct {
	dirs    paths.Dirs
	mgr     *engine.Manager
	launch  runner.LaunchOptions
	mu      sync.Mutex
	started map[string]bool
}

// New returns a session manager and cleans up terminals left behind by a
// previous daemon (live ones stay attachable).
func New(dirs paths.Dirs, mgr *engine.Manager, launch runner.LaunchOptions) *Manager {
	m := &Manager{dirs: dirs, mgr: mgr, launch: launch, started: map[string]bool{}}
	m.adopt()
	return m
}

func (m *Manager) terminalsDir() string { return filepath.Join(m.dirs.State, "terminals") }

func (m *Manager) terminalDir(id string) string { return filepath.Join(m.terminalsDir(), id) }

func (m *Manager) adopt() {
	entries, _ := os.ReadDir(m.terminalsDir())
	for _, e := range entries {
		dir := filepath.Join(m.terminalsDir(), e.Name())
		st, err := runner.ReadStatus(dir)
		if err != nil || st.Exited() || !runner.Alive(st.RunnerPID) {
			_ = os.RemoveAll(dir)
			continue
		}
		m.reap(e.Name())
	}
}

// reap removes a terminal's directory once it exits.
func (m *Manager) reap(id string) {
	m.mu.Lock()
	if m.started[id] {
		m.mu.Unlock()
		return
	}
	m.started[id] = true
	m.mu.Unlock()
	dir := m.terminalDir(id)
	go func() {
		p, _, err := runner.Open(dir)
		if err == nil {
			_, _ = p.Wait(context.Background())
		}
		_ = os.RemoveAll(dir)
		m.mu.Lock()
		delete(m.started, id)
		m.mu.Unlock()
	}()
}

// StopAll ends every terminal (on `daemon stop`).
func (m *Manager) StopAll(ctx context.Context) {
	entries, _ := os.ReadDir(m.terminalsDir())
	var wg sync.WaitGroup
	for _, e := range entries {
		p, st, err := runner.Open(m.terminalDir(e.Name()))
		if err != nil || st.Exited() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Stop(ctx, time.Second)
		}()
	}
	wg.Wait()
}

// Open opens a session for target.
func (m *Manager) Open(ctx context.Context, t Target, cols, rows int) (*Session, error) {
	switch t.Kind {
	case KindTerminal:
		if t.SessionID != "" {
			return m.reattach(ctx, t.SessionID, cols, rows)
		}
		return m.newTerminal(ctx, t.Project, cols, rows)
	case KindTask, KindService:
		p, err := m.mgr.Get(t.Project)
		if err != nil {
			return nil, err
		}
		var att *runner.Attachment
		if t.Kind == KindTask {
			task, err := p.Task(t.Name)
			if err != nil {
				return nil, err
			}
			att, err = task.Attach(ctx, cols, rows)
			if err != nil {
				return nil, err
			}
		} else {
			svc, err := p.Service(t.Name)
			if err != nil {
				return nil, err
			}
			att, err = svc.Attach(ctx, cols, rows)
			if err != nil {
				return nil, err
			}
		}
		return &Session{TTY: att.TTY, Stdin: att.Stdin, att: att}, nil
	}
	return nil, fmt.Errorf("%w: unknown session kind %q", engine.ErrInvalid, t.Kind)
}

func (m *Manager) reattach(ctx context.Context, id string, cols, rows int) (*Session, error) {
	if !sessionIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	p, st, err := runner.Open(m.terminalDir(id))
	if err != nil || st.Exited() {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	att, err := p.Attach(ctx, cols, rows)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	return &Session{ID: id, TTY: true, Stdin: true, att: att, kill: func(ctx context.Context) error {
		_, err := p.Stop(ctx, time.Second)
		return err
	}}, nil
}

func (m *Manager) newTerminal(ctx context.Context, project string, cols, rows int) (*Session, error) {
	p, err := m.mgr.Get(project)
	if err != nil {
		return nil, err
	}
	v := p.View()
	m.mu.Lock()
	n := len(m.started)
	m.mu.Unlock()
	if n >= maxTerminals {
		return nil, fmt.Errorf("%w: too many terminals (max %d)", engine.ErrInvalid, maxTerminals)
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(idBytes)
	dir := m.terminalDir(id)
	env := v.TerminalEnv
	shell := "/bin/sh"
	for _, kv := range env {
		if len(kv) > 6 && kv[:6] == "SHELL=" && kv[6:] != "" {
			shell = kv[6:]
		}
	}
	spec := runner.Spec{
		Project:   project,
		Kind:      KindTerminal,
		Name:      id,
		Run:       1,
		Command:   "exec " + shellQuote(shell) + " -i",
		Shell:     "/bin/sh",
		Dir:       filepath.Dir(v.Reg.ConfigPath),
		Env:       env,
		TTY:       true,
		Cols:      cols,
		Rows:      rows,
		ProcDir:   dir,
		Socket:    m.dirs.RunnerSocket(project, KindTerminal, id),
		StopGrace: time.Second,
		Ephemeral: true,
	}
	proc, err := runner.Launch(ctx, spec, m.launch)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	m.reap(id)
	att, err := proc.Attach(ctx, cols, rows)
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, TTY: true, Stdin: true, att: att, kill: func(ctx context.Context) error {
		_, err := proc.Stop(ctx, time.Second)
		return err
	}}, nil
}

func shellQuote(s string) string {
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
		} else {
			out += string(r)
		}
	}
	return out + "'"
}
