package harness

import (
	"bytes"
	"fmt"
	"regexp"
	"sync"
	"time"
)

// ansiRE matches CSI/OSC escape sequences so Expect can match text that a
// TTY interleaved with cursor movement or colors.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>]`)

// StripANSI removes terminal escape sequences and carriage returns.
func StripANSI(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	return string(bytes.ReplaceAll([]byte(s), []byte("\r"), nil))
}

// outputBuffer accumulates interactive output and supports expect-style
// matching with a cursor that advances past each match.
type outputBuffer struct {
	mu     sync.Mutex
	data   []byte
	cursor int
	closed bool
	notify chan struct{}
}

func newOutputBuffer() *outputBuffer {
	return &outputBuffer{notify: make(chan struct{})}
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	ch := b.notify
	b.notify = make(chan struct{})
	b.mu.Unlock()
	close(ch)
	return len(p), nil
}

func (b *outputBuffer) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	ch := b.notify
	b.notify = make(chan struct{})
	b.mu.Unlock()
	close(ch)
}

func (b *outputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func (b *outputBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.data)
}

// expect waits until the text after the cursor contains substr, then
// advances the cursor past the match. It rescans only new data (plus an
// overlap), and matches ANSI-stripped text within a bounded tail window, so
// large outputs stay linear.
func (b *outputBuffer) expect(substr string, timeout time.Duration) error {
	const window = 64 << 10
	deadline := time.After(timeout)
	needle := []byte(substr)
	scanned := -1
	for {
		b.mu.Lock()
		from := b.cursor
		if scanned >= 0 && scanned-len(needle) > from {
			from = scanned - len(needle)
		}
		if i := bytes.Index(b.data[from:], needle); i >= 0 {
			b.cursor = from + i + len(needle)
			b.mu.Unlock()
			return nil
		}
		wstart := len(b.data) - window
		if wstart < b.cursor {
			wstart = b.cursor
		}
		if bytes.Contains([]byte(StripANSI(string(b.data[wstart:]))), needle) {
			b.cursor = len(b.data)
			b.mu.Unlock()
			return nil
		}
		scanned = len(b.data)
		closed := b.closed
		ch := b.notify
		b.mu.Unlock()
		if closed {
			return fmt.Errorf("output closed before %q appeared; unmatched output:\n%q", substr, b.unmatched())
		}
		select {
		case <-ch:
		case <-deadline:
			return fmt.Errorf("timed out after %s waiting for %q; unmatched output:\n%q", timeout, substr, b.unmatched())
		}
	}
}

func (b *outputBuffer) unmatched() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := b.data[b.cursor:]
	if len(pending) > 4000 {
		return fmt.Sprintf("...(%d bytes elided)...", len(pending)-4000) + string(pending[len(pending)-4000:])
	}
	return string(pending)
}
