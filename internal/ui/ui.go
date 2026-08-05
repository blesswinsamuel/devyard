package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// TerminalWidth returns the current terminal width, or a sensible default.
func TerminalWidth() int {
	w, _, err := term.GetSize(0)
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// DefaultStyle returns the base lipgloss style used across frontends.
func DefaultStyle() lipgloss.Style {
	return lipgloss.NewStyle()
}

// StatusColor maps a supervisor status string to a representative color shared
// by the CLI table and web frontend so lifecycle states read consistently
// everywhere.
func StatusColor(status string) color.Color {
	switch status {
	case "running":
		return lipgloss.Color("42") // green
	case "starting":
		return lipgloss.Color("214") // amber
	case "stopping":
		return lipgloss.Color("214") // amber
	case "backoff":
		return lipgloss.Color("202") // orange-red
	case "exited":
		return lipgloss.Color("196") // red
	case "stopped":
		return lipgloss.Color("243") // dimmer grey
	default:
		return lipgloss.Color("250")
	}
}

// HealthColor maps a health state string to a color. Unknown/empty values get a
// neutral grey so a missing healthcheck never looks like a failure.
func HealthColor(health string) color.Color {
	switch health {
	case "healthy":
		return lipgloss.Color("42")
	case "unhealthy":
		return lipgloss.Color("196")
	case "starting", "starting_healthy":
		return lipgloss.Color("214")
	case "n/a", "":
		return lipgloss.Color("245")
	default:
		return lipgloss.Color("250")
	}
}

// HealthLabel renders the health column text for a service snapshot. Services
// without a healthcheck show "-" so the column is never empty.
func HealthLabel(hasHealth bool, health string) string {
	if !hasHealth {
		return "-"
	}
	if health == "" {
		return "n/a"
	}
	return health
}

// prefixColors is the per-service color palette used for log prefixes.
var prefixColors = []color.Color{
	lipgloss.Color("203"),
	lipgloss.Color("39"),
	lipgloss.Color("84"),
	lipgloss.Color("213"),
	lipgloss.Color("219"),
	lipgloss.Color("171"),
	lipgloss.Color("141"),
	lipgloss.Color("117"),
}

// ServicePrefix renders a colored, bold service name suitable for log line
// prefixes. The color is deterministically chosen from a palette by hashing the
// service name.
func ServicePrefix(name string) string {
	c := prefixColors[absHash(name)%len(prefixColors)]
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(name)
}

func absHash(s string) int {
	h := 0
	for _, c := range s {
		h += int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

// PIDLabel renders the pid column text, "-" when the service has no live pid.
func PIDLabel(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return itoa(pid)
}

// StatusLabel renders the status text for a service. When the service has
// exited with a non-zero code the code is appended so the user can see it
// at a glance.
func StatusLabel(status string, exitCode int) string {
	if status == "exited" && exitCode != 0 {
		return fmt.Sprintf("exited(%d)", exitCode)
	}
	return status
}

// StatusMessage renders a colored status label suitable for informational log
// lines (e.g. "exited at ...", "stopped at ..."). The status text is colored
// according to StatusColor so lifecycle events are visually distinct.
func StatusMessage(status string) string {
	c := StatusColor(status)
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(status)
}

// Dim renders text in a dim grey style, useful for de-emphasizing metadata
// like timestamps and prefixes in log messages.
func Dim(text string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(text)
}

// itoa avoids pulling strconv into every frontend just for pid formatting; it
// is only ever called on small non-negative values.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// CleanLogLine removes ANSI control sequences (cursor movement, erase, carriage
// returns, etc.) that corrupt terminal output, while preserving SGR sequences
// (colors, bold, etc.) so log output retains its styling.
func CleanLogLine(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	var state byte
	p := ansi.NewParser()
	for len(s) > 0 {
		seq, _, n, newState := ansi.DecodeSequence(s, state, p)
		if newState == ansi.NormalState && len(seq) > 0 {
			if seq[0] == '\x1b' && ansi.HasCsiPrefix(seq) && ansi.Cmd(p.Command()).Final() == 'm' {
				buf.WriteString(seq) // preserve SGR (color) sequences
			} else if newState == ansi.NormalState && len(seq) == 1 && seq[0] >= ' ' {
				buf.WriteString(seq) // preserve printable characters
			}
		}
		s = s[n:]
		state = newState
	}
	return buf.String()
}
