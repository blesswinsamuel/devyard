package gitlog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
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

// TestMain isolates git from the developer's global and system config (a
// user-level push.autoSetupRemote, for example, changes push semantics).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gitlog-home")
	if err != nil {
		panic(err)
	}
	gitconfig := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME":                home,
		"GIT_CONFIG_GLOBAL":   gitconfig,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
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
	if commits[0].TimeUnixMs == 0 {
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
	// Stats come from Diff, not from the log listing (no --shortstat).
	if commits[0].Additions != 0 || commits[0].Deletions != 0 || commits[0].FilesChanged != 0 {
		t.Fatalf("expected no per-commit stats in the log, got %+v", commits[0])
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

	res, err := Diff(dir, headHash, "", headHash, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if res.Commit.Hash != headHash {
		t.Fatalf("expected commit hash %s, got %s", headHash, res.Commit.Hash)
	}
	if !res.Commit.Head {
		t.Fatalf("expected Head flag for the HEAD commit, got %+v", res.Commit)
	}
	if other, err := Diff(dir, headHash, "", strings.Repeat("0", 40), 0); err != nil || other.Commit.Head {
		t.Fatalf("expected no Head flag for a non-matching headHash, got %+v (%v)", other.Commit, err)
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
	if commits[0].FilesChanged != 1 {
		t.Fatalf("expected the WORKDIR entry to count 1 changed file, got %+v", commits[0])
	}

	diffRes, err := Diff(dir, "WORKDIR", "", "", 0)
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

	diffRes, err := Diff(dir, "WORKDIR", "", "", 0)
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

	diffStaged, err := Diff(dir, "WORKDIR", "", "", 0)
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

	diffUnstaged, err := Diff(dir, "WORKDIR", "", "", 0)
	if err != nil {
		t.Fatalf("Diff WORKDIR after unstage: %v", err)
	}
	if len(diffUnstaged.Files) != 1 || !diffUnstaged.Files[0].Untracked {
		t.Fatalf("expected untracked file after unstage, got %+v", diffUnstaged.Files)
	}
}

func TestStashPushPopDrop(t *testing.T) {
	dir := initRepo(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		return string(b)
	}

	if got := len(GetStashes(dir)); got != 0 {
		t.Fatalf("expected no stashes in a fresh repo, got %d", got)
	}

	// Stash a mix of staged, unstaged and untracked changes.
	write("a.txt", "stashed\n")
	write("u.txt", "untracked\n")
	if err := Stage(dir, "a.txt", false, false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := StashPush(dir, nil, "my work", true); err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	if uncommittedCount(dir) > 0 {
		t.Fatal("expected a clean tree after StashPush with untracked")
	}
	stashes := GetStashes(dir)
	if len(stashes) != 1 || stashes[0].Index != "stash@{0}" {
		t.Fatalf("expected stash@{0}, got %+v", stashes)
	}
	if !strings.Contains(stashes[0].Name, "my work") {
		t.Fatalf("stash message missing from name: %+v", stashes[0])
	}

	// Restore: both files are back and the stash entry is gone.
	if err := StashPop(dir, ""); err != nil {
		t.Fatalf("StashPop: %v", err)
	}
	if got := read("a.txt"); got != "stashed\n" {
		t.Fatalf("a.txt not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "u.txt")); err != nil {
		t.Fatalf("untracked u.txt not restored: %v", err)
	}
	if stashes = GetStashes(dir); len(stashes) != 0 {
		t.Fatalf("expected no stashes after pop, got %+v", stashes)
	}

	// Drop removes the entry without restoring anything.
	if err := StashPush(dir, nil, "", false); err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	if err := StashDrop(dir, "stash@{0}"); err != nil {
		t.Fatalf("StashDrop: %v", err)
	}
	if stashes = GetStashes(dir); len(stashes) != 0 {
		t.Fatalf("expected no stashes after drop, got %+v", stashes)
	}

	// A pathspec stash takes only the named file; the rest stays.
	write("a.txt", "one-mod\n")
	write("b.txt", "two-mod\n")
	if err := StashPush(dir, []string{"a.txt"}, "", false); err != nil {
		t.Fatalf("StashPush paths: %v", err)
	}
	if got := read("a.txt"); got != "one\n" {
		t.Fatalf("a.txt should be back at HEAD, got %q", got)
	}
	if got := read("b.txt"); got != "two-mod\n" {
		t.Fatalf("b.txt should have stayed modified, got %q", got)
	}
	if err := StashDrop(dir, ""); err != nil {
		t.Fatalf("StashDrop default index: %v", err)
	}
}

func TestStashRejectsBadIndex(t *testing.T) {
	dir := initRepo(t)
	err := StashPop(dir, "--output=/tmp/x")
	if err == nil || !strings.Contains(err.Error(), "invalid stash index") {
		t.Fatalf("expected an invalid stash index error, got %v", err)
	}
	if err := StashDrop(dir, ""); err == nil {
		t.Fatal("expected an error popping without stashes")
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
	if _, err := Push(context.Background(), dir, ""); err == nil {
		t.Fatalf("Push with no upstream: expected error, got nil")
	}

	// Set upstream and push.
	runIn("push", "-u", "origin", "main")
	output, err := Push(context.Background(), dir, "origin")
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
	fetched, err := Fetch(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("Fetch: %v (output: %s)", err, fetched)
	}

	// Pull should fast-forward dir's main to the clone's commit.
	pulled, err := Pull(context.Background(), dir, "")
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
	if _, err := Push(context.Background(), dir, ""); err == nil {
		t.Fatalf("Push on non-repo: expected error, got nil")
	}
	if _, err := Pull(context.Background(), dir, ""); err == nil {
		t.Fatalf("Pull on non-repo: expected error, got nil")
	}
	if _, err := Fetch(context.Background(), dir, ""); err == nil {
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
	if _, err := Diff(dir, "WORKDIR", "", "", 0); err != nil {
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
	res, err := Diff(dir, commits[0].Hash, "", "", 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "renamed.txt" || res.Files[0].Status != "R" {
		t.Fatalf("unexpected rename files: %+v", res.Files)
	}
	if res.Files[0].OldPath != "b.txt" {
		t.Fatalf("expected rename old path b.txt, got %+v", res.Files[0])
	}
}

// TestDiffContextLines verifies the -U context width reaches the patch.
func TestDiffContextLines(t *testing.T) {
	dir := initRepo(t)
	body := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "ten lines")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body+"eleven\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-qam", "eleven")

	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log: %v", err)
	}
	narrow, err := Diff(dir, commits[0].Hash, "", "", 1)
	if err != nil {
		t.Fatalf("Diff U1: %v", err)
	}
	wide, err := Diff(dir, commits[0].Hash, "", "", 5)
	if err != nil {
		t.Fatalf("Diff U5: %v", err)
	}
	if !strings.Contains(narrow.Diff, "+eleven") || !strings.Contains(wide.Diff, "+eleven") {
		t.Fatalf("expected the added line in both patches:\nU1:\n%s\nU5:\n%s", narrow.Diff, wide.Diff)
	}
	if len(narrow.Diff) >= len(wide.Diff) {
		t.Fatalf("expected U5 to be longer than U1, got %d vs %d", len(wide.Diff), len(narrow.Diff))
	}
}

// TestDiffPathFilter covers a pathspec that matches and one that matches
// nothing: the latter still resolves the commit header (a bare git show
// with a non-matching pathspec prints nothing at all).
func TestDiffPathFilter(t *testing.T) {
	dir := initRepo(t)
	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log: %v", err)
	}
	headHash := commits[0].Hash

	res, err := Diff(dir, headHash, "b.txt", "", 0)
	if err != nil {
		t.Fatalf("Diff filtered: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "b.txt" {
		t.Fatalf("expected only b.txt, got %+v", res.Files)
	}
	if !strings.Contains(res.Diff, "+two") {
		t.Fatalf("expected the b.txt patch, got %q", res.Diff)
	}

	res, err = Diff(dir, headHash, "nope.txt", "", 0)
	if err != nil {
		t.Fatalf("Diff non-matching: %v", err)
	}
	if res.Commit.Hash != headHash || len(res.Files) != 0 || res.Diff != "" {
		t.Fatalf("expected meta without files for a non-matching pathspec, got %+v", res)
	}
}

// TestDiffMerge covers a merge commit: --raw prints nothing for merges, so
// the file list comes from --numstat.
func TestDiffMerge(t *testing.T) {
	dir := initRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "s.txt")
	run("commit", "-qm", "side")
	run("checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "m.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "m.txt")
	run("commit", "-qm", "mainline")
	run("merge", "-q", "--no-edit", "side")

	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log: %v", err)
	}
	merge := commits[0]
	if len(merge.Parents) != 2 {
		t.Fatalf("expected a merge commit with two parents, got %+v", merge)
	}
	res, err := Diff(dir, merge.Hash, "", "", 0)
	if err != nil {
		t.Fatalf("Diff merge: %v", err)
	}
	if res.Commit.Hash != merge.Hash {
		t.Fatalf("expected the merge commit meta, got %+v", res.Commit)
	}
	if len(res.Files) == 0 {
		t.Fatalf("expected --numstat files for a merge, got %+v", res.Files)
	}
}

// TestDiffBinary covers a committed binary file: "-" numstat counts and the
// "Binary files" patch marker.
func TestDiffBinary(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), []byte{0, 1, 2, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stage(dir, "b.bin", false, false); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "binary"); err != nil {
		t.Fatal(err)
	}
	commits, _, _, _, err := Log(dir)
	if err != nil || len(commits) == 0 {
		t.Fatalf("Log: %v", err)
	}
	res, err := Diff(dir, commits[0].Hash, "", "", 0)
	if err != nil {
		t.Fatalf("Diff binary: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("expected 1 file, got %+v", res.Files)
	}
	f := res.Files[0]
	if f.Path != "b.bin" || f.Status != "A" || f.Additions != 0 || f.Deletions != 0 {
		t.Fatalf("unexpected binary file row: %+v", f)
	}
	if !strings.Contains(res.Diff, "Binary files") {
		t.Fatalf("expected a binary marker in the patch, got %q", res.Diff)
	}
}

// TestDiffWorkdirUntrackedPatch covers the in-process new-file patches of
// untracked files, including the no-trailing-newline marker and binaries.
func TestDiffWorkdirUntrackedPatch(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "u.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nl.txt"), []byte("no newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Diff(dir, "WORKDIR", "", "", 0)
	if err != nil {
		t.Fatalf("Diff WORKDIR: %v", err)
	}
	byPath := map[string]*pb.GitFileChange{}
	for _, f := range res.Files {
		byPath[f.Path] = f
	}
	if len(res.Files) != 3 {
		t.Fatalf("expected 3 untracked files, got %+v", res.Files)
	}
	u := byPath["u.txt"]
	if u == nil || !u.Untracked || u.Additions != 2 || u.Deletions != 0 {
		t.Fatalf("unexpected u.txt row: %+v", u)
	}
	for _, want := range []string{
		"diff --git a/u.txt b/u.txt\n",
		"new file mode 100644\n",
		"--- /dev/null\n+++ b/u.txt\n",
		"@@ -0,0 +1,2 @@\n",
		"+hello\n",
	} {
		if !strings.Contains(res.Diff, want) {
			t.Fatalf("patch missing %q:\n%s", want, res.Diff)
		}
	}
	if !strings.Contains(res.Diff, "\\ No newline at end of file\n") {
		t.Fatalf("expected the no-newline marker:\n%s", res.Diff)
	}
	if !strings.Contains(res.Diff, "Binary files /dev/null and b/bin.dat differ") {
		t.Fatalf("expected the binary marker:\n%s", res.Diff)
	}
	if nl := byPath["nl.txt"]; nl == nil || nl.Additions != 1 {
		t.Fatalf("expected nl.txt with 1 addition, got %+v", nl)
	}
	if total := res.Commit.Additions; total != 3 {
		t.Fatalf("expected 3 total additions, got %d", total)
	}
}

// TestFingerprint covers the cached-root variant of Fingerprint: an
// explicit root matches the resolved one, and edits move the fingerprint.
func TestFingerprint(t *testing.T) {
	dir := initRepo(t)
	root := RepoRoot(dir)
	if root == "" {
		t.Fatalf("RepoRoot(%s) = empty", dir)
	}
	fp := Fingerprint(dir, root)
	if fp == "" {
		t.Fatal("expected a fingerprint")
	}
	if got := Fingerprint(dir, ""); got != fp {
		t.Fatalf("explicit root %q and resolved root disagree: %s vs %s", root, fp, got)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Fingerprint(dir, root); got == fp {
		t.Fatal("expected a working-tree edit to change the fingerprint")
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

func TestParseDecorations(t *testing.T) {
	refs := parseDecorations("HEAD -> refs/heads/main, tag: refs/tags/v1.0, refs/remotes/origin/main, refs/remotes/origin/HEAD, refs/heads/feature/search, refs/stash", "main")
	got := map[string]string{}
	for _, r := range refs {
		got[r.Name] = r.Type
	}
	want := map[string]string{"main": "branch", "HEAD": "head", "v1.0": "tag", "origin/main": "remote", "feature/search": "branch", "stash": "stash"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}
