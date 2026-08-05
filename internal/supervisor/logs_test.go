package supervisor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceLoggerSizeRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.log")
	l, err := newServiceLogger(path, "api", io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.close() }()
	l.maxSize = 200

	payload := strings.Repeat("x", 40)
	for i := 0; i < 20; i++ {
		l.writeLine(payload)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > l.maxSize+int64(len(payload))+64 {
		t.Fatalf("current log size %d exceeds soft cap %d by too much", info.Size(), l.maxSize)
	}
	prev := previousLogPath(path)
	if _, err := os.Stat(prev); err != nil {
		t.Fatalf("expected previous log after size rotation: %v", err)
	}
	prevInfo, err := os.Stat(prev)
	if err != nil {
		t.Fatal(err)
	}
	if prevInfo.Size() == 0 {
		t.Fatal("previous log is empty after size rotation")
	}
}
