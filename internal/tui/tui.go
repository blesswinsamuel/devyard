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
	"github.com/charmbracelet/x/ansi"

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

	selColor   = "238" // selection background
	hoverColor = "237" // hover background (one step lighter)

	copyCursorColor = "220" // bright yellow cursor highlight
	copySelectColor = "24"  // dark blue selection highlight
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

// helpBarItem represents a clickable command in the bottom bar.
type helpBarItem struct {
	label string // e.g. "restart"
	key   string // e.g. "r"
}

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
		socket:         opts.Socket,
		project:        opts.Project,
		configPath:     opts.ConfigPath,
		hoveredRow:     -1,
		hoveredProjRow: -1,
		hoveredCmd:     -1,
		selAnchorLine:  -1,
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

	// Hover state.
	hoveredRow     int // index of hovered service row, -1 for none
	hoveredProjRow int // index of hovered project row, -1 for none
	hoveredCmd     int // index of hovered command in help bar, -1 for none

	// Help popup state.
	showHelp bool

	// Copy/select mode state.
	copyMode       bool
	cursorLine     int  // line index in m.logLines
	cursorCol      int  // rune index within the line
	selAnchorLine  int  // selection anchor line (-1 = no anchor)
	selAnchorCol   int  // selection anchor column
	mouseSelecting bool // true while mouse button held for drag selection
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

	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)

	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)

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
		if m.copyMode {
			m.exitCopyMode()
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

	// Help popup toggles.
	if msg.String() == "?" {
		m.showHelp = !m.showHelp
		if !m.showHelp {
			m.hoveredCmd = -1
		}
		return m, nil
	}

	// When help popup is open, suppress all other keys.
	if m.showHelp {
		if msg.String() == "esc" {
			m.showHelp = false
			m.hoveredCmd = -1
			return m, nil
		}
		return m, nil
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
	}

	if m.copyMode {
		return m.handleCopyModeKey(msg)
	}

	switch msg.String() {
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

	case "k":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("killing "+name+"...", statusInfo)
		return m, actionCmd(m.socket, m.project, "kill", name)

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
	old := m.selected
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
		return m, nil
	default:
		return m, nil
	}
	if old != m.selected {
		return m, m.maybeSwitchFollow()
	}
	return m, nil
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

	// Click on help popup closes it.
	if m.showHelp {
		m.showHelp = false
		m.hoveredCmd = -1
		return m, nil
	}

	// Click on help bar (bottom row).
	if mouse.Y == m.height-1 {
		return m.handleHelpBarClick(mouse.X)
	}

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
		if m.copyMode {
			m.exitCopyMode()
		}
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
	if m.currentView == viewServices && mouse.X >= listPaneWidth && mouse.Y >= 0 && mouse.Y < m.height-1 {
		line, col := m.screenToLogPos(mouse.X, mouse.Y)
		m.enterCopyMode(line, col)
		m.selAnchorLine = line
		m.selAnchorCol = col
		m.mouseSelecting = true
		m.applySelectionHighlights()
	}
	return m, nil
}

func (m model) handleHelpBarClick(x int) (tea.Model, tea.Cmd) {
	items := m.helpBarItems()

	// Calculate x-positions for each button.
	xPos := 0
	for _, item := range items {
		itemW := len(item.key) + len(item.label) + 3 // " k label "
		if x >= xPos && x < xPos+itemW {
			return m.executeHelpBarItem(item)
		}
		xPos += itemW
	}

	// Check if click is on the "? help" hint (far right).
	helpHintW := lipgloss.Width("? help")
	if x >= m.width-helpHintW {
		m.showHelp = true
		return m, nil
	}

	return m, nil
}

func (m model) handleMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()

	if m.showHelp {
		m.hoveredCmd = -1
		m.hoveredRow = -1
		m.hoveredProjRow = -1
		return m, nil
	}

	// Help bar row: track command hover.
	if mouse.Y == m.height-1 {
		m.hoveredRow = -1
		m.hoveredProjRow = -1
		items := m.helpBarItems()
		found := -1
		xPos := 0
		for i, item := range items {
			itemW := len(item.key) + len(item.label) + 3 // " k label "
			if mouse.X >= xPos && mouse.X < xPos+itemW {
				found = i
				break
			}
			xPos += itemW
		}
		if found != m.hoveredCmd {
			m.hoveredCmd = found
		}
		return m, nil
	}

	m.hoveredCmd = -1

	if m.currentView == viewProjects {
		paneW := min(m.width, 50)
		if mouse.X < paneW && mouse.Y >= 2 {
			row := mouse.Y - 2
			if row < len(m.projects) {
				m.hoveredProjRow = row
				return m, nil
			}
		}
		m.hoveredProjRow = -1
		return m, nil
	}

	if m.currentView == viewServices && m.pane == paneList {
		if mouse.X < listPaneWidth && mouse.Y >= 2 {
			row := mouse.Y - 2
			if row < len(m.states) {
				m.hoveredRow = row
				return m, nil
			}
		}
		m.hoveredRow = -1
		return m, nil
	}

	m.hoveredRow = -1
	m.hoveredProjRow = -1

	// Handle mouse drag selection in logs pane.
	if m.copyMode && m.mouseSelecting && mouse.Button != 0 && m.currentView == viewServices {
		line, col := m.screenToLogPos(mouse.X, mouse.Y)
		m.cursorLine = line
		m.cursorCol = col
		m.clampCursor()
		m.applySelectionHighlights()
		return m, nil
	}

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
	oldLen := len(m.logLines)
	m.logLines = boundLogLines(m.logLines, ui.CleanLogLine(line), maxLogLineCount)
	trimmed := oldLen + 1 - len(m.logLines)
	if trimmed > 0 && m.copyMode {
		m.cursorLine -= trimmed
		m.selAnchorLine -= trimmed
		m.clampCursor()
	}
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(strings.Join(m.logLines, "\n"))
	if atBottom {
		m.viewport.GotoBottom()
	}
	if m.copyMode {
		m.applySelectionHighlights()
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

func (m model) helpBarItems() []helpBarItem {
	if m.currentView == viewProjects {
		return []helpBarItem{
			{label: "start", key: "s"},
		}
	}
	return []helpBarItem{
		{label: "restart", key: "r"},
		{label: "stop", key: "s"},
		{label: "kill", key: "k"},
		{label: "down", key: "d"},
	}
}

func (m model) executeHelpBarItem(item helpBarItem) (tea.Model, tea.Cmd) {
	switch item.key {
	case "r":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("restarting "+name+"...", statusInfo)
		return m, actionCmd(m.socket, m.project, "restart", name)
	case "s":
		if m.currentView == viewProjects {
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
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("stopping "+name+"...", statusInfo)
		return m, actionCmd(m.socket, m.project, "stop", name)
	case "k":
		name := m.selectedName()
		if name == "" {
			return m, nil
		}
		m.setStatus("killing "+name+"...", statusInfo)
		return m, actionCmd(m.socket, m.project, "kill", name)
	case "d":
		m.setStatus("down...", statusInfo)
		m.closeFollow()
		return m, actionCmd(m.socket, m.project, "down", "")
	}
	return m, nil
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
	var content string
	if m.currentView == viewProjects {
		content = m.renderProjectsView()
	} else {
		content = m.renderSplit()
	}
	if m.showHelp {
		popup := m.renderHelpPopup()
		content = m.overlayPopup(content, popup)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
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
		statusText := fmt.Sprintf("%-9s", p.Status)

		var line string
		if i == m.selectedProj {
			bg := lipgloss.Color(selColor)
			namePart := lipgloss.NewStyle().Background(bg).Render(fmt.Sprintf(" %-*s", nameWidth, name))
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(p.Status)).Background(bg).Render(" " + statusText)
			line = namePart + statusPart
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", contentWidth-w))
			}
		} else if i == m.hoveredProjRow {
			bg := lipgloss.Color(hoverColor)
			namePart := lipgloss.NewStyle().Background(bg).Render(fmt.Sprintf(" %-*s", nameWidth, name))
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(p.Status)).Background(bg).Render(" " + statusText)
			line = namePart + statusPart
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", contentWidth-w))
			}
		} else {
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(p.Status)).Render(statusText)
			line = fmt.Sprintf(" %-*s %s", nameWidth, name, statusPart)
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
		statusText := fmt.Sprintf("%-9s", ui.StatusLabel(st.Status, st.ExitCode))
		pidText := fmt.Sprintf("%5s", ui.PIDLabel(st.PID))

		var line string
		if i == m.selected {
			bg := lipgloss.Color(selColor)
			namePart := lipgloss.NewStyle().Background(bg).Render(fmt.Sprintf(" %-*s", nameWidth, name))
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(st.Status)).Background(bg).Render(" " + statusText)
			pidPart := lipgloss.NewStyle().Background(bg).Render(" " + pidText)
			line = namePart + statusPart + pidPart
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", contentWidth-w))
			}
		} else if i == m.hoveredRow {
			bg := lipgloss.Color(hoverColor)
			namePart := lipgloss.NewStyle().Background(bg).Render(fmt.Sprintf(" %-*s", nameWidth, name))
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(st.Status)).Background(bg).Render(" " + statusText)
			pidPart := lipgloss.NewStyle().Background(bg).Render(" " + pidText)
			line = namePart + statusPart + pidPart
			w := lipgloss.Width(line)
			if w < contentWidth {
				line += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", contentWidth-w))
			}
		} else {
			statusPart := lipgloss.NewStyle().Foreground(ui.StatusColor(st.Status)).Render(statusText)
			line = fmt.Sprintf(" %-*s %s %5s", nameWidth, name, statusPart, pidText)
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
	if m.copyMode {
		title += " [SELECT]"
	}
	content := m.viewport.View()
	height := m.height - helpBarHeight
	return renderTitledPane(m.width-listPaneWidth, height, title, content, m.pane == paneLogs)
}

func (m model) renderHelpBar() string {
	var buttons []string

	if m.copyMode {
		items := []helpBarItem{
			{label: "select", key: "v"},
			{label: "copy", key: "c"},
			{label: "exit", key: "esc"},
		}
		for _, item := range items {
			text := fmt.Sprintf(" %s %s ", item.key, item.label)
			styled := lipgloss.NewStyle().Faint(true).Render(text)
			buttons = append(buttons, styled)
		}
	} else {
		items := m.helpBarItems()
		for i, item := range items {
			text := fmt.Sprintf(" %s %s ", item.key, item.label)
			var styled string
			if i == m.hoveredCmd {
				styled = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("238")).Render(text)
			} else {
				styled = lipgloss.NewStyle().Faint(true).Render(text)
			}
			buttons = append(buttons, styled)
		}
	}
	left := strings.Join(buttons, "")

	// Status on the right side.
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

	// Help hint on the far right.
	helpHint := lipgloss.NewStyle().Faint(true).Render("? help")

	// Right-align status + help hint at the terminal edge.
	statusText := statusStyle.Render(statusLine)
	return left + lipgloss.PlaceHorizontal(m.width, lipgloss.Right, statusText+"  "+helpHint)
}

func (m model) renderHelpPopup() string {
	var sections []string

	// Navigation section
	navLines := []string{
		"  Navigation",
		"    \u2191/\u2193  k/j      Move up/down",
		"    \u2190/\u2192  h/l      Back / Forward",
		"    Tab            Switch pane",
		"    g/G            Top / Bottom",
		"    Enter          Select / Open",
	}
	sections = append(sections, strings.Join(navLines, "\n"))

	// Actions section
	var actionLines []string
	actionLines = append(actionLines, "  Actions")
	if m.currentView == viewProjects {
		actionLines = append(actionLines, "    s              Start project")
	} else {
		actionLines = append(actionLines, "    r              Restart service")
		actionLines = append(actionLines, "    s              Stop service")
		actionLines = append(actionLines, "    k              Kill service")
		actionLines = append(actionLines, "    d              Down project")
	}
	sections = append(sections, strings.Join(actionLines, "\n"))

	// General section
	genLines := []string{
		"  General",
		"    q              Quit",
		"    Esc            Back",
		"    ?              Close this help",
	}
	sections = append(sections, strings.Join(genLines, "\n"))

	// Copy mode section
	copyLines := []string{
		"  Copy Mode (in logs pane)",
		"    Click + drag    Select text with mouse",
		"    v              Enter / anchor selection",
		"    c              Copy selection to clipboard",
		"    h/j/k/l        Move cursor",
		"    0 / $          Start / end of line",
		"    g / G          Top / Bottom",
		"    Esc            Exit copy mode",
	}
	sections = append(sections, strings.Join(copyLines, "\n"))

	title := lipgloss.NewStyle().Bold(true).Render("Keyboard Shortcuts")
	content := title + "\n\n" + strings.Join(sections, "\n\n") + "\n"

	// Build bordered box
	popupW := 42

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Width(popupW-2). // border adds 2 chars
		Padding(0, 1)
	popup := borderStyle.Render(content)

	return popup
}

func (m model) overlayPopup(bg, popup string) string {
	bgLines := strings.Split(bg, "\n")
	popupLines := strings.Split(popup, "\n")

	bgH := len(bgLines)
	popupH := len(popupLines)
	popupW := 0
	for _, l := range popupLines {
		w := lipgloss.Width(l)
		if w > popupW {
			popupW = w
		}
	}

	startY := (bgH - popupH) / 2
	if startY < 0 {
		startY = 0
	}
	startX := (m.width - popupW) / 2
	if startX < 0 {
		startX = 0
	}

	result := make([]string, bgH)
	for y := 0; y < bgH; y++ {
		line := ""
		if y < len(bgLines) {
			line = bgLines[y]
		}
		if y >= startY && y-startY < popupH {
			pLine := popupLines[y-startY]
			padLeft := strings.Repeat(" ", startX)
			remaining := m.width - startX - lipgloss.Width(pLine)
			if remaining < 0 {
				remaining = 0
			}
			padRight := strings.Repeat(" ", remaining)
			result[y] = padLeft + pLine + padRight
		} else {
			result[y] = line
		}
	}
	return strings.Join(result, "\n")
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

// --- Copy mode ---

func (m *model) enterCopyMode(line, col int) {
	m.copyMode = true
	m.cursorLine = line
	m.cursorCol = col
	m.selAnchorLine = -1
	m.selAnchorCol = 0
	m.clampCursor()
}

func (m *model) exitCopyMode() {
	m.copyMode = false
	m.selAnchorLine = -1
	m.selAnchorCol = 0
	m.mouseSelecting = false
	m.viewport.ClearHighlights()
}

func (m model) handleCopyModeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitCopyMode()
		return m, nil

	case "v":
		if m.selAnchorLine >= 0 {
			m.selAnchorLine = -1
			m.selAnchorCol = 0
		} else {
			m.selAnchorLine = m.cursorLine
			m.selAnchorCol = m.cursorCol
		}
		m.applySelectionHighlights()
		return m, nil

	case "c":
		if m.selAnchorLine >= 0 {
			text := m.extractSelectedText()
			m.exitCopyMode()
			return m, tea.SetClipboard(text)
		}
		return m, nil

	case "h", "left":
		m.moveCursorLeft()
	case "l", "right":
		m.moveCursorRight()
	case "j", "down":
		m.moveCursorDown()
	case "k", "up":
		m.moveCursorUp()
	case "g", "home":
		m.cursorLine = 0
		m.cursorCol = 0
	case "G", "end":
		if len(m.logLines) > 0 {
			m.cursorLine = len(m.logLines) - 1
			runes := []rune(m.logLines[m.cursorLine])
			m.cursorCol = len(runes)
		}
	case "0":
		m.cursorCol = 0
	case "$":
		if m.cursorLine < len(m.logLines) {
			runes := []rune(m.logLines[m.cursorLine])
			m.cursorCol = len(runes)
		}
	default:
		return m, nil
	}

	m.clampCursor()
	m.ensureCursorVisible()
	m.applySelectionHighlights()
	return m, nil
}

func (m *model) moveCursorLeft() {
	if m.cursorCol > 0 {
		m.cursorCol--
	}
}

func (m *model) moveCursorRight() {
	if m.cursorLine < len(m.logLines) {
		runes := []rune(m.logLines[m.cursorLine])
		if m.cursorCol < len(runes) {
			m.cursorCol++
		}
	}
}

func (m *model) moveCursorUp() {
	if m.cursorLine > 0 {
		m.cursorLine--
		m.cursorCol = min(m.cursorCol, len([]rune(m.logLines[m.cursorLine])))
	}
}

func (m *model) moveCursorDown() {
	if m.cursorLine < len(m.logLines)-1 {
		m.cursorLine++
		m.cursorCol = min(m.cursorCol, len([]rune(m.logLines[m.cursorLine])))
	}
}

func (m *model) clampCursor() {
	if len(m.logLines) == 0 {
		m.cursorLine = 0
		m.cursorCol = 0
		return
	}
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	if m.cursorLine >= len(m.logLines) {
		m.cursorLine = len(m.logLines) - 1
	}
	runes := []rune(m.logLines[m.cursorLine])
	if m.cursorCol < 0 {
		m.cursorCol = 0
	}
	if m.cursorCol > len(runes) {
		m.cursorCol = len(runes)
	}
}

func (m *model) ensureCursorVisible() {
	if !m.ready {
		return
	}
	maxWidth := m.viewport.Width()
	if maxWidth <= 0 {
		return
	}
	visRow := m.logicalToVisualRow(m.cursorLine, m.cursorCol)
	visHeight := m.viewport.Height()
	yOff := m.viewport.YOffset()
	if visRow < yOff {
		m.viewport.SetYOffset(visRow)
	} else if visRow >= yOff+visHeight {
		m.viewport.SetYOffset(visRow - visHeight + 1)
	}
}

func (m *model) logicalToVisualRow(line, col int) int {
	maxWidth := m.viewport.Width()
	if maxWidth <= 0 {
		return 0
	}
	visRow := 0
	for i := 0; i < line && i < len(m.logLines); i++ {
		lineWidth := ansi.StringWidth(m.logLines[i])
		visRows := (lineWidth + maxWidth - 1) / maxWidth
		if visRows == 0 {
			visRows = 1
		}
		visRow += visRows
	}
	if line < len(m.logLines) {
		runes := []rune(m.logLines[line])
		if col > len(runes) {
			col = len(runes)
		}
		prefix := string(runes[:col])
		displayCol := ansi.StringWidth(prefix)
		visRow += displayCol / maxWidth
	}
	return visRow
}

func (m model) screenToLogPos(screenX, screenY int) (line, col int) {
	vpX := screenX - listPaneWidth - 2
	vpY := screenY - 1
	if vpX < 0 {
		vpX = 0
	}
	if vpY < 0 {
		vpY = 0
	}
	visRow := vpY + m.viewport.YOffset()
	maxWidth := m.viewport.Width()
	if maxWidth <= 0 || len(m.logLines) == 0 {
		return 0, 0
	}
	totalVisRow := 0
	for i, logLine := range m.logLines {
		lineWidth := ansi.StringWidth(logLine)
		lineVisRows := (lineWidth + maxWidth - 1) / maxWidth
		if lineVisRows == 0 {
			lineVisRows = 1
		}
		if totalVisRow+lineVisRows > visRow {
			visOffset := visRow - totalVisRow
			subLineStartCol := visOffset * maxWidth
			targetDisplayCol := subLineStartCol + vpX
			runes := []rune(logLine)
			accumWidth := 0
			for r, ch := range runes {
				chWidth := max(1, ansi.StringWidth(string(ch)))
				if accumWidth+chWidth > targetDisplayCol {
					return i, r
				}
				accumWidth += chWidth
			}
			return i, len(runes)
		}
		totalVisRow += lineVisRows
	}
	return len(m.logLines) - 1, 0
}

func (m *model) posToByteOffset(line, col int) int {
	offset := 0
	for i := 0; i < line && i < len(m.logLines); i++ {
		offset += len(m.logLines[i]) + 1
	}
	if line >= 0 && line < len(m.logLines) {
		runes := []rune(m.logLines[line])
		if col > len(runes) {
			col = len(runes)
		}
		offset += len(string(runes[:col]))
	}
	return offset
}

func (m *model) applySelectionHighlights() {
	m.viewport.HighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color(copySelectColor))
	m.viewport.SelectedHighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color(copySelectColor))

	content := m.viewport.GetContent()
	if content == "" {
		m.viewport.ClearHighlights()
		return
	}

	if m.selAnchorLine >= 0 {
		startByte := m.posToByteOffset(m.selAnchorLine, m.selAnchorCol)
		endByte := m.posToByteOffset(m.cursorLine, m.cursorCol)
		if startByte > endByte {
			startByte, endByte = endByte, startByte
		}
		if startByte == endByte {
			endByte = startByte + 1
		}
		m.viewport.SetHighlights([][]int{{startByte, endByte}})
	} else {
		byteOff := m.posToByteOffset(m.cursorLine, m.cursorCol)
		if byteOff < len(content) {
			m.viewport.HighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color(copyCursorColor))
			m.viewport.SetHighlights([][]int{{byteOff, byteOff + 1}})
		}
	}
}

func (m *model) extractSelectedText() string {
	if m.selAnchorLine < 0 {
		return ""
	}
	startLine, startCol := m.selAnchorLine, m.selAnchorCol
	endLine, endCol := m.cursorLine, m.cursorCol
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}
	if startLine == endLine {
		runes := []rune(m.logLines[startLine])
		startCol = min(startCol, len(runes))
		endCol = min(endCol, len(runes))
		return string(runes[startCol:endCol])
	}
	var parts []string
	if startLine < len(m.logLines) {
		firstRunes := []rune(m.logLines[startLine])
		if startCol < len(firstRunes) {
			parts = append(parts, string(firstRunes[startCol:]))
		}
	}
	for i := startLine + 1; i < endLine && i < len(m.logLines); i++ {
		parts = append(parts, m.logLines[i])
	}
	if endLine < len(m.logLines) {
		lastRunes := []rune(m.logLines[endLine])
		endCol = min(endCol, len(lastRunes))
		parts = append(parts, string(lastRunes[:endCol]))
	}
	return strings.Join(parts, "\n")
}

func (m model) handleMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	if m.mouseSelecting {
		m.mouseSelecting = false
		return m, nil
	}
	return m, nil
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
		case "kill":
			err = c.KillService(project, service)
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
