package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/blesswinsamuel/local-compose/internal/ui"
)

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
	prefixStr := ui.ServicePrefix(name)
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
		_, _ = fmt.Fprintf(l.stdout, "%s │ %s\n", l.prefixStr, ui.CleanLogLine(line))
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
