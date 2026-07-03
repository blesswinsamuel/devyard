package ui

import (
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
