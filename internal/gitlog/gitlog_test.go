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
	run("git", "config", "commit.gpgsign", "false")
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
	if commits[0].Time == nil {
		t.Fatalf("expected commit time, got nil")
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
	if commits[0].Additions != 1 || commits[0].Deletions != 0 || commits[0].FilesChanged != 1 {
		t.Fatalf("expected commit[0] 1 add, 0 del, 1 file; got %+v", commits[0])
	}
	if commits[1].Additions != 1 || commits[1].Deletions != 0 || commits[1].FilesChanged != 1 {
		t.Fatalf("expected commit[1] 1 add, 0 del, 1 file; got %+v", commits[1])
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

	res, err := Diff(dir, headHash, "")
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
	if res.Commit.Additions != 1 || res.Commit.Deletions != 0 || res.Commit.FilesChanged != 1 {
		t.Fatalf("expected Diff commit 1 add, 0 del, 1 file; got %+v", res.Commit)
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

	diffRes, err := Diff(dir, "WORKDIR", "")
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
	if err := Stage(dir, "c.txt", false, false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
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

	diffRes, err := Diff(dir, "WORKDIR", "")
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

	diffStaged, err := Diff(dir, "WORKDIR", "")
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

	diffUnstaged, err := Diff(dir, "WORKDIR", "")
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

	// Set upstream and push.
	runIn("push", "-u", "origin", "main")
	output, err := Push(dir, "origin")
	if err != nil {
		t.Fatalf("Push: %v (output: %s)", err, output)
	}

	// Cloning dir gives the pushed commits; fetch should download them and
	// pull should fast-forward to match.
	clone := t.TempDir()
	runInClone := exec.Command("git", "clone", "-q", "-b", "main", remote, clone)
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
			"git add -A && git -c commit.gpgsign=false -c user.name='Test Author' -c user.email='test@example.com' commit -q -m '"+msg+"' && git push -q origin main")
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
		a, b := parseTracking(tc.input)
		if a != tc.ahead || b != tc.behind {
			t.Errorf("parseTracking(%q) = (%d, %d), want (%d, %d)", tc.input, a, b, tc.ahead, tc.behind)
		}
	}
}

func TestLogDoesNotModifyIndex(t *testing.T) {
	dir := initRepo(t)

	// Modify a working file so git status has work to do
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	indexPath := filepath.Join(dir, ".git", "index")
	fiBefore, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index before: %v", err)
	}

	// Run Log
	if _, _, _, _, err := Log(dir); err != nil {
		t.Fatalf("Log: %v", err)
	}

	// Run Diff for WORKDIR
	if _, err := Diff(dir, "WORKDIR", ""); err != nil {
		t.Fatalf("Diff WORKDIR: %v", err)
	}

	fiAfter, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index after: %v", err)
	}

	if !fiBefore.ModTime().Equal(fiAfter.ModTime()) {
		t.Errorf("read-only git operations modified .git/index mtime: before %v, after %v", fiBefore.ModTime(), fiAfter.ModTime())
	}
}

func TestParseStatsFromPatch(t *testing.T) {
	patch := "diff --git a/a.txt b/a.txt\n" +
		"index 111111132..444444 100644\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		"+new line\n" +
		" ctx\n" +
		"-old line\n" +
		"diff --git a/dir/old.txt b/dir/new.txt\n" +
		"rename from dir/old.txt\n" +
		"rename to dir/new.txt\n" +
		"diff --git a/c.bin b/c.bin\n" +
		"Binary files a/c.bin and b/c.bin differ\n"

	stats := parseStatsFromPatch(patch)
	if s := stats["a.txt"]; s.add != 1 || s.del != 1 {
		t.Errorf("a.txt stats = %+v, want 1 add / 1 del", s)
	}
	if s := stats["dir/new.txt"]; s.add != 0 || s.del != 0 {
		t.Errorf("rename stats = %+v, want 0 add / 0 del", s)
	}
	if s := stats["c.bin"]; s.add != 0 || s.del != 0 {
		t.Errorf("binary stats = %+v, want 0 add / 0 del", s)
	}
}

func TestUnquoteGitPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"plain.txt", "plain.txt"},
		{`"sp ace.txt"`, "sp ace.txt"},
		{`"weird\"name.txt"`, `weird"name.txt`},
		{"", ""},
		{`"`, `"`}, // malformed: left as-is
	}
	for _, tc := range tests {
		if got := unquoteGitPath(tc.input); got != tc.want {
			t.Errorf("unquoteGitPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestDiffRenameStats ensures renamed files get their +/− stats resolved
// against the rename-aware numstat key ("old => new"), not the raw field.
func TestDiffRenameStats(t *testing.T) {
	dir := initRepo(t)
	// Rename b.txt -> renamed.txt and verify the file row still resolves.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("-C", dir, "mv", "b.txt", "renamed.txt")
	run("-C", dir, "add", "-A")
	run("commit", "-q", "-m", "rename")

	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log: %v", err)
	}
	res, err := Diff(dir, commits[0].Hash, "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "renamed.txt" || res.Files[0].Status != "R" {
		t.Fatalf("unexpected rename files: %+v", res.Files)
	}
}

func TestParseShortstat(t *testing.T) {
	tests := []struct {
		input     string
		wantFiles int32
		wantAdd   int32
		wantDel   int32
	}{
		{
			input:     " 8 files changed, 135 insertions(+), 32 deletions(-)",
			wantFiles: 8,
			wantAdd:   135,
			wantDel:   32,
		},
		{
			input:     " 1 file changed, 7 insertions(+), 12 deletions(-)",
			wantFiles: 1,
			wantAdd:   7,
			wantDel:   12,
		},
		{
			input:     " 1 file changed, 89 insertions(+)",
			wantFiles: 1,
			wantAdd:   89,
			wantDel:   0,
		},
		{
			input:     " 2 files changed, 19 deletions(-)",
			wantFiles: 2,
			wantAdd:   0,
			wantDel:   19,
		},
		{
			input:     " 3 files changed",
			wantFiles: 3,
			wantAdd:   0,
			wantDel:   0,
		},
		{
			input:     "",
			wantFiles: 0,
			wantAdd:   0,
			wantDel:   0,
		},
	}

	for _, tt := range tests {
		fc, add, del := parseShortstat(tt.input)
		if fc != tt.wantFiles || add != tt.wantAdd || del != tt.wantDel {
			t.Errorf("parseShortstat(%q) = (%d, %d, %d); want (%d, %d, %d)",
				tt.input, fc, add, del, tt.wantFiles, tt.wantAdd, tt.wantDel)
		}
	}
}

func TestStatus(t *testing.T) {
	dir := initRepo(t)

	// Clean status
	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.IsRepo || !st.IsClean || st.Branch != "main" {
		t.Fatalf("expected clean repo on branch main, got %+v", st)
	}
	if st.Staged != 0 || st.Dirty != 0 || st.Untracked != 0 {
		t.Fatalf("expected all counts 0, got staged=%d dirty=%d untracked=%d", st.Staged, st.Dirty, st.Untracked)
	}

	// Add untracked file
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = Status(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsClean || st.Untracked != 1 {
		t.Fatalf("expected 1 untracked file and not clean, got %+v", st)
	}

	// Modify existing file (dirty)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = Status(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Dirty != 1 {
		t.Fatalf("expected 1 dirty file, got %+v", st)
	}

	// Stage a change
	cmd := exec.Command("git", "-C", dir, "add", "untracked.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	st, err = Status(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Staged != 1 || st.Untracked != 0 || st.Dirty != 1 {
		t.Fatalf("expected staged=1, untracked=0, dirty=1; got %+v", st)
	}

	// Test non-repo
	emptyDir := t.TempDir()
	st, err = Status(emptyDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsRepo {
		t.Fatalf("expected IsRepo=false for empty dir, got %+v", st)
	}
}
