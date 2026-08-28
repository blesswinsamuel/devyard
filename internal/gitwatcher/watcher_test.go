package gitwatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepoWatcher(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gitwatcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}

	ch := make(chan string, 5)
	w, err := New(func(project string) {
		ch <- project
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.SetDebounce(50 * time.Millisecond)

	projName := "test-proj"
	if err := w.AddProject(projName, tmpDir); err != nil {
		t.Fatalf("AddProject failed: %v", err)
	}

	// Trigger change by touching .git/HEAD
	headFile := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(headFile, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to write HEAD: %v", err)
	}

	select {
	case got := <-ch:
		if got != projName {
			t.Errorf("got project %q, want %q", got, projName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for git change event")
	}

	// Trigger change by modifying source file
	srcFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	select {
	case got := <-ch:
		if got != projName {
			t.Errorf("got project %q, want %q", got, projName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for source file change event")
	}

	// Remove project and ensure no further events fire
	w.RemoveProject(projName)
	if err := os.WriteFile(srcFile, []byte("package main // update\n"), 0644); err != nil {
		t.Fatalf("failed to update source file: %v", err)
	}

	select {
	case got := <-ch:
		t.Fatalf("unexpected event after RemoveProject: %s", got)
	case <-time.After(150 * time.Millisecond):
		// Expected timeout
	}
}
