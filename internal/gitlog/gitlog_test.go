package gitlog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a throwaway git repo in a temp dir with two commits.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Committer",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("git", "init", "-q", "-b", "main")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test Author")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "add", "a.txt")
	run("git", "commit", "-q", "-m", "first commit")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("git", "add", "b.txt")
	run("git", "commit", "-q", "-m", "second commit")
	return dir
}

func TestLog(t *testing.T) {
	dir := initRepo(t)
	if !IsRepo(dir) {
		t.Fatalf("IsRepo(%s) = false, want true", dir)
	}
	commits, branches, tags, stashes, err := Log(dir)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("Log returned %d commits, want 2", len(commits))
	}
	if len(branches) < 1 {
		t.Fatalf("expected at least 1 branch (main), got %d", len(branches))
	}
	if !branches[0].IsActive || branches[0].Name != "main" {
		t.Fatalf("expected active branch main, got %+v", branches[0])
	}
	_ = tags
	_ = stashes
	// Newest first.
	if commits[0].Subject != "second commit" || commits[1].Subject != "first commit" {
		t.Fatalf("unexpected order: %+v", commits)
	}
	if !strings.HasPrefix(commits[0].Hash, commits[0].Short) {
		t.Fatalf("short %q is not a prefix of hash %q", commits[0].Short, commits[0].Hash)
	}
	if commits[0].Author != "Test Author" || commits[0].Email != "test@example.com" {
		t.Fatalf("unexpected author: %+v", commits[0])
	}
	if commits[0].Time == "" {
		t.Fatalf("expected commit time, got empty")
	}
	if !commits[0].Head {
		t.Fatalf("expected most recent commit to be marked HEAD, got %+v", commits[0])
	}
	if len(commits[0].Parents) != 1 || commits[0].Parents[0] != commits[1].Hash {
		t.Fatalf("expected newest commit to have 1 parent (the root), got %+v", commits[0])
	}
	// The root commit has no parents.
	if len(commits[1].Parents) != 0 {
		t.Fatalf("expected root commit to have no parents, got %+v", commits[1])
	}
}

func TestLogNotARepo(t *testing.T) {
	dir := t.TempDir()
	if IsRepo(dir) {
		t.Fatalf("IsRepo(%s) = true for non-repo", dir)
	}
	if _, _, _, _, err := Log(dir); err == nil {
		t.Fatalf("Log on non-repo dir: expected error, got nil")
	}
}

func TestLogEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	commits, _, _, _, err := Log(dir)
	if err != nil {
		t.Fatalf("Log on empty repo: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("expected 0 commits, got %d", len(commits))
	}
}

func TestDiff(t *testing.T) {
	dir := initRepo(t)
	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log failed: %v", err)
	}
	headHash := commits[0].Hash

	res, err := Diff(dir, headHash)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if res.Commit.Hash != headHash {
		t.Fatalf("expected commit hash %s, got %s", headHash, res.Commit.Hash)
	}
	if len(res.Files) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(res.Files))
	}
	if res.Files[0].Path != "b.txt" || res.Files[0].Status != "A" {
		t.Fatalf("unexpected file change: %+v", res.Files[0])
	}
	if !strings.Contains(res.Diff, "b.txt") || !strings.Contains(res.Diff, "+two") {
		t.Fatalf("unexpected diff content: %s", res.Diff)
	}
}

func TestUncommittedAndCommit(t *testing.T) {
	dir := initRepo(t)
	// Add an uncommitted file
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	commits, _, _, _, err := Log(dir)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits (including WORKDIR), got %d", len(commits))
	}
	if commits[0].Hash != "WORKDIR" {
		t.Fatalf("expected first commit to be WORKDIR, got %+v", commits[0])
	}

	diffRes, err := Diff(dir, "WORKDIR")
	if err != nil {
		t.Fatalf("Diff WORKDIR: %v", err)
	}
	if diffRes.Commit.Hash != "WORKDIR" {
		t.Fatalf("expected WORKDIR hash, got %s", diffRes.Commit.Hash)
	}
	if len(diffRes.Files) != 1 || diffRes.Files[0].Path != "c.txt" {
		t.Fatalf("unexpected WORKDIR files: %+v", diffRes.Files)
	}

	// Commit the changes
	if err := Commit(dir, "third commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	commitsAfter, _, _, _, err := Log(dir)
	if err != nil {
		t.Fatalf("Log after commit: %v", err)
	}
	if len(commitsAfter) != 3 {
		t.Fatalf("expected 3 commits after commit, got %d", len(commitsAfter))
	}
	if commitsAfter[0].Hash == "WORKDIR" || commitsAfter[0].Subject != "third commit" {
		t.Fatalf("unexpected commits after commit: %+v", commitsAfter[0])
	}
}

func TestStageAndUnstage(t *testing.T) {
	dir := initRepo(t)
	// Add an untracked file and modify an existing file
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	diffRes, err := Diff(dir, "WORKDIR")
	if err != nil {
		t.Fatalf("Diff WORKDIR: %v", err)
	}
	if len(diffRes.Files) != 1 || !diffRes.Files[0].Untracked {
		t.Fatalf("expected untracked file, got %+v", diffRes.Files)
	}

	// Stage the untracked file
	if err := Stage(dir, "untracked.txt", false, false); err != nil {
		t.Fatalf("Stage untracked: %v", err)
	}

	diffStaged, err := Diff(dir, "WORKDIR")
	if err != nil {
		t.Fatalf("Diff WORKDIR after stage: %v", err)
	}
	if len(diffStaged.Files) != 1 || !diffStaged.Files[0].Staged {
		t.Fatalf("expected staged file, got %+v", diffStaged.Files)
	}

	// Unstage the file
	if err := Stage(dir, "untracked.txt", false, true); err != nil {
		t.Fatalf("Unstage file: %v", err)
	}

	diffUnstaged, err := Diff(dir, "WORKDIR")
	if err != nil {
		t.Fatalf("Diff WORKDIR after unstage: %v", err)
	}
	if len(diffUnstaged.Files) != 1 || !diffUnstaged.Files[0].Untracked {
		t.Fatalf("expected untracked file after unstage, got %+v", diffUnstaged.Files)
	}
}

// newBareRemote creates a bare git repo to act as a remote for push/pull/fetch.
func newBareRemote(t *testing.T) string {
	t.Helper()
	remote := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return remote
}

func TestPushPullFetch(t *testing.T) {
	dir := initRepo(t)
	remote := newBareRemote(t)

	// Wire the repo up to the remote and set an upstream for the branch.
	runIn := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Committer",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runIn("remote", "add", "origin", remote)

	// Push without an upstream should fail with useful guidance.
	if _, err := Push(dir, ""); err == nil {
		t.Fatalf("Push with no upstream: expected error, got nil")
	}

	// Push with an explicit remote and set upstream.
	output, err := Push(dir, "origin", "-u", "main")
	if err != nil {
		t.Fatalf("Push: %v (output: %s)", err, output)
	}

	// Cloning dir gives the pushed commits; fetch should download them and
	// pull should fast-forward to match.
	clone := t.TempDir()
	runInClone := exec.Command("git", "clone", "-q", remote, clone)
	if out, err := runInClone.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}

	// Add a commit in the clone and push it back to the remote.
	writeAndCommit := func(repo, file, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cmd := exec.Command("sh", "-c",
			"git add -A && git -c user.name='Test Author' -c user.email='test@example.com' commit -q -m '"+msg+"' && git push -q origin HEAD")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit+push: %v\n%s", err, out)
		}
	}
	writeAndCommit(clone, "c.txt", "clone commit")

	// Fetch should bring the new refs into dir without touching the worktree.
	fetched, err := Fetch(dir, "")
	if err != nil {
		t.Fatalf("Fetch: %v (output: %s)", err, fetched)
	}

	// Pull should fast-forward dir's main to the clone's commit.
	pulled, err := Pull(dir, "")
	if err != nil {
		t.Fatalf("Pull: %v (output: %s)", err, pulled)
	}

	// Confirmed by the log having three commits now.
	commits, _, _, _, err := Log(dir)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits after pull, got %d", len(commits))
	}
	if commits[0].Subject != "clone commit" {
		t.Fatalf("expected HEAD to be 'clone commit', got %q", commits[0].Subject)
	}
}

func TestPushPullFetchNotARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Push(dir, ""); err == nil {
		t.Fatalf("Push on non-repo: expected error, got nil")
	}
	if _, err := Pull(dir, ""); err == nil {
		t.Fatalf("Pull on non-repo: expected error, got nil")
	}
	if _, err := Fetch(dir, ""); err == nil {
		t.Fatalf("Fetch on non-repo: expected error, got nil")
	}
}

func TestParseAheadBehind(t *testing.T) {
	tests := []struct {
		input  string
		ahead  int
		behind int
	}{
		{"ahead 15", 15, 0},
		{"behind 2", 0, 2},
		{"ahead 3, behind 4", 3, 4},
		{"behind 10, ahead 1", 1, 10},
		{"gone", 0, 0},
		{"", 0, 0},
	}
	for _, tc := range tests {
		a, b := parseAheadBehind(tc.input)
		if a != tc.ahead || b != tc.behind {
			t.Errorf("parseAheadBehind(%q) = (%d, %d), want (%d, %d)", tc.input, a, b, tc.ahead, tc.behind)
		}
	}
}
