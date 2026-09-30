// Package logstore stores the output of supervised processes as structured,
// per-run log files and reads them back (tail, paging, follow).
//
// Each process (service or task) has a directory with one file per run:
//
//	<dir>/runs/00000007.log        current segment of run 7
//	<dir>/runs/00000007.s3.log     older segments (size rotation within a run;
//	                               higher numbers are newer)
//
// A run never overwrites another run's file, so "the previous run" is always
// the previous run. Records are single lines:
//
//	<seq> <unix-nanos> <stream> <text>\n
//
// seq starts at 1 and increases by one per record within a run, across size
// rotations, so readers can page and resume precisely.
package logstore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stream identifies where a line came from.
type Stream byte

const (
	Stdout Stream = 'o'
	Stderr Stream = 'e'
	System Stream = 's'
)

// String returns the wire name of the stream.
func (s Stream) String() string {
	switch s {
	case Stdout:
		return "stdout"
	case Stderr:
		return "stderr"
	default:
		return "system"
	}
}

// Line is one log record.
type Line struct {
	Run    int64
	Seq    uint64
	TS     int64 // unix nanoseconds
	Stream Stream
	Text   string
}

const (
	// MaxSegmentBytes is the soft cap of one run segment before rotation.
	MaxSegmentBytes = 10 << 20
	// MaxOldSegments bounds the rotated segments kept per run (together with
	// the current one, a run keeps up to ~90 MiB of history).
	MaxOldSegments = 8
	// MaxLineBytes truncates pathological single lines.
	MaxLineBytes = 1 << 20
	// KeepRuns is how many runs are kept per process.
	KeepRuns = 10
)

// ErrNoRun is returned when a requested run does not exist.
var ErrNoRun = errors.New("logstore: no such run")

func runsDir(dir string) string { return filepath.Join(dir, "runs") }

func segmentPath(dir string, run int64) string {
	return filepath.Join(runsDir(dir), fmt.Sprintf("%08d.log", run))
}

func oldSegmentPath(dir string, run int64, k int) string {
	return filepath.Join(runsDir(dir), fmt.Sprintf("%08d.s%d.log", run, k))
}

// oldSegments lists the rotated segments of run, newest first.
func oldSegments(dir string, run int64) []int {
	matches, _ := filepath.Glob(filepath.Join(runsDir(dir), fmt.Sprintf("%08d.s*.log", run)))
	var ks []int
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".log")
		if i := strings.LastIndex(name, ".s"); i >= 0 {
			if k, err := strconv.Atoi(name[i+2:]); err == nil {
				ks = append(ks, k)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ks)))
	return ks
}

// Runs lists the run numbers stored in dir, ascending.
func Runs(dir string) ([]int64, error) {
	entries, err := os.ReadDir(runsDir(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var runs []int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".old.log") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(name, ".log"), 10, 64)
		if err == nil {
			runs = append(runs, n)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	return runs, nil
}

// LatestRun returns the highest run number in dir, or 0 when there is none.
func LatestRun(dir string) int64 {
	runs, _ := Runs(dir)
	if len(runs) == 0 {
		return 0
	}
	return runs[len(runs)-1]
}

// ResolveRun maps an offset (0 = latest, -1 = the one before, …) to a run.
func ResolveRun(dir string, offset int64) (int64, error) {
	runs, err := Runs(dir)
	if err != nil {
		return 0, err
	}
	idx := int64(len(runs)-1) + offset
	if offset > 0 || idx < 0 || idx >= int64(len(runs)) {
		return 0, ErrNoRun
	}
	return runs[idx], nil
}

// Writer appends records to one run. It is safe for concurrent use.
type Writer struct {
	mu   sync.Mutex
	dir  string
	run  int64
	f    *os.File
	size int64
	seq  uint64
	now  func() time.Time
}

// Create starts a new run in dir and prunes old runs beyond KeepRuns.
func Create(dir string, run int64) (*Writer, error) {
	if err := os.MkdirAll(runsDir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("logstore: %w", err)
	}
	f, err := os.OpenFile(segmentPath(dir, run), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logstore: %w", err)
	}
	prune(dir, run)
	return &Writer{dir: dir, run: run, f: f, now: time.Now}, nil
}

func prune(dir string, current int64) {
	runs, err := Runs(dir)
	if err != nil {
		return
	}
	for _, r := range runs {
		if r <= current-KeepRuns {
			_ = os.Remove(segmentPath(dir, r))
			for _, k := range oldSegments(dir, r) {
				_ = os.Remove(oldSegmentPath(dir, r, k))
			}
		}
	}
}

// Discard returns a Writer that drops every record (for interactive
// terminals, whose output is not persisted).
func Discard() *Writer { return &Writer{} }

// Run returns the run number being written.
func (w *Writer) Run() int64 { return w.run }

// Append writes one record. Text must not contain '\n'.
func (w *Writer) Append(stream Stream, text string) {
	if len(text) > MaxLineBytes {
		text = text[:MaxLineBytes] + " …[truncated]"
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return
	}
	w.seq++
	rec := strconv.FormatUint(w.seq, 10) + " " + strconv.FormatInt(w.now().UnixNano(), 10) + " " + string(rune(stream)) + " " + text + "\n"
	if w.size > 0 && w.size+int64(len(rec)) > MaxSegmentBytes {
		w.rotateLocked()
		if w.f == nil {
			return
		}
	}
	n, _ := w.f.WriteString(rec)
	w.size += int64(n)
}

// Systemf appends a system record.
func (w *Writer) Systemf(format string, args ...any) {
	w.Append(System, fmt.Sprintf(format, args...))
}

func (w *Writer) rotateLocked() {
	_ = w.f.Close()
	next := 1
	if ks := oldSegments(w.dir, w.run); len(ks) > 0 {
		next = ks[0] + 1
		for _, k := range ks {
			if k <= next-MaxOldSegments {
				_ = os.Remove(oldSegmentPath(w.dir, w.run, k))
			}
		}
	}
	_ = os.Rename(segmentPath(w.dir, w.run), oldSegmentPath(w.dir, w.run, next))
	f, err := os.OpenFile(segmentPath(w.dir, w.run), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		w.f = nil
		return
	}
	w.f = f
	w.size = 0
}

// Close closes the writer.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// LineWriter adapts a Writer to io.Writer for one stream, splitting input on
// '\n' and '\r' line endings. Call Flush to emit a trailing partial line.
type LineWriter struct {
	w      *Writer
	stream Stream
	buf    []byte
}

// NewLineWriter returns a LineWriter for stream.
func NewLineWriter(w *Writer, stream Stream) *LineWriter {
	return &LineWriter{w: w, stream: stream}
}

// Write implements io.Writer.
func (l *LineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.w.Append(l.stream, strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > MaxLineBytes {
		l.w.Append(l.stream, string(l.buf))
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

// Flush emits any buffered partial line.
func (l *LineWriter) Flush() {
	if len(l.buf) > 0 {
		l.w.Append(l.stream, strings.TrimRight(string(l.buf), "\r"))
		l.buf = l.buf[:0]
	}
}

// parseLine parses one record. ok is false for malformed lines.
func parseLine(run int64, b []byte) (Line, bool) {
	s := string(bytes.TrimRight(b, "\n"))
	seqEnd := strings.IndexByte(s, ' ')
	if seqEnd <= 0 {
		return Line{}, false
	}
	seq, err := strconv.ParseUint(s[:seqEnd], 10, 64)
	if err != nil {
		return Line{}, false
	}
	rest := s[seqEnd+1:]
	tsEnd := strings.IndexByte(rest, ' ')
	if tsEnd <= 0 {
		return Line{}, false
	}
	ts, err := strconv.ParseInt(rest[:tsEnd], 10, 64)
	if err != nil {
		return Line{}, false
	}
	rest = rest[tsEnd+1:]
	if len(rest) < 1 {
		return Line{}, false
	}
	stream := Stream(rest[0])
	text := ""
	if len(rest) >= 2 {
		text = rest[2:]
	}
	return Line{Run: run, Seq: seq, TS: ts, Stream: stream, Text: text}, true
}

// Tail returns up to n of the last lines of run with seq < before (before = 0
// means no bound). more reports whether earlier lines exist. n <= 0 returns
// every matching line.
func Tail(dir string, run int64, n int, before uint64) (lines []Line, more bool, err error) {
	var collected []Line // newest first
	paths := []string{segmentPath(dir, run)}
	for _, k := range oldSegments(dir, run) {
		paths = append(paths, oldSegmentPath(dir, run, k))
	}
	for _, path := range paths {
		if n > 0 && len(collected) >= n {
			more = true
			break
		}
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if path == segmentPath(dir, run) {
					return nil, false, ErrNoRun
				}
				continue
			}
			return nil, false, err
		}
		stop := false
		err = scanBackward(f, func(b []byte) bool {
			l, ok := parseLine(run, b)
			if !ok {
				return true
			}
			if before > 0 && l.Seq >= before {
				return true
			}
			if n > 0 && len(collected) >= n {
				more = true
				stop = true
				return false
			}
			collected = append(collected, l)
			return true
		})
		_ = f.Close()
		if err != nil {
			return nil, false, err
		}
		if stop {
			break
		}
	}
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	return collected, more, nil
}

// scanBackward calls fn for each complete line of f from the end towards the
// start until fn returns false.
func scanBackward(f *os.File, fn func([]byte) bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	const block = 64 << 10
	pos := info.Size()
	var carry []byte
	for pos > 0 {
		size := int64(block)
		if pos < size {
			size = pos
		}
		pos -= size
		chunk := make([]byte, size, size+int64(len(carry)))
		if _, err := f.ReadAt(chunk, pos); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		chunk = append(chunk, carry...)
		// Emit complete lines from the end of chunk.
		end := len(chunk)
		for {
			i := bytes.LastIndexByte(chunk[:end], '\n')
			if i < 0 {
				break
			}
			if line := chunk[i+1 : end]; len(line) > 0 && !fn(line) {
				return nil
			}
			end = i
		}
		carry = append([]byte(nil), chunk[:end]...)
	}
	if len(carry) > 0 {
		fn(carry)
	}
	return nil
}

// Follower streams new records of a process's runs as they are written. It
// follows the given run and, when newer runs appear, moves on to them after
// draining the old one completely.
type Follower struct {
	dir      string
	run      int64
	afterSeq uint64
	poll     time.Duration

	f       *os.File
	reader  *bufio.Reader
	partial []byte
	pending []Line
}

// NewFollower follows run starting after afterSeq.
func NewFollower(dir string, run int64, afterSeq uint64) *Follower {
	return &Follower{dir: dir, run: run, afterSeq: afterSeq, poll: 100 * time.Millisecond}
}

// Run is the run currently being followed.
func (fl *Follower) Run() int64 { return fl.run }

// Close releases the follower's file.
func (fl *Follower) Close() {
	if fl.f != nil {
		_ = fl.f.Close()
		fl.f = nil
	}
}

// Poll reads everything new. It returns the new lines and newRun=true when
// the follower moved to a newer run during this call.
func (fl *Follower) Poll() (lines []Line, newRun bool, err error) {
	for {
		if fl.f == nil {
			if err := fl.open(); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// Run file not created yet (or pruned): try the next run.
					if next := fl.nextRun(); next > 0 {
						fl.switchRun(next)
						newRun = true
						continue
					}
					return lines, newRun, nil
				}
				return lines, newRun, err
			}
		}
		got, err := fl.readAvailable()
		if err != nil {
			return lines, newRun, err
		}
		lines = append(lines, got...)
		// The current segment was rotated away: finish reading the old fd
		// (done above) and reopen the fresh segment of the same run.
		if fl.rotated() {
			fl.Close()
			continue
		}
		if next := fl.nextRun(); next > 0 {
			// Drain once more before switching; the old run is finished.
			got, err := fl.readAvailable()
			if err != nil {
				return lines, newRun, err
			}
			lines = append(lines, got...)
			fl.switchRun(next)
			newRun = true
			continue
		}
		return lines, newRun, nil
	}
}

func (fl *Follower) switchRun(run int64) {
	fl.Close()
	fl.run = run
	fl.afterSeq = 0
	fl.partial = nil
	fl.pending = nil
}

func (fl *Follower) nextRun() int64 {
	latest := LatestRun(fl.dir)
	if latest > fl.run {
		runs, _ := Runs(fl.dir)
		for _, r := range runs {
			if r > fl.run {
				return r
			}
		}
	}
	return 0
}

func (fl *Follower) open() error {
	f, err := os.Open(segmentPath(fl.dir, fl.run))
	if err != nil {
		return err
	}
	fl.f = f
	fl.reader = bufio.NewReaderSize(f, 64<<10)
	fl.partial = nil
	// A size rotation may have moved lines we have not seen yet into the
	// .old segment; emit those first.
	if fl.afterSeq > 0 {
		ks := oldSegments(fl.dir, fl.run)
		for i := len(ks) - 1; i >= 0; i-- { // oldest first
			old, err := os.Open(oldSegmentPath(fl.dir, fl.run, ks[i]))
			if err != nil {
				continue
			}
			sc := bufio.NewReaderSize(old, 64<<10)
			for {
				b, err := sc.ReadBytes('\n')
				if l, ok := parseLine(fl.run, b); ok && len(b) > 0 && b[len(b)-1] == '\n' && l.Seq > fl.afterSeq {
					fl.pending = append(fl.pending, l)
				}
				if err != nil {
					break
				}
			}
			_ = old.Close()
		}
	}
	return nil
}

// rotated reports whether the file we hold is no longer the run's current
// segment (it was renamed to .old).
func (fl *Follower) rotated() bool {
	if fl.f == nil {
		return false
	}
	held, err := fl.f.Stat()
	if err != nil {
		return true
	}
	cur, err := os.Stat(segmentPath(fl.dir, fl.run))
	if err != nil {
		return false
	}
	return !os.SameFile(held, cur)
}

func (fl *Follower) readAvailable() ([]Line, error) {
	var lines []Line
	if len(fl.pending) > 0 {
		lines = append(lines, fl.pending...)
		fl.afterSeq = fl.pending[len(fl.pending)-1].Seq
		fl.pending = nil
	}
	for {
		chunk, err := fl.reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if chunk[len(chunk)-1] != '\n' {
				fl.partial = append(fl.partial, chunk...)
			} else {
				full := chunk
				if len(fl.partial) > 0 {
					full = append(fl.partial, chunk...)
					fl.partial = nil
				}
				if l, ok := parseLine(fl.run, full); ok && l.Seq > fl.afterSeq {
					fl.afterSeq = l.Seq
					lines = append(lines, l)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
	}
}

// PollInterval is how often callers should call Poll while following.
func (fl *Follower) PollInterval() time.Duration { return fl.poll }
