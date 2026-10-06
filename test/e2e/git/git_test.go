// Package git_test covers the git integration (status in Watch, log/diff,
// stage/commit, push/pull/fetch against a local bare remote) with the
// sandboxed git config.
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
