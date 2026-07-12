// Package tui implements the Bubble Tea interactive frontend.
//
// The TUI is a thin client over the global daemon's control socket. It shows a
// project list first (polled from list_projects), then a service view for the
// selected project with streaming logs, restart/stop keybindings, and a
// back-to-projects key (Esc). If no daemon is running it offers to start one.
package tui

import (
	"errors"
	"fmt"
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

// view is the currently active screen.
type view int

const (
	viewProjects view = iota
	viewServices
)

// pane is the currently focused split in the services view.
type pane int

const (
	paneList pane = iota
	paneLogs
)

// Options configures a TUI run. Socket is the daemon's control socket. If
// Project is non-empty, the TUI skips the project list and goes directly to
// that project's service view.
type Options struct {
	Socket     string
	Project    string
	ConfigPath string
}

// New constructs the Bubble Tea program for the TUI.
func New(opts Options) *tea.Program {
	m := model{
		socket:     opts.Socket,
		project:    opts.Project,
		configPath: opts.ConfigPath,
	}
	if opts.Project != "" {
		m.currentView = viewServices
	} else {
		m.currentView = viewProjects
	}
	m.pane = paneList

	var p *tea.Program
	m.startFollow = func(socket, project, service string, gen int64) tea.Cmd {
		return startFollowCmd(socket, project, service, gen, p)
	}
	p = tea.NewProgram(m)
	return p
}

type model struct {
	socket     string
	project    string
	configPath string

	// startFollow builds a startFollowCmd bound to the running *tea.Program.
	startFollow func(socket, project, service string, gen int64) tea.Cmd

	width, height int
	ready         bool

	// Project list view state.
	projects     []protocol.ProjectInfo
	selectedProj int

	// Service view state.
	currentView view
	states      []protocol.ServiceState
	selected    int

	pane     pane
	viewport viewport.Model

	// follow is the active log-follow connection.
	follow    *control.Client
	followGen int64

	logLines []string

	status     string
	statusKind statusKind

	noDaemon bool
	quitting bool
}

type statusKind int

const (
	statusInfo statusKind = iota
	statusErr
)

// --- messages ---

type tickMsg struct{}

type projectsMsg struct {
	projects []protocol.ProjectInfo
	err      error
}

type statesMsg struct {
	states []protocol.ServiceState
	err    error
}

type logLineMsg struct {
	gen     int64
	service string
	line    string
}

type logContentMsg struct {
	gen     int64
	service string
	content string
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
		tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} }),
		m.pollCmd(),
	)
}

func (m model) pollCmd() tea.Cmd {
	if m.noDaemon {
		return nil
	}
	if m.currentView == viewProjects {
		return pollProjectsCmd(m.socket)
	}
	return pollStatesCmd(m.socket, m.project)
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

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)

	case tea.MouseWheelMsg:
		if m.currentView == viewServices {
			m.pane = paneLogs
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} }),
			m.pollCmd(),
		)

	case projectsMsg:
		return m.handleProjects(msg)

	case statesMsg:
		return m.handleStates(msg)

	case followStartedMsg:
		if msg.gen != m.followGen {
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

	case logContentMsg:
		if msg.gen != m.followGen || msg.service != m.selectedName() {
			return m, nil
		}
		m.logLines = nil
		for _, line := range strings.Split(strings.TrimRight(msg.content, "\n"), "\n") {
			if line != "" {
				m.logLines = boundLogLines(m.logLines, ui.CleanLogLine(line), maxLogLineCount)
			}
		}
		m.viewport.SetContent(strings.Join(m.logLines, "\n"))
		m.viewport.GotoBottom()
		return m, nil

	case logDoneMsg:
		if msg.gen != m.followGen || (msg.service != "" && msg.service != m.selectedName()) {
			return m, nil
		}
		m.follow = nil
		if msg.err != nil && !m.quitting {
			m.setStatus("logs: "+msg.err.Error(), statusErr)
			return m, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
				return retryFollowMsg{}
			})
		}
		return m, nil

	case retryFollowMsg:
		if m.quitting || m.noDaemon {
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
		return m, m.pollCmd()

	case startedMsg:
		if msg.err != nil {
			m.setStatus("start daemon: "+msg.err.Error(), statusErr)
			m.noDaemon = true
			return m, nil
		}
		m.noDaemon = false
		m.setStatus(fmt.Sprintf("daemon started (pid %d)", msg.pid), statusInfo)
		return m, tea.Batch(
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} }),
			m.pollCmd(),
		)
	}
	return m, nil
}

type retryFollowMsg struct{}

// --- Key handling ---

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.noDaemon {
		return m.handleStartPrompt(msg)
	}
	if m.quitting {
		return m, nil
	}

	// Global quit works from any view.
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		m.closeFollow()
		return m, tea.Quit
	}

	if m.currentView == viewProjects {
		return m.handleProjectListKey(msg)
	}
	return m.handleServiceViewKey(msg)
}

func (m model) handleStartPrompt(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.noDaemon = false
		m.setStatus("starting daemon...", statusInfo)
		return m, startDaemonCmd(m.configPath)
	case "n", "N", "q", "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleProjectListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.selectedProj > 0 {
			m.selectedProj--
		}
		return m, nil

	case "down", "j":
		if m.selectedProj < len(m.projects)-1 {
			m.selectedProj++
		}
		return m, nil

	case "home", "g":
		m.selectedProj = 0
		return m, nil

	case "end", "G":
		m.selectedProj = len(m.projects) - 1
		return m, nil

	case "enter", "right", "l":
		if m.selectedProj < 0 || m.selectedProj >= len(m.projects) {
			return m, nil
		}
		m.project = m.projects[m.selectedProj].Name
		m.configPath = m.projects[m.selectedProj].ConfigPath
		m.currentView = viewServices
		m.states = nil
		m.selected = 0
		m.closeFollow()
		return m, pollStatesCmd(m.socket, m.project)

	case "s":
		// Start the selected (stopped) project.
		if m.selectedProj < 0 || m.selectedProj >= len(m.projects) {
			return m, nil
		}
		p := m.projects[m.selectedProj]
		if p.ConfigPath == "" {
			return m, nil
		}
		m.setStatus("starting "+p.Name+"...", statusInfo)
		return m, startProjectCmd(m.socket, p.ConfigPath)
	}
	return m, nil
}

func (m model) handleServiceViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		m.closeFollow()
		return m, tea.Quit

	case "esc", "backspace", "left", "h":
		if m.pane == paneLogs {
			m.pane = paneList
			return m, nil
		}
		// Esc from list pane → back to projects.
		m.currentView = viewProjects
		m.closeFollow()
		m.states = nil
		return m, pollProjectsCmd(m.socket)

	case "tab", "shift+tab":
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
		return m, actionCmd(m.socket, m.project, "restart", name)

	case "s":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("stopping "+name+"...", statusInfo)
		return m, actionCmd(m.socket, m.project, "stop", name)

	case "d":
		m.setStatus("down...", statusInfo)
		m.closeFollow()
		return m, actionCmd(m.socket, m.project, "down", "")
	}

	if m.pane == paneList {
		return m.handleListKey(msg)
	}
	return m.handleLogsKey(msg)
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

func (m model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if m.currentView == viewProjects {
		paneW := min(m.width, 50)
		if mouse.X >= paneW || mouse.Y < 0 {
			return m, nil
		}
		row := mouse.Y - 2
		if row >= 0 && row < len(m.projects) {
			m.selectedProj = row
		}
		return m, nil
	}
	if m.currentView != viewServices {
		return m, nil
	}
	if mouse.X < listPaneWidth {
		m.pane = paneList
		row := mouse.Y - 2
		if row >= 0 && row < len(m.states) {
			old := m.selected
			m.selected = row
			if old != m.selected {
				return m, m.maybeSwitchFollow()
			}
		}
		return m, nil
	}
	m.pane = paneLogs
	return m, nil
}

// --- State handlers ---

func (m model) handleProjects(msg projectsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.projects = nil
		m.noDaemon = true
		m.setStatus("no daemon running (press y to start, q to quit)", statusInfo)
		return m, nil
	}
	m.noDaemon = false
	m.projects = msg.projects
	if m.selectedProj >= len(m.projects) {
		m.selectedProj = len(m.projects) - 1
	}
	if m.selectedProj < 0 {
		m.selectedProj = 0
	}
	return m, nil
}

func (m model) handleStates(msg statesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.states = nil
		m.closeFollow()
		// Project might have been stopped; go back to projects list.
		m.currentView = viewProjects
		m.setStatus("project not running (press Esc to go back)", statusInfo)
		return m, pollProjectsCmd(m.socket)
	}
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

// --- Follow management ---

func (m *model) maybeSwitchFollow() tea.Cmd {
	if m.noDaemon || m.quitting {
		return nil
	}
	name := m.selectedName()
	if name == "" {
		return nil
	}
	m.closeFollow()
	m.followGen++
	m.logLines = m.logLines[:0]
	m.viewport.SetContent("")
	if m.startFollow == nil {
		return nil
	}
	return m.startFollow(m.socket, m.project, name, m.followGen)
}

func (m *model) startFollowSelected() tea.Cmd {
	name := m.selectedName()
	if name == "" {
		return nil
	}
	m.followGen++
	m.logLines = m.logLines[:0]
	m.viewport.SetContent("")
	if m.startFollow == nil {
		return nil
	}
	return m.startFollow(m.socket, m.project, name, m.followGen)
}

func (m *model) closeFollow() {
	if m.follow != nil {
		_ = m.follow.Close()
		m.follow = nil
	}
}

func (m *model) appendLog(line string) {
	m.logLines = boundLogLines(m.logLines, ui.CleanLogLine(line), maxLogLineCount)
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(strings.Join(m.logLines, "\n"))
	if atBottom {
		m.viewport.GotoBottom()
	}
}

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

func (m model) selectedName() string {
	if m.selected < 0 || m.selected >= len(m.states) {
		return ""
	}
	return m.states[m.selected].Name
}

func (m *model) layoutViewport() {
	if !m.ready {
		return
	}
	rightW := m.width - listPaneWidth
	if rightW < 10 {
		rightW = 10
	}
	innerW := rightW - 4
	if innerW < 1 {
		innerW = 1
	}
	innerH := m.height - helpBarHeight - 2
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
	if m.noDaemon {
		return tea.NewView(m.renderStartPrompt())
	}
	if m.currentView == viewProjects {
		v := tea.NewView(m.renderProjectsView())
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	v := tea.NewView(m.renderSplit())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m model) renderStartPrompt() string {
	title := lipgloss.NewStyle().Bold(true).Render("local-compose tui")
	body := "No daemon running.\n\n  y - start it now\n  n / q - quit"
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body)
}

func (m model) renderProjectsView() string {
	paneW := min(m.width, 50)
	contentWidth := paneW - 4
	nameWidth := max(1, contentWidth-12)
	header := fmt.Sprintf(" %-*s %-9s", nameWidth, "PROJECT", "STATUS")
	rows := []string{header}
	for i, p := range m.projects {
		name := p.Name
		if len(name) > nameWidth {
			name = name[:nameWidth]
		}
		statusCell := lipgloss.NewStyle().Foreground(ui.StatusColor(p.Status)).Render(fmt.Sprintf("%-9s", p.Status))
		line := fmt.Sprintf(" %-*s %s", nameWidth, name, statusCell)
		if i == m.selectedProj {
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += strings.Repeat(" ", contentWidth-w)
			}
			line = lipgloss.NewStyle().Background(lipgloss.Color("62")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.projects) == 0 {
		rows = append(rows, lipgloss.NewStyle().Faint(true).Render("(no projects; run `local-compose up` to start one)"))
	}
	content := strings.Join(rows, "\n")
	height := m.height - helpBarHeight
	body := renderTitledPane(paneW, height, "Projects", content, true)
	help := m.renderHelpBar()
	return lipgloss.JoinVertical(lipgloss.Left, body, help)
}

func (m model) renderSplit() string {
	left := m.renderListPane()
	right := m.renderLogsPane()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	help := m.renderHelpBar()
	return lipgloss.JoinVertical(lipgloss.Left, body, help)
}

func renderTitledPane(width, height int, title, content string, active bool) string {
	innerWidth := width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}
	titlePart := "━ " + title + " "
	titleRunes := []rune(titlePart)
	fillWidth := innerWidth - len(titleRunes)
	if fillWidth < 0 {
		if innerWidth > 2 {
			titlePart = string(titleRunes[:innerWidth-1]) + " "
		} else {
			titlePart = ""
		}
		titleRunes = []rune(titlePart)
		fillWidth = innerWidth - len(titleRunes)
		if fillWidth < 0 {
			fillWidth = 0
		}
	}

	var borderStyle, titleStyle lipgloss.Style
	if active {
		borderStyle = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("255")).Padding(0, 1)
		titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	} else {
		borderStyle = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("243")).Padding(0, 1)
		titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	}

	topLine := titleStyle.Render("┏" + titlePart + strings.Repeat("━", fillWidth) + "┓")

	rendered := borderStyle.Width(width).Height(height).Render(content)
	lines := strings.SplitN(rendered, "\n", 2)
	if len(lines) < 2 {
		return rendered
	}
	return topLine + "\n" + lines[1]
}

func (m model) renderListPane() string {
	contentWidth := listPaneWidth - 4
	nameWidth := max(1, contentWidth-18)
	header := fmt.Sprintf(" %-*s %-9s %5s", nameWidth, "SERVICE", "STATUS", "PID")
	rows := []string{header}
	for i, st := range m.states {
		name := st.Name
		if len(name) > nameWidth {
			name = name[:nameWidth]
		}
		statusCell := lipgloss.NewStyle().Foreground(ui.StatusColor(st.Status)).Render(fmt.Sprintf("%-9s", ui.StatusLabel(st.Status, st.ExitCode)))
		pidCell := ui.PIDLabel(st.PID)
		line := fmt.Sprintf(" %-*s %s %5s", nameWidth, name, statusCell, pidCell)
		if i == m.selected {
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += strings.Repeat(" ", contentWidth-w)
			}
			line = lipgloss.NewStyle().Background(lipgloss.Color("62")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.states) == 0 {
		rows = append(rows, lipgloss.NewStyle().Faint(true).Render("(no services)"))
	}
	content := strings.Join(rows, "\n")
	height := m.height - helpBarHeight
	return renderTitledPane(listPaneWidth, height, "Services", content, m.pane == paneList)
}

func (m model) renderLogsPane() string {
	title := "Logs"
	if name := m.selectedName(); name != "" {
		title = "Logs: " + name
	}
	content := m.viewport.View()
	height := m.height - helpBarHeight
	return renderTitledPane(m.width-listPaneWidth, height, title, content, m.pane == paneLogs)
}

func (m model) renderHelpBar() string {
	var keys string
	if m.currentView == viewProjects {
		keys = " ↑/↓ select · Enter open · s start · q quit"
	} else if m.pane == paneLogs {
		keys = " Logs: ↑/↓ scroll · h/Tab list · r restart · s stop · d down · Esc back · q quit"
	} else {
		keys = " ↑/↓ select · Tab logs · r restart · s stop · d down · Esc back · q quit"
	}
	statusLine := m.status
	if statusLine == "" {
		if m.currentView == viewServices {
			statusLine = fmt.Sprintf("project %s", m.project)
		} else {
			statusLine = "local-compose"
		}
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
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// --- commands ---

func pollProjectsCmd(socket string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return projectsMsg{err: err}
		}
		defer func() { _ = c.Close() }()
		projects, err := c.ListProjects()
		return projectsMsg{projects: projects, err: err}
	}
}

func pollStatesCmd(socket, project string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return statesMsg{err: err}
		}
		defer func() { _ = c.Close() }()
		states, err := c.List(project)
		return statesMsg{states: states, err: err}
	}
}

func startFollowCmd(socket, project, service string, gen int64, p *tea.Program) tea.Cmd {
	return func() tea.Msg {
		if p == nil {
			return logDoneMsg{gen: gen, service: service, err: errors.New("tui: program not initialized")}
		}
		c, err := control.Dial(socket)
		if err != nil {
			return logDoneMsg{gen: gen, service: service, err: err}
		}
		if err := c.Send(protocol.Request{Kind: protocol.KindLogs, Project: project, Service: service, Follow: true}); err != nil {
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
		case protocol.KindLogContent:
			p.Send(logContentMsg{gen: gen, service: service, content: resp.Content})
		case protocol.KindDone:
			p.Send(logDoneMsg{gen: gen, service: service})
			return
		case protocol.KindError:
			p.Send(logDoneMsg{gen: gen, service: service, err: errors.New(resp.Error)})
			return
		}
	}
}

func actionCmd(socket, project, action, service string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return actionResultMsg{action: action, service: service, err: err}
		}
		defer func() { _ = c.Close() }()
		switch action {
		case "restart":
			err = c.Restart(project, service)
		case "stop":
			err = c.StopService(project, service)
		case "down":
			err = c.StopProject(project)
		default:
			err = fmt.Errorf("unknown action %q", action)
		}
		return actionResultMsg{action: action, service: service, err: err}
	}
}

func startProjectCmd(socket, configPath string) tea.Cmd {
	return func() tea.Msg {
		c, err := control.Dial(socket)
		if err != nil {
			return actionResultMsg{action: "start", err: err}
		}
		defer func() { _ = c.Close() }()
		if err := c.StartProject(configPath, false); err != nil {
			return actionResultMsg{action: "start", err: err}
		}
		return actionResultMsg{action: "start"}
	}
}

func startDaemonCmd(configPath string) tea.Cmd {
	return func() tea.Msg {
		dloc, err := project.ResolveDaemon()
		if err != nil {
			return startedMsg{err: err}
		}
		pid, err := daemon.DaemonRunning(dloc)
		if err != nil {
			return startedMsg{err: err}
		}
		if pid == 0 {
			pid, err = daemon.SpawnDaemon(dloc)
			if err != nil {
				return startedMsg{err: err}
			}
		}
		_ = control.WaitForSocket(dloc.Socket, 3*time.Second)
		return startedMsg{pid: pid}
	}
}
