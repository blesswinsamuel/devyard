package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Shell runs string commands.
const Shell = "sh"

// Command is a process command line. A YAML string is a shell script run
// with `sh -c`; a YAML list is an argv that is executed directly.
type Command struct {
	Script string
	Argv   []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (c *Command) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		c.Script, c.Argv = value.Value, nil
		return nil
	case yaml.SequenceNode:
		var argv []string
		if err := value.Decode(&argv); err != nil {
			return fmt.Errorf("line %d: run: %w", value.Line, err)
		}
		c.Script, c.Argv = "", argv
		return nil
	default:
		return fmt.Errorf("line %d: run must be a string or a list", value.Line)
	}
}

// IsZero reports whether no command is set.
func (c Command) IsZero() bool {
	if len(c.Argv) > 0 {
		return strings.TrimSpace(c.Argv[0]) == ""
	}
	return strings.TrimSpace(c.Script) == ""
}

// Args returns the argv to execute, with extra arguments appended (shell
// quoted for scripts).
func (c Command) Args(extra ...string) []string {
	if len(c.Argv) > 0 {
		return append(append([]string(nil), c.Argv...), extra...)
	}
	script := c.Script
	for _, a := range extra {
		script += " " + ShellQuote(a)
	}
	return []string{Shell, "-c", script}
}

// String renders the command for display.
func (c Command) String() string {
	if len(c.Argv) == 0 {
		return c.Script
	}
	parts := make([]string, len(c.Argv))
	for i, a := range c.Argv {
		parts[i] = ShellQuote(a)
	}
	return strings.Join(parts, " ")
}

// mapStrings returns a copy of c with fn applied to the script or every argv
// element.
func (c Command) mapStrings(fn func(string) (string, error)) (Command, error) {
	if len(c.Argv) == 0 {
		s, err := fn(c.Script)
		return Command{Script: s}, err
	}
	out := Command{Argv: make([]string, len(c.Argv))}
	for i, a := range c.Argv {
		s, err := fn(a)
		if err != nil {
			return Command{}, err
		}
		out.Argv[i] = s
	}
	return out, nil
}

// ShellQuote quotes s for a POSIX shell when needed.
func ShellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
