package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

func writeSumFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setSumMtime forces a modification time far from "now" so a same-size edit is
// still seen as a change on filesystems with coarse timestamps.
func setSumMtime(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestSumCacheMatchesSumFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "devyard.yml")
	writeSumFile(t, path, "a: 1\n")
	var c config.SumCache
	if got, want := c.Sum(path), config.SumFile(path); got != want {
		t.Fatalf("Sum = %q, want %q", got, want)
	}
	// Repeated reads of an unchanged file are stable.
	if got := c.Sum(path); got != c.Sum(path) {
		t.Fatalf("unstable sum: %q", got)
	}
}

func TestSumCacheNoticesEdits(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "devyard.yml")
	writeSumFile(t, path, "a: 1\n")
	var c config.SumCache
	first := c.Sum(path)

	// A size change is noticed.
	writeSumFile(t, path, "a: 2\nmore\n")
	second := c.Sum(path)
	if second == first {
		t.Fatalf("sum after size change = %q, unchanged", second)
	}

	// A same-size edit is noticed once the modification time moves.
	writeSumFile(t, path, "a: 3\nmore\n")
	setSumMtime(t, path)
	if third := c.Sum(path); third == second {
		t.Fatalf("sum after same-size edit = %q, unchanged", third)
	}
}

func TestSumCacheChanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "devyard.yml")
	writeSumFile(t, path, "a: 1\n")
	var c config.SumCache
	want := config.Sums{path: c.Sum(path)}
	if c.Changed(want) {
		t.Fatal("Changed = true for an unchanged file")
	}
	writeSumFile(t, path, "a: 2\n")
	if !c.Changed(want) {
		t.Fatal("Changed = false after an edit")
	}
	// A missing file hashes to "" and does not count as a change against "".
	missing := filepath.Join(t.TempDir(), "absent")
	if c.Changed(config.Sums{missing: ""}) {
		t.Fatal("Changed = true for a missing file recorded as empty")
	}
}
