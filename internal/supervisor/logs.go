package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/ui"
)

// LogTimestampFormat is the fixed format used for timestamps in log files.
// It uses RFC3339Nano in UTC so lines sort chronologically by string
// comparison and are easy to parse back.
const LogTimestampFormat = time.RFC3339Nano

// ParseTimestamp strips a LogTimestampFormat prefix from a line and returns the
// parsed time and the remainder. If the line has no recognizable prefix the
// zero time is returned and the original line is the remainder.
func ParseTimestamp(line string) (time.Time, string) {
	if len(line) < 20 {
		return time.Time{}, line
	}
	// Scan for the space separator after a valid RFC3339 timestamp.
	for i := 19; i < len(line) && i < 35; i++ {
		if line[i] == ' ' {
			ts, err := time.Parse(LogTimestampFormat, line[:i])
			if err == nil {
				return ts, line[i+1:]
			}
			break
		}
	}
	return time.Time{}, line
}

// previousLogPath derives a service's previous-run log path from its current
// one by swapping the ".log" suffix for ".prev.log" (e.g. "api.log" ->
// "api.prev.log"). Both are constructed from the service name elsewhere, so
// the ".log" suffix is a fixed convention.
func previousLogPath(path string) string {
	return strings.TrimSuffix(path, ".log") + ".prev.log"
}

// serviceLogger appends a service's output to a log file and, in foreground
// mode, also writes prefixed colored lines to stdout. One logger is created
// per service in openLoggers and reused across restarts; rotate() moves the
// finished run's file aside so the current file always holds one run.
type serviceLogger struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	prevPath   string
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
		path:       path,
		prevPath:   previousLogPath(path),
		stdout:     stdout,
		prefix:     name,
		prefixStr:  prefixStr,
		foreground: foreground,
	}, nil
}

// rotate moves the current log to the previous-run file (overwriting the prior
// previous run) and starts a fresh current log. It is called once per
// successful process spawn so the current file holds exactly one run and the
// previous file holds the run before it. If the current file is empty (a
// freshly opened logger, or a run that produced no output) the existing
// previous run is kept. Best-effort: a failure leaves the logger pointing at
// the previous content rather than taking supervision down.
func (l *serviceLogger) rotate() {
	if l == nil || l.file == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if info, err := l.file.Stat(); err == nil && info.Size() == 0 {
		return
	}
	_ = l.file.Close()
	l.file = nil
	if err := os.Rename(l.path, l.prevPath); err != nil && !os.IsNotExist(err) {
		// Rename failed (e.g. permissions); truncate so the new run still
		// starts from a clean slate.
		_ = os.Truncate(l.path, 0)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	l.file = f
}

// writeLine writes one logical line (without trailing newline) to the log
// file and, in foreground mode, to stdout with the colored prefix.
func (l *serviceLogger) writeLine(line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().UTC().Format(LogTimestampFormat)
	if l.file != nil {
		if _, err := l.file.WriteString(ts + " " + line + "\n"); err != nil {
			// Best-effort; a full disk shouldn't take down supervision.
			_ = err
		}
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
