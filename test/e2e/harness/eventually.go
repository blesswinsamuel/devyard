package harness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TB is the subset of testing.TB the harness needs. *testing.T satisfies it;
// cmd/sandbox provides its own implementation.
type TB interface {
	Helper()
	Cleanup(func())
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	Name() string
	Failed() bool
}

// DefaultWait is the base deadline for Eventually/WaitFor before scaling.
const DefaultWait = 10 * time.Second

// Scale multiplies d by the run's deadline factor: 3x under -race, and
// DEVYARD_E2E_TIMEOUT_SCALE (a float) on top when set.
func Scale(d time.Duration) time.Duration {
	f := 1.0
	if RaceEnabled {
		f = 3
	}
	if s := os.Getenv("DEVYARD_E2E_TIMEOUT_SCALE"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			f *= v
		}
	}
	return time.Duration(float64(d) * f)
}

// C collects the failures of one Eventually attempt.
type C struct {
	msgs []string
}

type attemptAbort struct{}

// Errorf records a failure for this attempt; the attempt still runs to the end.
func (c *C) Errorf(format string, args ...any) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

// Fatalf records a failure and aborts this attempt.
func (c *C) Fatalf(format string, args ...any) {
	c.Errorf(format, args...)
	panic(attemptAbort{})
}

// Failed reports whether this attempt has recorded a failure.
func (c *C) Failed() bool { return len(c.msgs) > 0 }

func (c *C) run(f func(*C)) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isAbort := r.(attemptAbort); !isAbort {
				panic(r)
			}
			ok = false
		}
	}()
	f(c)
	return !c.Failed()
}

// EventuallyOpt customizes Eventually.
type EventuallyOpt func(*eventuallyCfg)

type eventuallyCfg struct {
	timeout  time.Duration
	interval time.Duration
	diag     func() string
}

// Within sets the (unscaled) deadline.
func Within(d time.Duration) EventuallyOpt {
	return func(c *eventuallyCfg) { c.timeout = d }
}

// Every sets the poll interval.
func Every(d time.Duration) EventuallyOpt {
	return func(c *eventuallyCfg) { c.interval = d }
}

// WithDiagnostics appends the output of diag to the failure message.
func WithDiagnostics(diag func() string) EventuallyOpt {
	return func(c *eventuallyCfg) { c.diag = diag }
}

// Eventually polls f until an attempt records no failures or the (scaled)
// deadline passes; then it fails t with desc and the last attempt's errors.
func Eventually(t TB, desc string, f func(c *C), opts ...EventuallyOpt) {
	t.Helper()
	cfg := eventuallyCfg{timeout: DefaultWait, interval: 50 * time.Millisecond}
	for _, o := range opts {
		o(&cfg)
	}
	timeout := Scale(cfg.timeout)
	start := time.Now()
	deadline := start.Add(timeout)
	attempts := 0
	var last *C
	for {
		attempts++
		c := &C{}
		if c.run(f) {
			return
		}
		last = c
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(cfg.interval)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "eventually %q: not satisfied after %s (%d attempts)\nlast attempt:\n", desc, time.Since(start).Round(time.Millisecond), attempts)
	for _, m := range last.msgs {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	if cfg.diag != nil {
		b.WriteString(cfg.diag())
	}
	t.Fatalf("%s", b.String())
}

// Consistently fails t as soon as an attempt of f records a failure within
// d (scaled): the condition must hold for the whole window.
func Consistently(t TB, desc string, d time.Duration, f func(c *C)) {
	t.Helper()
	deadline := time.Now().Add(Scale(d))
	for {
		c := &C{}
		if !c.run(f) {
			t.Fatalf("consistently %q: violated: %s", desc, strings.Join(c.msgs, "; "))
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
