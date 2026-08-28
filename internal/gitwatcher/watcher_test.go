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
	refsDir := filepath.Join(gitDir, "refs", "heads")
	if err := os.MkdirAll(refsDir, 0755); err != nil {
		t.Fatalf("failed to create .git refs dir: %v", err)
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

	// Idempotent AddProject should succeed without error or re-adding
	if err := w.AddProject(projName, tmpDir); err != nil {
		t.Fatalf("idempotent AddProject failed: %v", err)
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
		t.Fatal("timed out waiting for HEAD change event")
	}

	// Trigger change by modifying .git/index (staging files)
	indexFile := filepath.Join(gitDir, "index")
	if err := os.WriteFile(indexFile, []byte("index-content"), 0644); err != nil {
		t.Fatalf("failed to write index: %v", err)
	}

	select {
	case got := <-ch:
		if got != projName {
			t.Errorf("got project %q, want %q", got, projName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for index change event")
	}

	// Trigger change by updating branch ref
	refFile := filepath.Join(refsDir, "main")
	if err := os.WriteFile(refFile, []byte("0123456789abcdef"), 0644); err != nil {
		t.Fatalf("failed to write branch ref: %v", err)
	}

	select {
	case got := <-ch:
		if got != projName {
			t.Errorf("got project %q, want %q", got, projName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ref change event")
	}

	// Remove project and ensure no further events fire
	w.RemoveProject(projName)
	if err := os.WriteFile(headFile, []byte("ref: refs/heads/feature\n"), 0644); err != nil {
		t.Fatalf("failed to update HEAD: %v", err)
	}

	select {
	case got := <-ch:
		t.Fatalf("unexpected event after RemoveProject: %s", got)
	case <-time.After(150 * time.Millisecond):
		// Expected timeout
	}
}

func TestRepoWatcherWorktree(t *testing.T) {
	realGitDir, err := os.MkdirTemp("", "gitwatcher-real-git-*")
	if err != nil {
		t.Fatalf("failed to create real git dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(realGitDir) }()

	worktreeDir, err := os.MkdirTemp("", "gitwatcher-wt-*")
	if err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(worktreeDir) }()

	// Create .git file in worktree pointing to realGitDir
	gitFile := filepath.Join(worktreeDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+realGitDir+"\n"), 0644); err != nil {
		t.Fatalf("failed to write .git file: %v", err)
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

	projName := "wt-proj"
	if err := w.AddProject(projName, worktreeDir); err != nil {
		t.Fatalf("AddProject failed: %v", err)
	}

	// Trigger change in realGitDir/HEAD
	headFile := filepath.Join(realGitDir, "HEAD")
	if err := os.WriteFile(headFile, []byte("ref: refs/heads/wt-branch\n"), 0644); err != nil {
		t.Fatalf("failed to write HEAD: %v", err)
	}

	select {
	case got := <-ch:
		if got != projName {
			t.Errorf("got project %q, want %q", got, projName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worktree git change event")
	}
}
