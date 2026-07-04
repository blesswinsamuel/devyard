// Package tui implements the Bubble Tea interactive frontend.
//
// The TUI is a thin client over the same Unix-socket control protocol used by
// the CLI: it dials the supervisor, polls List snapshots on a timer, and
// follows the selected service's log stream over a long-lived Logs{follow}
// connection. Restart/stop keybindings send one-shot requests. If no
// supervisor is running it offers to start one detached (`up -d`) and then
// attaches. Quitting only detaches — the supervisor keeps running.
package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/ui"
)

// Program is the Bubble Tea program type used by the TUI frontend.
type Program = tea.Program

const (
	pollInterval    = 1 * time.Second
	listPaneWidth   = 30
	helpBarHeight   = 1
	maxLogLineCount = 5000
)

// pane is the currently focused split.
type pane int

const (
	paneList pane = iota
	paneLogs
)

// Options configures a TUI run. Socket is the supervisor's control socket;
// Locs/Project/ConfigPath are only used when the TUI offers to start a
// detached supervisor because none is running.
type Options struct {
	Socket     string
	Locs       *project.Locations
	Project    string
	ConfigPath string
}

// New constructs the Bubble Tea program for the TUI. The returned program has
// not been started; call Run().
func New(opts Options) *tea.Program {
	m := model{
		socket:     opts.Socket,
		locs:       opts.Locs,
		project:    opts.Project,
		configPath: opts.ConfigPath,
		pane:       paneList,
	}
	// Bubble Tea copies the model passed to NewProgram, so a *tea.Program
	// assigned to a field after NewProgram never reaches the program's internal
	// copy (that was the nil-deref panic in pumpLogs). A closure is a reference
	// type, though: it's copied by value but still closes over the same `p`
	// variable, which is assigned below. By the time Update invokes the closure
	// `p` holds the real program, so startFollowCmd gets a non-nil *tea.Program.
	var p *tea.Program
	m.startFollow = func(socket, service string, gen int64) tea.Cmd {
		return startFollowCmd(socket, service, gen, p)
	}
	p = tea.NewProgram(m)
	return p
}

type model struct {
	socket     string
	locs       *project.Locations
	project    string
	configPath string

	// startFollow builds a startFollowCmd bound to the running *tea.Program.
	// See New for why this is a closure rather than a stored *tea.Program.
	startFollow func(socket, service string, gen int64) tea.Cmd

	width, height int
	ready         bool

	states   []protocol.ServiceState
	selected int

	pane     pane
	viewport viewport.Model

	// follow is the active log-follow connection. It is nil when no service
	// is being tailed. followGen increments every time a new follow is
	// requested so stale messages from a previous goroutine are ignored.
	follow    *control.Client
	followGen int64

	logLines []string

	status     string
	statusKind statusKind

	noSupervisor bool
	quitting     bool
}

type statusKind int

const (
	statusInfo statusKind = iota
	statusErr
)

// --- messages ---

type listTickMsg struct{}

type statesMsg struct {
	states []protocol.ServiceState
	err    error
}

type logLineMsg struct {
	gen     int64
	service string
	line    string
}

type logDoneMsg struct {
	gen     int64
	service string
	err     error
}

type followStartedMsg struct {
	gen     int64
	service string
	client  *control.Client
}

type actionResultMsg struct {
	action  string
	service string
	err     error
}

type startedMsg struct {
	pid int
	err error
}

// --- Init / Update ---

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(pollInterval, func(time.Time) tea.Msg { return listTickMsg{} }),
		connectCmd(m.socket),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layoutViewport()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case listTickMsg:
		return m, tea.Batch(
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return listTickMsg{} }),
			connectCmd(m.socket),
		)

	case statesMsg:
		return m.handleStates(msg)

	case followStartedMsg:
		if msg.gen != m.followGen {
			// Stale: a newer selection superseded this follow. Close it.
			_ = msg.client.Close()
			return m, nil
		}
		m.follow = msg.client
		return m, nil

	case logLineMsg:
		if msg.gen != m.followGen || msg.service != m.selectedName() {
			return m, nil
		}
		m.appendLog(msg.line)
		return m, nil

	case logDoneMsg:
		if msg.gen != m.followGen || (msg.service != "" && msg.service != m.selectedName()) {
			return m, nil
		}
		m.follow = nil
		if msg.err != nil && !m.quitting {
			m.setStatus("logs: "+msg.err.Error(), statusErr)
			// Reconnect shortly so a supervisor restart/down-then-up is
			// picked up automatically.
			return m, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
				return retryFollowMsg{}
			})
		}
		return m, nil

	case retryFollowMsg:
		if m.quitting || m.noSupervisor {
			return m, nil
		}
		if m.follow != nil {
			return m, nil
		}
		return m, m.startFollowSelected()

	case actionResultMsg:
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("%s %s: %v", msg.action, msg.service, msg.err), statusErr)
		} else {
			if msg.service != "" {
				m.setStatus(fmt.Sprintf("%s %s: ok", msg.action, msg.service), statusInfo)
			} else {
				m.setStatus(fmt.Sprintf("%s: ok", msg.action), statusInfo)
			}
		}
		// Refresh the snapshot immediately so the UI reflects the action.
		return m, connectCmd(m.socket)

	case startedMsg:
		if msg.err != nil {
			m.setStatus("start supervisor: "+msg.err.Error(), statusErr)
			m.noSupervisor = true
			return m, nil
		}
		m.noSupervisor = false
		m.setStatus(fmt.Sprintf("supervisor started (pid %d)", msg.pid), statusInfo)
		return m, tea.Batch(
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return listTickMsg{} }),
			connectCmd(m.socket),
		)
	}
	return m, nil
}

type retryFollowMsg struct{}

// handleKey dispatches key presses. Selection keys are live in the list pane,
// scroll keys in the logs pane. Global keys (r/s/d/q/Tab) work from either.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.noSupervisor {
		return m.handleStartPrompt(msg)
	}
	if m.quitting {
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		m.quitting = true
		m.closeFollow()
		return m, tea.Quit

	case "tab":
		if m.pane == paneList {
			m.pane = paneLogs
		} else {
			m.pane = paneList
		}
		return m, nil

	case "r":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("restarting "+name+"...", statusInfo)
		return m, actionCmd(m.socket, "restart", name)

	case "s":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("stopping "+name+"...", statusInfo)
		return m, actionCmd(m.socket, "stop", name)

	case "d":
		m.setStatus("down...", statusInfo)
		m.closeFollow()
		return m, actionCmd(m.socket, "down", "")
	}

	if m.pane == paneList {
		return m.handleListKey(msg)
	}
	return m.handleLogsKey(msg)
}

func (m model) handleStartPrompt(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.noSupervisor = false
		m.setStatus("starting supervisor...", statusInfo)
		return m, startDaemonCmd(m.locs, m.project, m.configPath)
	case "n", "N", "q", "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.states) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.states)-1 {
			m.selected++
		}
	case "home", "g":
		m.selected = 0
	case "end", "G":
		m.selected = len(m.states) - 1
	case "enter", "right", "l":
		m.pane = paneLogs
	default:
		return m, nil
	}
	return m, m.maybeSwitchFollow()
}

func (m model) handleLogsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		m.pane = paneList
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// handleStates applies a fresh List snapshot. A dial failure transitions to
// the "offer to start" prompt; success refreshes the table and starts the log
// follow for the selected service if none is running.
func (m model) handleStates(msg statesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.states = nil
		m.closeFollow()
		m.noSupervisor = true
		m.setStatus("no supervisor running (press y to start, q to quit)", statusInfo)
		return m, nil
	}
	m.noSupervisor = false
	m.states = msg.states
	if len(m.states) == 0 {
		m.selected = 0
		m.closeFollow()
		return m, nil
	}
	if m.selected >= len(m.states) {
		m.selected = len(m.states) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	var cmd tea.Cmd
	if m.follow == nil && m.selectedName() != "" {
		cmd = m.startFollowSelected()
	}
	return m, cmd
}

// maybeSwitchFollow restarts the log follow when the selection has changed.
// It has a pointer receiver because it bumps followGen and clears the log
// buffer; callers return the mutated model so the gen advance survives.
func (m *model) maybeSwitchFollow() tea.Cmd {
	if m.noSupervisor || m.quitting {
		return nil
	}
	name := m.selectedName()
	if name == "" {
		return nil
	}
	// Switching is always a fresh follow: close the old connection (its
	// goroutine's done message will be ignored as stale) and bump the gen.
	m.closeFollow()
	m.followGen++
	m.logLines = m.logLines[:0]
	m.viewport.SetContent("")
	if m.startFollow == nil {
		// Model wasn't constructed via New (e.g. in tests). No program to
		// bind a follow to.
		return nil
	}
	return m.startFollow(m.socket, name, m.followGen)
}

// startFollowSelected begins a follow for the currently selected service.
// Pointer receiver for the same reason as maybeSwitchFollow.
func (m *model) startFollowSelected() tea.Cmd {
	name := m.selectedName()
	if name == "" {
		return nil
	}
	m.followGen++
	m.logLines = m.logLines[:0]
	m.viewport.SetContent("")
	if m.startFollow == nil {
		// Model wasn't constructed via New (e.g. in tests).
		return nil
	}
	return m.startFollow(m.socket, name, m.followGen)
}

// closeFollow closes the active log-follow connection (if any). The pump
// goroutine will emit a stale logDoneMsg that Update ignores via followGen.
func (m *model) closeFollow() {
	if m.follow != nil {
		_ = m.follow.Close()
		m.follow = nil
	}
}

func (m *model) appendLog(line string) {
	m.logLines = boundLogLines(m.logLines, line, maxLogLineCount)
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(strings.Join(m.logLines, "\n"))
	if atBottom {
		m.viewport.GotoBottom()
	}
}

// boundLogLines appends line to lines and trims the oldest entries so the
// slice never exceeds max. It is pure (no rendering) so it can be unit-tested
// in isolation from the viewport.
func boundLogLines(lines []string, line string, max int) []string {
	lines = append(lines, line)
	if len(lines) > max {
		excess := len(lines) - max
		lines = lines[excess:]
	}
	return lines
}

func (m *model) setStatus(s string, k statusKind) {
	m.status = s
	m.statusKind = k
}

// selectedName returns the name of the currently highlighted service, or "".
func (m model) selectedName() string {
	if m.selected < 0 || m.selected >= len(m.states) {
		return ""
	}
	return m.states[m.selected].Name
}

// layoutViewport sizes the log viewport to fit the right pane given the
// current terminal dimensions.
func (m *model) layoutViewport() {
	if !m.ready {
		return
	}
	rightW := m.width - listPaneWidth
	if rightW < 10 {
		rightW = 10
	}
	innerW := rightW - 2 // box border
	if innerW < 1 {
		innerW = 1
	}
	innerH := m.height - helpBarHeight - 2 // box border
	if innerH < 1 {
		innerH = 1
	}
	m.viewport.SoftWrap = true
	m.viewport.SetWidth(innerW)
	m.viewport.SetHeight(innerH)
}

// --- View ---

func (m model) View() tea.View {
	if !m.ready {
		return tea.NewView("local-compose tui: starting...")
	}
	if m.noSupervisor {
		return tea.NewView(m.renderStartPrompt())
	}
	v := tea.NewView(m.renderSplit())
	v.AltScreen = true
	return v
}

func (m model) renderStartPrompt() string {
	title := lipgloss.NewStyle().Bold(true).Render("local-compose tui")
	body := fmt.Sprintf(
		"No supervisor running for project %q.\n\n  y - start it now (detached, like `up -d`)\n  n / q - quit",
		m.project,
	)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body)
}

func (m model) renderSplit() string {
	left := m.renderListPane()
	right := m.renderLogsPane()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	help := m.renderHelpBar()
	return lipgloss.JoinVertical(lipgloss.Left, body, help)
}

var (
	paneBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	titleStyle = lipgloss.NewStyle().Bold(true).Faint(true)
)

func (m model) renderListPane() string {
	width := listPaneWidth - 2 // account for border + padding
	nameWidth := max(1, width-16)
	header := fmt.Sprintf("%-*s %-9s %5s", nameWidth, "SERVICE", "STATUS", "PID")
	rows := []string{titleStyle.Render("Services"), header}
	for i, st := range m.states {
		name := st.Name
		if len(name) > nameWidth {
			name = name[:nameWidth]
		}
		statusCell := lipgloss.NewStyle().Foreground(ui.StatusColor(st.Status)).Render(fmt.Sprintf("%-9s", st.Status))
		pidCell := ui.PIDLabel(st.PID)
		marker := " "
		if i == m.selected {
			marker = "▸"
		}
		line := fmt.Sprintf("%s %-*s %s %5s", marker, nameWidth, name, statusCell, pidCell)
		if i == m.selected {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.states) == 0 {
		rows = append(rows, lipgloss.NewStyle().Faint(true).Render("(no services)"))
	}
	content := strings.Join(rows, "\n")
	height := m.height - helpBarHeight - 2
	return paneBorder.Width(listPaneWidth).Height(height).Render(content)
}

func (m model) renderLogsPane() string {
	title := "Logs"
	if name := m.selectedName(); name != "" {
		title = "Logs: " + name
	}
	content := titleStyle.Render(title) + "\n" + m.viewport.View()
	height := m.height - helpBarHeight - 2
	return paneBorder.Width(m.width - listPaneWidth).Height(height).Render(content)
}

func (m model) renderHelpBar() string {
	keys := " ↑/↓ select · Tab pane · r restart · s stop · d down · G/g top/bottom · q quit"
	if m.pane == paneLogs {
		keys = " Logs pane: ↑/↓ scroll · PgUp/PgDn · h back to list · Tab pane · r restart · s stop · q quit"
	}
	statusLine := m.status
	if statusLine == "" {
		statusLine = fmt.Sprintf("project %s", m.project)
	}
	statusStyle := lipgloss.NewStyle()
	if m.statusKind == statusErr {
		statusStyle = statusStyle.Foreground(lipgloss.Color("196"))
	}
	left := statusStyle.Render(truncate(statusLine, m.width-len(keys)-2))
	right := lipgloss.NewStyle().Faint(true).Render(keys)
	return lipgloss.JoinHorizontal(lipgloss.Left, left, "  ", right)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	// crude truncation by rune count; good enough for a status line.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// --- commands ---

func connectCmd(socket string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return statesMsg{err: err}
		}
		defer func() { _ = c.Close() }()
		states, err := c.List()
		return statesMsg{states: states, err: err}
	}
}

func startFollowCmd(socket, service string, gen int64, p *tea.Program) tea.Cmd {
	return func() tea.Msg {
		if p == nil {
			// Should not happen: the startFollow closure in New captures the
			// program before it's assigned, but only invokes after. Guard so a
			// future regression surfaces as a soft error instead of a nil-deref
			// panic inside a goroutine.
			return logDoneMsg{gen: gen, service: service, err: errors.New("tui: program not initialized")}
		}
		c, err := control.Dial(socket)
		if err != nil {
			return logDoneMsg{gen: gen, service: service, err: err}
		}
		if err := c.Send(protocol.Request{Kind: protocol.KindLogs, Service: service, Follow: true}); err != nil {
			_ = c.Close()
			return logDoneMsg{gen: gen, service: service, err: err}
		}
		go pumpLogs(c, service, gen, p)
		return followStartedMsg{gen: gen, service: service, client: c}
	}
}

func pumpLogs(c *control.Client, service string, gen int64, p *tea.Program) {
	defer func() { _ = c.Close() }()
	for {
		resp, err := c.Recv()
		if err != nil {
			p.Send(logDoneMsg{gen: gen, service: service, err: err})
			return
		}
		switch resp.Kind {
		case protocol.KindLogLine:
			p.Send(logLineMsg{gen: gen, service: service, line: resp.Line})
		case protocol.KindDone:
			p.Send(logDoneMsg{gen: gen, service: service})
			return
		case protocol.KindError:
			p.Send(logDoneMsg{gen: gen, service: service, err: errors.New(resp.Error)})
			return
		}
	}
}

func actionCmd(socket, action, service string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return actionResultMsg{action: action, service: service, err: err}
		}
		defer func() { _ = c.Close() }()
		switch action {
		case "restart":
			err = c.Restart(service)
		case "stop":
			err = c.StopService(service)
		case "down":
			err = c.Stop()
		default:
			err = fmt.Errorf("unknown action %q", action)
		}
		return actionResultMsg{action: action, service: service, err: err}
	}
}

func startDaemonCmd(locs *project.Locations, project, configPath string) tea.Cmd {
	return func() tea.Msg {
		if locs == nil {
			return startedMsg{err: errors.New("no project locations resolved")}
		}
		if pid, err := daemon.Running(locs); err != nil {
			return startedMsg{err: err}
		} else if pid > 0 {
			waitForSocket(locs.Socket, 3*time.Second)
			return startedMsg{pid: pid}
		}
		pid, err := daemon.Spawn(daemon.Options{
			Locations:  locs,
			Project:    project,
			ConfigPath: configPath,
		})
		if err != nil {
			return startedMsg{err: err}
		}
		waitForSocket(locs.Socket, 3*time.Second)
		return startedMsg{pid: pid}
	}
}

// waitForSocket polls until a Unix socket exists at path or the timeout
// elapses, giving the daemonized child a moment to bind before we dial.
func waitForSocket(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
