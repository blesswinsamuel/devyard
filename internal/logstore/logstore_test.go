package logstore

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func writeRun(t *testing.T, dir string, run int64, n int) *Writer {
	t.Helper()
	w, err := Create(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		w.Append(Stdout, fmt.Sprintf("line %d", i))
	}
	return w
}

func TestTailAndPaging(t *testing.T) {
	dir := t.TempDir()
	w := writeRun(t, dir, 1, 100)
	defer func() { _ = w.Close() }()

	lines, more, err := Tail(dir, 1, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 10 || !more || lines[0].Text != "line 91" || lines[9].Text != "line 100" || lines[9].Seq != 100 {
		t.Fatalf("tail: %d more=%v first=%+v last=%+v", len(lines), more, lines[0], lines[len(lines)-1])
	}
	page, more, err := Tail(dir, 1, 10, lines[0].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 10 || page[0].Text != "line 81" || page[9].Text != "line 90" || !more {
		t.Fatalf("page: %+v", page)
	}
	all, more, err := Tail(dir, 1, 0, 0)
	if err != nil || len(all) != 100 || more {
		t.Fatalf("all: %d %v %v", len(all), more, err)
	}
	if _, _, err := Tail(dir, 9, 10, 0); err != ErrNoRun {
		t.Fatalf("missing run err = %v", err)
	}
}

func TestLongLinesAcrossBlocks(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 200<<10)
	w.Append(Stdout, "a")
	w.Append(Stderr, long)
	w.Append(System, "c")
	_ = w.Close()
	lines, _, err := Tail(dir, 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[1].Text != long || lines[1].Stream != Stderr || lines[2].Stream != System {
		t.Fatalf("got %d lines", len(lines))
	}
}

func TestRunsAndResolve(t *testing.T) {
	dir := t.TempDir()
	for r := int64(1); r <= 3; r++ {
		_ = writeRun(t, dir, r, 1).Close()
	}
	if r, err := ResolveRun(dir, 0); err != nil || r != 3 {
		t.Fatalf("offset 0: %d %v", r, err)
	}
	if r, err := ResolveRun(dir, -2); err != nil || r != 1 {
		t.Fatalf("offset -2: %d %v", r, err)
	}
	if _, err := ResolveRun(dir, -3); err != ErrNoRun {
		t.Fatalf("offset -3: %v", err)
	}
}

func TestPruneKeepsRecentRuns(t *testing.T) {
	dir := t.TempDir()
	for r := int64(1); r <= KeepRuns+3; r++ {
		_ = writeRun(t, dir, r, 1).Close()
	}
	runs, _ := Runs(dir)
	if len(runs) != KeepRuns || runs[0] != 4 {
		t.Fatalf("runs after prune: %v", runs)
	}
}

func TestFollowerRotationWithinRun(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	fl := NewFollower(dir, 1, 0)
	defer fl.Close()

	w.Append(Stdout, "before")
	got, _, err := fl.Poll()
	if err != nil || len(got) != 1 {
		t.Fatalf("first poll: %v %v", got, err)
	}
	// Force a rotation, then write more; the follower must not lose lines
	// written to the old segment after its last poll.
	w.Append(Stdout, "tail-of-old")
	w.mu.Lock()
	w.rotateLocked()
	w.mu.Unlock()
	w.Append(Stdout, "fresh")
	got, _, err = fl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range got {
		texts = append(texts, l.Text)
	}
	if strings.Join(texts, ",") != "tail-of-old,fresh" {
		t.Fatalf("after rotation got %v", texts)
	}
	if _, err := os.Stat(oldSegmentPath(dir, 1, 1)); err != nil {
		t.Fatalf("expected old segment: %v", err)
	}
}

func TestFollowerMovesToNewRunAfterDraining(t *testing.T) {
	dir := t.TempDir()
	w1 := writeRun(t, dir, 1, 2)
	fl := NewFollower(dir, 1, 0)
	defer fl.Close()
	if got, _, _ := fl.Poll(); len(got) != 2 {
		t.Fatalf("initial: %v", got)
	}
	w1.Append(System, "exited")
	_ = w1.Close()
	w2 := writeRun(t, dir, 2, 1)
	defer func() { _ = w2.Close() }()
	got, newRun, err := fl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !newRun || len(got) != 2 || got[0].Text != "exited" || got[0].Run != 1 || got[1].Run != 2 {
		t.Fatalf("got newRun=%v %+v", newRun, got)
	}
}

func TestFollowerStartsBeforeRunExists(t *testing.T) {
	dir := t.TempDir()
	fl := NewFollower(dir, 0, 0)
	defer fl.Close()
	if got, _, err := fl.Poll(); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	w := writeRun(t, dir, 1, 1)
	defer func() { _ = w.Close() }()
	got, newRun, err := fl.Poll()
	if err != nil || !newRun || len(got) != 1 {
		t.Fatalf("got %v %v %v", got, newRun, err)
	}
}

func TestLineWriter(t *testing.T) {
	dir := t.TempDir()
	w, _ := Create(dir, 1)
	lw := NewLineWriter(w, Stdout)
	_, _ = lw.Write([]byte("a\r\nb"))
	_, _ = lw.Write([]byte("c\n"))
	_, _ = lw.Write([]byte("partial"))
	lw.Flush()
	_ = w.Close()
	lines, _, _ := Tail(dir, 1, 0, 0)
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.Text)
	}
	if strings.Join(texts, "|") != "a|bc|partial" {
		t.Fatalf("got %v", texts)
	}
}

func TestManyRotationsKeepHistory(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ {
		w.Append(Stdout, fmt.Sprintf("line %d", i))
		if i%10 == 0 {
			w.mu.Lock()
			w.rotateLocked()
			w.mu.Unlock()
		}
	}
	_ = w.Close()
	lines, _, err := Tail(dir, 1, 0, 0)
	if err != nil || len(lines) != 50 || lines[0].Seq != 1 || lines[49].Seq != 50 {
		t.Fatalf("got %d lines err %v", len(lines), err)
	}
	page, more, _ := Tail(dir, 1, 15, 20)
	if len(page) != 15 || page[0].Seq != 5 || !more {
		t.Fatalf("page: %d first %d more %v", len(page), page[0].Seq, more)
	}
}
