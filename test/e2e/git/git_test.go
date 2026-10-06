// Package git_test covers the git integration (status in Watch, log/diff,
// stage/commit, push/pull/fetch against a local bare remote) with the
// sandboxed git config.
package git_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const cfg = `services:
  a:
    run: {{fixture "ticker"}} -interval 1s
`

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// repoProject creates a project whose dir is a git repo with one commit.
func repoProject(t *testing.T, sb *harness.Sandbox, name string) *harness.Project {
	t.Helper()
	p := sb.WriteProject(name, cfg, map[string]string{"README.md": "hello\n"})
	sb.Git(p.Dir, "init", "-q")
	sb.Git(p.Dir, "add", "-A")
	sb.Git(p.Dir, "commit", "-q", "-m", "initial")
	return p
}

func TestGit_StatusInWatchAndChangeSeq(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gstat")
	p.Start()
	w := sb.Daemon().Watch(context.Background())
	st := w.WaitFor(t, "git status", func(s harness.State) bool {
		g := s.Git["gstat"]
		return g.GetIsRepo() && g.GetBranch() == "main" && g.GetIsClean()
	})
	seq := st.Git["gstat"].GetChangeSeq()
	p.WriteFile("README.md", "changed\n")
	p.WriteFile("new.txt", "x\n")
	// Working-tree edits are only noticed by the tracker's poll: allow a
	// full poll interval plus slack.
	w.WaitForWithin(t, 20*time.Second, "dirty + untracked reported", func(s harness.State) bool {
		g := s.Git["gstat"]
		return g.GetDirty() >= 1 && g.GetUntracked() >= 1 && !g.GetIsClean() && g.GetChangeSeq() > seq
	})
}

func TestGit_LogDiffStageCommit(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gops")
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()

	log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gops"}))
	harness.NoError(t, err, "GitLog")
	if cs := log.Msg.GetCommits(); len(cs) != 1 || cs[0].GetSubject() != "initial" || cs[0].GetEmail() != "e2e@devyard.invalid" {
		t.Fatalf("GitLog: %v", cs)
	}

	p.WriteFile("feature.txt", "feature\n")
	diff, err := c.GitDiff(d.Ctx(), connect.NewRequest(&v1.GitDiffRequest{Project: "gops", Hash: "WORKDIR"}))
	harness.NoError(t, err, "GitDiff WORKDIR")
	found := false
	for _, f := range diff.Msg.GetResult().GetFiles() {
		if f.GetPath() == "feature.txt" && f.GetUntracked() {
			found = true
		}
	}
	if !found {
		t.Errorf("WORKDIR diff missing untracked feature.txt: %v", diff.Msg.GetResult().GetFiles())
	}

	_, err = c.GitStage(d.Ctx(), connect.NewRequest(&v1.GitStageRequest{Project: "gops", StageAll: true}))
	harness.NoError(t, err, "GitStage")
	w.WaitFor(t, "staged", func(s harness.State) bool { return s.Git["gops"].GetStaged() >= 1 })
	_, err = c.GitCommit(d.Ctx(), connect.NewRequest(&v1.GitCommitRequest{Project: "gops", Message: "add feature"}))
	harness.NoError(t, err, "GitCommit")
	w.WaitFor(t, "clean after commit", func(s harness.State) bool { return s.Git["gops"].GetIsClean() })

	log, err = c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gops"}))
	harness.NoError(t, err, "GitLog")
	head := log.Msg.GetCommits()[0]
	if head.GetSubject() != "add feature" || head.GetAuthor() != "Devyard E2E" {
		t.Errorf("head commit: %v (author must come from the sandbox gitconfig)", head)
	}
	cd, err := c.GitDiff(d.Ctx(), connect.NewRequest(&v1.GitDiffRequest{Project: "gops", Hash: head.GetHash()}))
	harness.NoError(t, err, "GitDiff commit")
	if !strings.Contains(cd.Msg.GetResult().GetDiff(), "+feature") {
		t.Errorf("commit diff: %q", cd.Msg.GetResult().GetDiff())
	}
	if got := sb.Git(p.Dir, "log", "-1", "--format=%an <%ae>"); got != "Devyard E2E <e2e@devyard.invalid>" {
		t.Errorf("git log author %q", got)
	}
}

func TestGit_StashPushPopDrop(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gstash")
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()
	w.WaitFor(t, "clean repo", func(s harness.State) bool { return s.Git["gstash"].GetIsClean() })

	p.WriteFile("README.md", "stashed\n")
	p.WriteFile("untracked.txt", "u\n")
	w.WaitForWithin(t, 20*time.Second, "dirty", func(s harness.State) bool { return !s.Git["gstash"].GetIsClean() })

	_, err := c.GitStash(d.Ctx(), connect.NewRequest(&v1.GitStashRequest{
		Project: "gstash", Op: "push", Message: "wip", IncludeUntracked: true,
	}))
	harness.NoError(t, err, "GitStash push")
	w.WaitFor(t, "clean after stash", func(s harness.State) bool { return s.Git["gstash"].GetIsClean() })
	log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gstash"}))
	harness.NoError(t, err, "GitLog")
	if stashes := log.Msg.GetStashes(); len(stashes) != 1 || !strings.Contains(stashes[0].GetName(), "wip") {
		t.Fatalf("stash list after push: %v", stashes)
	}

	// Pop restores the changes and removes the entry.
	_, err = c.GitStash(d.Ctx(), connect.NewRequest(&v1.GitStashRequest{Project: "gstash", Op: "pop", Index: "stash@{0}"}))
	harness.NoError(t, err, "GitStash pop")
	w.WaitFor(t, "dirty after pop", func(s harness.State) bool { return !s.Git["gstash"].GetIsClean() })
	if b, err := os.ReadFile(p.Path("README.md")); err != nil || string(b) != "stashed\n" {
		t.Fatalf("README.md after pop: %q %v", b, err)
	}
	if _, err := os.Stat(p.Path("untracked.txt")); err != nil {
		t.Fatalf("untracked.txt not restored by pop: %v", err)
	}
	log, err = c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gstash"}))
	harness.NoError(t, err, "GitLog")
	if stashes := log.Msg.GetStashes(); len(stashes) != 0 {
		t.Fatalf("stash list after pop: %v", stashes)
	}

	// Drop removes a stash without restoring anything.
	_, err = c.GitStash(d.Ctx(), connect.NewRequest(&v1.GitStashRequest{Project: "gstash", Op: "push", IncludeUntracked: true}))
	harness.NoError(t, err, "GitStash push again")
	st := w.WaitFor(t, "clean again", func(s harness.State) bool { return s.Git["gstash"].GetIsClean() })
	seq := st.Git["gstash"].GetChangeSeq()
	_, err = c.GitStash(d.Ctx(), connect.NewRequest(&v1.GitStashRequest{Project: "gstash", Op: "drop"}))
	harness.NoError(t, err, "GitStash drop")
	w.WaitFor(t, "drop reported", func(s harness.State) bool { return s.Git["gstash"].GetChangeSeq() > seq })
	if got := sb.Git(p.Dir, "stash", "list"); got != "" {
		t.Fatalf("expected no stash entries after drop, got %q", got)
	}

	// Unknown ops are rejected without touching the repository.
	_, err = c.GitStash(d.Ctx(), connect.NewRequest(&v1.GitStashRequest{Project: "gstash", Op: "rewind"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument for op=rewind, got %v", err)
	}
}

func TestGit_BranchCheckoutCreateRestore(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gbranch")
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()
	w.WaitFor(t, "on main", func(s harness.State) bool { return s.Git["gbranch"].GetBranch() == "main" })

	// Create a branch and check it out in one call; the switch reaches Watch.
	_, err := c.GitBranchCreate(d.Ctx(), connect.NewRequest(&v1.GitBranchCreateRequest{
		Project: "gbranch", Name: "feature", Checkout: true,
	}))
	harness.NoError(t, err, "GitBranchCreate")
	w.WaitFor(t, "on feature", func(s harness.State) bool { return s.Git["gbranch"].GetBranch() == "feature" })

	// Switch back; a branch created without checkout does not move HEAD.
	_, err = c.GitCheckout(d.Ctx(), connect.NewRequest(&v1.GitCheckoutRequest{Project: "gbranch", Branch: "main"}))
	harness.NoError(t, err, "GitCheckout")
	w.WaitFor(t, "back on main", func(s harness.State) bool { return s.Git["gbranch"].GetBranch() == "main" })
	_, err = c.GitBranchCreate(d.Ctx(), connect.NewRequest(&v1.GitBranchCreateRequest{Project: "gbranch", Name: "side"}))
	harness.NoError(t, err, "GitBranchCreate no checkout")
	if got := w.State().Git["gbranch"].GetBranch(); got != "main" {
		t.Fatalf("HEAD moved to %q, want main", got)
	}

	// Both branches are listed.
	log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gbranch"}))
	harness.NoError(t, err, "GitLog")
	names := map[string]bool{}
	for _, b := range log.Msg.GetBranches() {
		names[b.GetName()] = true
	}
	if !names["main"] || !names["feature"] || !names["side"] {
		t.Fatalf("branches after create: %v", log.Msg.GetBranches())
	}

	// Empty and malformed branch names are rejected with distinct codes.
	_, err = c.GitCheckout(d.Ctx(), connect.NewRequest(&v1.GitCheckoutRequest{Project: "gbranch", Branch: ""}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty branch: expected InvalidArgument, got %v", err)
	}
	_, err = c.GitCheckout(d.Ctx(), connect.NewRequest(&v1.GitCheckoutRequest{Project: "gbranch", Branch: "bad name"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("invalid branch: expected FailedPrecondition, got %v", err)
	}
	_, err = c.GitBranchCreate(d.Ctx(), connect.NewRequest(&v1.GitBranchCreateRequest{Project: "gbranch", Name: "feature"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("duplicate branch: expected FailedPrecondition, got %v", err)
	}

	// Discard a tracked file (back to HEAD) and delete an untracked one.
	p.WriteFile("README.md", "dirty\n")
	p.WriteFile("scratch.txt", "tmp\n")
	w.WaitForWithin(t, 20*time.Second, "dirty", func(s harness.State) bool { return !s.Git["gbranch"].GetIsClean() })
	_, err = c.GitRestore(d.Ctx(), connect.NewRequest(&v1.GitRestoreRequest{Project: "gbranch", Path: "README.md"}))
	harness.NoError(t, err, "GitRestore path")
	if got := sb.Git(p.Dir, "show", "HEAD:README.md"); got != "hello" {
		t.Fatalf("README.md restored to %q, want hello", got)
	}
	if _, err := os.Stat(p.Path("scratch.txt")); err != nil {
		t.Fatalf("scratch.txt removed too early: %v", err)
	}
	_, err = c.GitRestore(d.Ctx(), connect.NewRequest(&v1.GitRestoreRequest{Project: "gbranch", Path: "scratch.txt"}))
	harness.NoError(t, err, "GitRestore untracked")
	if _, err := os.Stat(p.Path("scratch.txt")); err == nil {
		t.Fatal("scratch.txt still there after restore")
	}
	w.WaitFor(t, "clean after restore", func(s harness.State) bool { return s.Git["gbranch"].GetIsClean() })

	// Discard all clears a fresh batch of changes.
	p.WriteFile("README.md", "dirty again\n")
	p.WriteFile("more.txt", "x\n")
	w.WaitForWithin(t, 20*time.Second, "dirty again", func(s harness.State) bool { return !s.Git["gbranch"].GetIsClean() })
	_, err = c.GitRestore(d.Ctx(), connect.NewRequest(&v1.GitRestoreRequest{Project: "gbranch", All: true}))
	harness.NoError(t, err, "GitRestore all")
	w.WaitFor(t, "clean after restore all", func(s harness.State) bool { return s.Git["gbranch"].GetIsClean() })
	if _, err := os.Stat(p.Path("more.txt")); err == nil {
		t.Fatal("more.txt still there after restore all")
	}

	// Neither path nor all is a bad request.
	_, err = c.GitRestore(d.Ctx(), connect.NewRequest(&v1.GitRestoreRequest{Project: "gbranch"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("restore without path or all: expected InvalidArgument, got %v", err)
	}
}

func TestGit_AmendAndMessageBody(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gamend")
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()
	w.WaitFor(t, "clean repo", func(s harness.State) bool { return s.Git["gamend"].GetIsClean() })

	stageAll := func() {
		t.Helper()
		_, err := c.GitStage(d.Ctx(), connect.NewRequest(&v1.GitStageRequest{Project: "gamend", StageAll: true}))
		harness.NoError(t, err, "GitStage")
		w.WaitFor(t, "staged", func(s harness.State) bool { return s.Git["gamend"].GetStaged() >= 1 })
	}

	// Amend with a message replaces HEAD's subject without adding a commit.
	p.WriteFile("README.md", "amended\n")
	stageAll()
	_, err := c.GitCommit(d.Ctx(), connect.NewRequest(&v1.GitCommitRequest{Project: "gamend", Message: "amended message", Amend: true}))
	harness.NoError(t, err, "GitCommit amend")
	w.WaitFor(t, "clean after amend", func(s harness.State) bool { return s.Git["gamend"].GetIsClean() })
	log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gamend"}))
	harness.NoError(t, err, "GitLog")
	if cs := log.Msg.GetCommits(); len(cs) != 1 || cs[0].GetSubject() != "amended message" {
		t.Fatalf("commits after amend: %v", cs)
	}

	// An empty message keeps HEAD's subject and folds the staged changes in.
	p.WriteFile("README.md", "folded\n")
	stageAll()
	_, err = c.GitCommit(d.Ctx(), connect.NewRequest(&v1.GitCommitRequest{Project: "gamend", Amend: true}))
	harness.NoError(t, err, "GitCommit amend --no-edit")
	w.WaitFor(t, "clean after no-edit amend", func(s harness.State) bool { return s.Git["gamend"].GetIsClean() })
	if got := sb.Git(p.Dir, "show", "HEAD:README.md"); got != "folded" {
		t.Fatalf("amended tree has README.md %q, want folded", got)
	}

	// A commit message's body travels with GitDiff, not the listing.
	p.WriteFile("body.txt", "b\n")
	stageAll()
	_, err = c.GitCommit(d.Ctx(), connect.NewRequest(&v1.GitCommitRequest{Project: "gamend", Message: "subject line\n\nbody line 1\nbody line 2"}))
	harness.NoError(t, err, "GitCommit with body")
	w.WaitFor(t, "clean after body commit", func(s harness.State) bool { return s.Git["gamend"].GetIsClean() })
	log, err = c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gamend"}))
	harness.NoError(t, err, "GitLog")
	head := log.Msg.GetCommits()[0]
	if head.GetSubject() != "subject line" {
		t.Fatalf("head subject %q", head.GetSubject())
	}
	if head.GetBody() != "" {
		t.Fatalf("GitLog must leave the body empty, got %q", head.GetBody())
	}
	res, err := c.GitDiff(d.Ctx(), connect.NewRequest(&v1.GitDiffRequest{Project: "gamend", Hash: head.GetHash()}))
	harness.NoError(t, err, "GitDiff")
	if got := res.Msg.GetResult().GetCommit().GetBody(); got != "body line 1\nbody line 2" {
		t.Fatalf("GitDiff body = %q, want \"body line 1\\nbody line 2\"", got)
	}
}

func TestGit_BranchDeleteAndTags(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "grefs")
	p.Start()
	d := sb.Daemon()
	c := d.Client()

	branches := func() []string {
		log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "grefs"}))
		harness.NoError(t, err, "GitLog")
		var names []string
		for _, b := range log.Msg.GetBranches() {
			names = append(names, b.GetName())
		}
		return names
	}
	tags := func() []string {
		log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "grefs"}))
		harness.NoError(t, err, "GitLog")
		var names []string
		for _, tg := range log.Msg.GetTags() {
			names = append(names, tg.GetName())
		}
		return names
	}

	// A created branch can be deleted again; the checked-out one cannot.
	_, err := c.GitBranchCreate(d.Ctx(), connect.NewRequest(&v1.GitBranchCreateRequest{Project: "grefs", Name: "feature"}))
	harness.NoError(t, err, "GitBranchCreate")
	if got := branches(); !slices.Contains(got, "feature") {
		t.Fatalf("branches after create: %v", got)
	}
	_, err = c.GitBranchDelete(d.Ctx(), connect.NewRequest(&v1.GitBranchDeleteRequest{Project: "grefs", Name: "feature"}))
	harness.NoError(t, err, "GitBranchDelete")
	if got := branches(); slices.Contains(got, "feature") {
		t.Fatalf("branch not deleted: %v", got)
	}
	_, err = c.GitBranchDelete(d.Ctx(), connect.NewRequest(&v1.GitBranchDeleteRequest{Project: "grefs", Name: "main"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("deleting the checked-out branch: expected FailedPrecondition, got %v", err)
	}

	// Lightweight and annotated tags, then delete one.
	_, err = c.GitTagCreate(d.Ctx(), connect.NewRequest(&v1.GitTagCreateRequest{Project: "grefs", Name: "v1.0.0"}))
	harness.NoError(t, err, "GitTagCreate lightweight")
	_, err = c.GitTagCreate(d.Ctx(), connect.NewRequest(&v1.GitTagCreateRequest{Project: "grefs", Name: "v2.0.0", Message: "release two"}))
	harness.NoError(t, err, "GitTagCreate annotated")
	if got := tags(); len(got) != 2 {
		t.Fatalf("tags after create: %v", got)
	}
	_, err = c.GitTagDelete(d.Ctx(), connect.NewRequest(&v1.GitTagDeleteRequest{Project: "grefs", Name: "v1.0.0"}))
	harness.NoError(t, err, "GitTagDelete")
	if got := tags(); len(got) != 1 || got[0] != "v2.0.0" {
		t.Fatalf("tags after delete: %v", got)
	}

	// Malformed names and empty input are rejected.
	_, err = c.GitTagCreate(d.Ctx(), connect.NewRequest(&v1.GitTagCreateRequest{Project: "grefs", Name: "bad name"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("invalid tag name: expected FailedPrecondition, got %v", err)
	}
	_, err = c.GitBranchDelete(d.Ctx(), connect.NewRequest(&v1.GitBranchDeleteRequest{Project: "grefs"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty branch name: expected InvalidArgument, got %v", err)
	}
}

func TestGit_LogPaging(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gpaging") // one commit
	for i := 0; i < 105; i++ {
		sb.Git(p.Dir, "commit", "--allow-empty", "-q", "-m", fmt.Sprintf("c%d", i))
	}
	p.Start()
	d := sb.Daemon()
	c := d.Client()

	first, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gpaging"}))
	harness.NoError(t, err, "GitLog page 0")
	if got := len(first.Msg.GetCommits()); got != 100 || !first.Msg.GetHasMore() {
		t.Fatalf("page 0: %d commits, hasMore=%v; want 100, true", got, first.Msg.GetHasMore())
	}
	second, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "gpaging", Skip: 100}))
	harness.NoError(t, err, "GitLog page 1")
	if got := len(second.Msg.GetCommits()); got != 6 || second.Msg.GetHasMore() {
		t.Fatalf("page 1: %d commits, hasMore=%v; want 6, false", got, second.Msg.GetHasMore())
	}
	seen := map[string]bool{}
	for _, cm := range append(first.Msg.GetCommits(), second.Msg.GetCommits()...) {
		if seen[cm.GetHash()] {
			t.Fatalf("commit %s appears on both pages", cm.GetShort())
		}
		seen[cm.GetHash()] = true
	}
	if len(seen) != 106 {
		t.Fatalf("pages cover %d commits, want 106", len(seen))
	}
}

func TestGit_LogPathHistory(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "ghistory") // "initial" touches README.md
	p.Start()
	d := sb.Daemon()
	c := d.Client()

	// An unrelated file, then a README change.
	p.WriteFile("other.txt", "o\n")
	sb.Git(p.Dir, "add", "-A")
	sb.Git(p.Dir, "commit", "-q", "-m", "add other")
	p.WriteFile("README.md", "hello again\n")
	sb.Git(p.Dir, "add", "-A")
	sb.Git(p.Dir, "commit", "-q", "-m", "readme change")

	log, err := c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "ghistory", Path: "README.md"}))
	harness.NoError(t, err, "GitLog path")
	var subjects []string
	for _, cm := range log.Msg.GetCommits() {
		subjects = append(subjects, cm.GetSubject())
	}
	if strings.Join(subjects, ",") != "readme change,initial" {
		t.Fatalf("history of README.md: %v", subjects)
	}

	// Uncommitted changes do not appear in a file's history.
	p.WriteFile("README.md", "dirty\n")
	log, err = c.GitLog(d.Ctx(), connect.NewRequest(&v1.GitLogRequest{Project: "ghistory", Path: "README.md"}))
	harness.NoError(t, err, "GitLog dirty path")
	for _, cm := range log.Msg.GetCommits() {
		if cm.GetHash() == "WORKDIR" {
			t.Fatal("WORKDIR appeared in a path-filtered log")
		}
	}
}

func TestGit_DiffRange(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "grange") // "initial" touches README.md
	p.Start()
	d := sb.Daemon()
	c := d.Client()

	p.WriteFile("x.txt", "x\n")
	sb.Git(p.Dir, "add", "-A")
	sb.Git(p.Dir, "commit", "-q", "-m", "add x")
	head := sb.Git(p.Dir, "rev-parse", "HEAD")
	initial := sb.Git(p.Dir, "rev-parse", "HEAD~1")

	res, err := c.GitDiff(d.Ctx(), connect.NewRequest(&v1.GitDiffRequest{Project: "grange", Hash: head, Base: initial}))
	harness.NoError(t, err, "GitDiff range")
	if got := res.Msg.GetResult().GetCommit().GetHash(); got != head {
		t.Fatalf("range commit = %s, want %s", got, head)
	}
	found := false
	for _, f := range res.Msg.GetResult().GetFiles() {
		if f.GetPath() == "x.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("range files: %v", res.Msg.GetResult().GetFiles())
	}
	if !strings.Contains(res.Msg.GetResult().GetDiff(), "+x") {
		t.Fatalf("range diff: %q", res.Msg.GetResult().GetDiff())
	}
}

func TestGit_ApplyHunk(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "ghunk") // README.md is "hello\n"
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()

	// Stage, unstage and discard one hunk, using git's own diff as the patch.
	p.WriteFile("README.md", "changed\n")
	patch := sb.Git(p.Dir, "diff", "--", "README.md") + "\n"
	if !strings.Contains(patch, "+changed") {
		t.Fatalf("patch to apply: %q", patch)
	}
	_, err := c.GitApply(d.Ctx(), connect.NewRequest(&v1.GitApplyRequest{Project: "ghunk", Patch: patch, Cached: true}))
	harness.NoError(t, err, "GitApply stage")
	w.WaitFor(t, "staged", func(s harness.State) bool { return s.Git["ghunk"].GetStaged() >= 1 })
	if b, _ := os.ReadFile(p.Path("README.md")); string(b) != "changed\n" {
		t.Fatalf("--cached touched the working tree: %q", b)
	}

	_, err = c.GitApply(d.Ctx(), connect.NewRequest(&v1.GitApplyRequest{Project: "ghunk", Patch: patch, Cached: true, Reverse: true}))
	harness.NoError(t, err, "GitApply unstage")
	w.WaitFor(t, "unstaged", func(s harness.State) bool { return s.Git["ghunk"].GetStaged() == 0 })

	_, err = c.GitApply(d.Ctx(), connect.NewRequest(&v1.GitApplyRequest{Project: "ghunk", Patch: patch, Reverse: true}))
	harness.NoError(t, err, "GitApply discard")
	w.WaitFor(t, "clean", func(s harness.State) bool { return s.Git["ghunk"].GetIsClean() })
	if b, _ := os.ReadFile(p.Path("README.md")); string(b) != "hello\n" {
		t.Fatalf("README.md after discard = %q", b)
	}

	_, err = c.GitApply(d.Ctx(), connect.NewRequest(&v1.GitApplyRequest{Project: "ghunk"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty patch: expected InvalidArgument, got %v", err)
	}
}

func TestGit_PushFetchPullLocalRemote(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gremote")
	remote := filepath.Join(sb.Root, "remote.git")
	sb.Git(sb.Root, "init", "-q", "--bare", remote)
	sb.Git(p.Dir, "remote", "add", "origin", remote)
	sb.Git(p.Dir, "push", "-q", "-u", "origin", "main")
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	c := d.Client()

	p.WriteFile("local.txt", "l\n")
	sb.Git(p.Dir, "add", "-A")
	sb.Git(p.Dir, "commit", "-q", "-m", "local change")
	w.WaitFor(t, "ahead 1", func(s harness.State) bool { return s.Git["gremote"].GetAhead() == 1 })
	_, err := c.GitPush(d.Ctx(), connect.NewRequest(&v1.GitPushRequest{Project: "gremote"}))
	harness.NoError(t, err, "GitPush")
	if got := sb.Git(sb.Root, "--git-dir", remote, "log", "-1", "--format=%s", "main"); got != "local change" {
		t.Errorf("remote head %q after push", got)
	}

	// Someone else pushes; fetch shows behind, pull catches up.
	other := filepath.Join(sb.Root, "other")
	sb.Git(sb.Root, "clone", "-q", remote, other)
	if err := os.WriteFile(filepath.Join(other, "theirs.txt"), []byte("t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb.Git(other, "add", "-A")
	sb.Git(other, "commit", "-q", "-m", "their change")
	sb.Git(other, "push", "-q", "origin", "main")
	_, err = c.GitFetch(d.Ctx(), connect.NewRequest(&v1.GitFetchRequest{Project: "gremote"}))
	harness.NoError(t, err, "GitFetch")
	w.WaitFor(t, "behind 1", func(s harness.State) bool { return s.Git["gremote"].GetBehind() == 1 })
	_, err = c.GitPull(d.Ctx(), connect.NewRequest(&v1.GitPullRequest{Project: "gremote"}))
	harness.NoError(t, err, "GitPull")
	w.WaitFor(t, "up to date", func(s harness.State) bool {
		g := s.Git["gremote"]
		return g.GetBehind() == 0 && g.GetAhead() == 0
	})
	if _, err := os.Stat(p.Path("theirs.txt")); err != nil {
		t.Errorf("pull did not bring theirs.txt: %v", err)
	}
}

// O18: stopped projects are still watched.
func TestLedger_O18_StoppedProjectStillWatched(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	p := repoProject(t, sb, "gstopped")
	p.Start()
	p.CLI("stop").MustSucceed(t)
	w := sb.Daemon().Watch(context.Background())
	st := w.WaitFor(t, "git status of the stopped project", func(s harness.State) bool {
		return s.Git["gstopped"].GetIsRepo() && s.AllServicesIn("gstopped", "stopped")
	})
	seq := st.Git["gstopped"].GetChangeSeq()
	p.WriteFile("edit.txt", "x\n")
	// Only the poll notices edits to a stopped project's working tree.
	w.WaitForWithin(t, 20*time.Second, "change seen while stopped", func(s harness.State) bool {
		return s.Git["gstopped"].GetChangeSeq() > seq && s.Git["gstopped"].GetUntracked() >= 1
	})
}

// O18: two projects in one repository both see changes.
func TestLedger_O18_SharedRepoBothProjectsNotified(t *testing.T) {
	t.Parallel()
	requireGit(t)
	sb := harness.New(t)
	root := filepath.Join(sb.Root, "mono")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sb.Git(root, "init", "-q")
	var projects []*harness.Project
	for _, name := range []string{"mono-a", "mono-b"} {
		projects = append(projects, sb.WriteProjectAt(name, filepath.Join(root, name), cfg, nil))
	}
	sb.Git(root, "add", "-A")
	sb.Git(root, "commit", "-q", "-m", "init")
	for _, p := range projects {
		p.Start()
	}
	w := sb.Daemon().Watch(context.Background())
	st := w.WaitFor(t, "both have git status", func(s harness.State) bool {
		return s.Git["mono-a"].GetIsRepo() && s.Git["mono-b"].GetIsRepo()
	})
	a, b := st.Git["mono-a"].GetChangeSeq(), st.Git["mono-b"].GetChangeSeq()
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only the poll notices working-tree edits shared by both projects.
	w.WaitForWithin(t, 20*time.Second, "both notified", func(s harness.State) bool {
		return s.Git["mono-a"].GetChangeSeq() > a && s.Git["mono-b"].GetChangeSeq() > b
	})
	// A new branch (new ref directory) is noticed.
	sb.Git(root, "branch", "feature/deep/branch")
	st = w.State()
	a = st.Git["mono-a"].GetChangeSeq()
	sb.Git(root, "checkout", "-q", "feature/deep/branch")
	w.WaitFor(t, "branch switch noticed", func(s harness.State) bool {
		return s.Git["mono-a"].GetBranch() == "feature/deep/branch" && s.Git["mono-a"].GetChangeSeq() > a
	})
}
