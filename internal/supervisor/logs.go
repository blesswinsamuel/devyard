package supervisor

import (
	"bufio"
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

// prefixColors is the per-service color palette used for foreground prefixes.
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

// serviceLogger appends a service's output to a log file and, in foreground
// mode, also writes prefixed colored lines to stdout.
type serviceLogger struct {
	mu         sync.Mutex
	file       *os.File
	stdout     io.Writer
	prefix     string
	prefixStr  string
	foreground bool
}

func newServiceLogger(path, name string, stdout io.Writer, foreground bool) (*serviceLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	color := prefixColors[absHash(name)%len(prefixColors)]
	prefixStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	prefixStr := prefixStyle.Render(name)
	return &serviceLogger{
		file:       f,
		stdout:     stdout,
		prefix:     name,
		prefixStr:  prefixStr,
		foreground: foreground,
	}, nil
}

// writeLine writes one logical line (without trailing newline) to the log
// file and, in foreground mode, to stdout with the colored prefix.
func (l *serviceLogger) writeLine(line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.WriteString(line + "\n"); err != nil {
		// Best-effort; a full disk shouldn't take down supervision.
		_ = err
	}
	if l.foreground {
		_, _ = fmt.Fprintf(l.stdout, "%s │ %s\n", l.prefixStr, line)
	}
}

func (l *serviceLogger) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// readLines copies r to the logger line-by-line. A final line without a
// trailing newline is still emitted.
func (s *Supervisor) readLines(r io.Reader, rt *serviceRuntime) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			rt.logger.writeLine(strings.TrimRight(line, "\n"))
		}
		if err != nil {
			return
		}
	}
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
