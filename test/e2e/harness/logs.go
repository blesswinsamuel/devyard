package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

// Svc is a service log source.
func Svc(name string) *v1.LogSource { return &v1.LogSource{Kind: "service", Name: name} }

// TaskSrc is a task log source.
func TaskSrc(name string) *v1.LogSource { return &v1.LogSource{Kind: "task", Name: name} }

func collectLogs(ctx context.Context, client devyardv1connect.DaemonServiceClient, req *v1.LogsRequest) ([]*v1.LogLine, error) {
	req.Follow = false
	stream, err := client.Logs(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	var lines []*v1.LogLine
	for stream.Receive() {
		lines = append(lines, stream.Msg().GetLines()...)
	}
	return lines, stream.Err()
}

// LogsResult is a non-following Logs call.
type LogsResult struct {
	Lines         []*v1.LogLine
	HasMoreBefore bool
}

// Logs reads history (follow=false) and fails t on error.
func (d *Daemon) Logs(req *v1.LogsRequest) []*v1.LogLine {
	d.sb.t.Helper()
	return d.LogsFull(req).Lines
}

// LogsFull is Logs that also returns the paging flag.
func (d *Daemon) LogsFull(req *v1.LogsRequest) LogsResult {
	d.sb.t.Helper()
	ctx, cancel := context.WithTimeout(d.sb.ctx, Scale(20*time.Second))
	defer cancel()
	req.Follow = false
	stream, err := d.Client().Logs(ctx, connect.NewRequest(req))
	if err != nil {
		d.sb.t.Fatalf("Logs(%v): %v", req, err)
	}
	defer func() { _ = stream.Close() }()
	var res LogsResult
	first := true
	for stream.Receive() {
		msg := stream.Msg()
		if first {
			res.HasMoreBefore = msg.GetHasMoreBefore()
			first = false
		}
		res.Lines = append(res.Lines, msg.GetLines()...)
	}
	if err := stream.Err(); err != nil {
		d.sb.t.Fatalf("Logs(%v) stream: %v", req, err)
	}
	return res
}

// LogText joins the text of lines with newlines.
func LogText(lines []*v1.LogLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.GetText())
		b.WriteByte('\n')
	}
	return b.String()
}

// LogFollower is a following Logs stream.
type LogFollower struct {
	sb      *Sandbox
	cancel  context.CancelFunc
	done    chan struct{}
	mu      sync.Mutex
	lines   []*v1.LogLine
	newRuns []*v1.LogSource
	err     error
	notify  chan struct{}
}

// FollowLogs opens a following Logs stream; it's closed at cleanup.
func (d *Daemon) FollowLogs(req *v1.LogsRequest) *LogFollower {
	d.sb.t.Helper()
	ctx, cancel := context.WithCancel(d.sb.ctx)
	req.Follow = true
	f := &LogFollower{sb: d.sb, cancel: cancel, done: make(chan struct{}), notify: make(chan struct{})}
	stream, err := d.Client().Logs(ctx, connect.NewRequest(req))
	if err != nil {
		cancel()
		d.sb.t.Fatalf("Logs(follow): %v", err)
	}
	go func() {
		defer close(f.done)
		for stream.Receive() {
			msg := stream.Msg()
			f.mu.Lock()
			f.lines = append(f.lines, msg.GetLines()...)
			f.newRuns = append(f.newRuns, msg.GetNewRun()...)
			ch := f.notify
			f.notify = make(chan struct{})
			f.mu.Unlock()
			close(ch)
		}
		f.mu.Lock()
		f.err = stream.Err()
		ch := f.notify
		f.notify = make(chan struct{})
		f.mu.Unlock()
		close(ch)
		_ = stream.Close()
	}()
	d.sb.t.Cleanup(f.Close)
	return f
}

// Close ends the stream.
func (f *LogFollower) Close() {
	f.cancel()
	<-f.done
}

// Lines returns everything received so far.
func (f *LogFollower) Lines() []*v1.LogLine {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*v1.LogLine(nil), f.lines...)
}

// NewRuns returns the sources reported as having switched runs.
func (f *LogFollower) NewRuns() []*v1.LogSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*v1.LogSource(nil), f.newRuns...)
}

// Ended reports whether the stream ended, and its error.
func (f *LogFollower) Ended() (bool, error) {
	select {
	case <-f.done:
		f.mu.Lock()
		defer f.mu.Unlock()
		return true, f.err
	default:
		return false, nil
	}
}

// WaitFor waits until pred holds for the received lines.
func (f *LogFollower) WaitFor(t TB, desc string, pred func([]*v1.LogLine) bool) []*v1.LogLine {
	t.Helper()
	return f.WaitForWithin(t, DefaultWait, desc, pred)
}

// WaitForWithin is WaitFor with an explicit (unscaled) timeout.
func (f *LogFollower) WaitForWithin(t TB, d time.Duration, desc string, pred func([]*v1.LogLine) bool) []*v1.LogLine {
	t.Helper()
	deadline := time.After(Scale(d))
	for {
		f.mu.Lock()
		lines := append([]*v1.LogLine(nil), f.lines...)
		ch := f.notify
		err := f.err
		f.mu.Unlock()
		if pred(lines) {
			return lines
		}
		select {
		case <-f.done:
			if !pred(f.Lines()) {
				t.Fatalf("logs stream ended (err=%v) before %s; received %d lines:\n%s", err, desc, len(lines), FormatLogLines(tailLogLines(lines, 40)))
			}
			return f.Lines()
		case <-ch:
		case <-deadline:
			t.Fatalf("logs: timed out after %s waiting for %s; received %d lines:\n%s\n%s", Scale(d), desc, len(lines), FormatLogLines(tailLogLines(lines, 40)), f.sb.daemonLogDiag())
			return lines
		}
	}
}

// WaitForText waits until some received line contains substr.
func (f *LogFollower) WaitForText(t TB, substr string) []*v1.LogLine {
	t.Helper()
	return f.WaitFor(t, fmt.Sprintf("a line containing %q", substr), func(lines []*v1.LogLine) bool {
		return ContainsText(lines, substr)
	})
}

// ContainsText reports whether any line contains substr.
func ContainsText(lines []*v1.LogLine, substr string) bool {
	for _, l := range lines {
		if strings.Contains(l.GetText(), substr) {
			return true
		}
	}
	return false
}

func tailLogLines(lines []*v1.LogLine, n int) []*v1.LogLine {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// WaitLog waits until the current run of a source has logged a line
// containing substr. Use it to wait for a fixture's readiness line: a
// service is "running" as soon as it is spawned, before e.g. its signal
// handlers are installed.
func (d *Daemon) WaitLog(project string, source *v1.LogSource, substr string) {
	d.sb.t.Helper()
	Eventually(d.sb.t, fmt.Sprintf("%s/%s logged %q", project, source.GetName(), substr), func(c *C) {
		ctx, cancel := context.WithTimeout(d.sb.ctx, 5*time.Second)
		defer cancel()
		lines, err := collectLogs(ctx, d.Client(), &v1.LogsRequest{Project: project, Sources: []*v1.LogSource{source}})
		if err != nil {
			c.Fatalf("Logs: %v", err)
		}
		if !ContainsText(lines, substr) {
			c.Errorf("not yet; tail:\n%s", FormatLogLines(tailLogLines(lines, 10)))
		}
	}, Within(20*time.Second))
}
