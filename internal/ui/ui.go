package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
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
// by the CLI table, TUI, and web frontends so lifecycle states read
// consistently everywhere.
func StatusColor(status string) color.Color {
	switch status {
	case "running":
		return lipgloss.Color("42") // green
	case "starting":
		return lipgloss.Color("214") // amber
	case "backoff":
		return lipgloss.Color("202") // orange-red
	case "exited":
		return lipgloss.Color("245") // grey
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

// PIDLabel renders the pid column text, "-" when the service has no live pid.
func PIDLabel(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return itoa(pid)
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
