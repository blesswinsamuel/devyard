package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLogHistoryTailLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.log")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString(strings.Repeat("x", i%3+1))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	got, err := ReadLogHistory(f, 3)
	if err != nil {
		t.Fatalf("ReadLogHistory: %v", err)
	}
	want := "xxx\nx\nxx\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadLogHistoryAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.log")
	content := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	got, err := ReadLogHistory(f, 0)
	if err != nil {
		t.Fatalf("ReadLogHistory: %v", err)
	}
	if string(got) != content {
		t.Fatalf("got %q, want %q", got, content)
	}
}

func TestLastNLines(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\n", 2, "b\nc\n"},
		{"a\nb\nc", 2, "b\nc"},
		{"a\nb\nc\n", 10, "a\nb\nc\n"},
		{"only\n", 1, "only\n"},
		{"", 5, ""},
	}
	for _, tc := range tests {
		got := string(lastNLines([]byte(tc.in), tc.n))
		if got != tc.want {
			t.Errorf("lastNLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
