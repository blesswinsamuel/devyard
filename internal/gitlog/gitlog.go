// Package gitlog runs `git log` in a working directory and parses the result
// into wire format. It is used by the orchestrator (project-level git view)
// and by the single-project control backend.
package gitlog

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// CommitLimit caps how many commits are returned per request so a large
// repository can't swamp the control socket in one frame.
const CommitLimit = 100

// gitCmd creates an exec.Cmd for a git operation with optional locking disabled
// (GIT_OPTIONAL_LOCKS=0 and --no-optional-locks) so read-only operations do not
// refresh the index or touch .git/index, preventing file watcher loops.
func gitCmd(dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	setProcessGroup(cmd)
	return cmd
}

// RemoteTimeout bounds push, pull and fetch.
const RemoteTimeout = 2 * time.Minute

// remoteCmd runs a network git operation bounded by ctx and RemoteTimeout.
// It never prompts for credentials, and cancellation kills the whole process
// group (git spawns ssh and credential helpers).
func remoteCmd(ctx context.Context, dir, op, remote string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return "", fmt.Errorf("git: %s is not a git repository", dir)
	}
	ctx, cancel := context.WithTimeout(ctx, RemoteTimeout)
	defer cancel()
	args := []string{"-C", dir, op}
	if remote != "" {
		args = append(args, remote)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	combined := strings.TrimSpace(outBuf.String() + "\n" + errBuf.String())
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return combined, fmt.Errorf("git %s: timed out after %s", op, RemoteTimeout)
		}
		if combined == "" {
			combined = err.Error()
		}
		return combined, fmt.Errorf("git %s: %s: %w", op, combined, err)
	}
	return combined, nil
}

// Log runs `git log` in dir and returns up to CommitLimit commits, newest
// first, along with branches, tags, and stashes. skip drops that many newest
// commits (paging), and hasMore reports whether older commits exist beyond
// the page. path limits the listing to the commits that touched it (a
// repository-relative path). The WORKDIR pseudo-commit only appears on the
// first page of an unfiltered log.
func Log(dir string, skip int, path string) ([]*pb.GitCommit, []*pb.GitBranch, []*pb.GitTag, []*pb.GitStash, bool, error) {
	if dir == "" {
		return nil, nil, nil, nil, false, fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return nil, nil, nil, nil, false, fmt.Errorf("git: %s is not a git repository", dir)
	}
	if skip < 0 {
		skip = 0
	}

	head := resolveHead(dir)
	activeBranch := resolveActiveBranch(dir)
	uncommitted := uncommittedCount(dir)

	branches := GetBranches(dir, activeBranch)
	tags := GetTags(dir)
	stashes := GetStashes(dir)

	// An empty repository (unborn branch) has no commits, so `git log` would
	// fail with exit 128. Surface that as an empty log (or uncommitted changes) rather than an error.
	if head == "" {
		if uncommitted > 0 && skip == 0 && path == "" {
			return []*pb.GitCommit{{
				Hash:       "WORKDIR",
				Short:      "WORKDIR",
				Subject:    "Uncommitted Changes",
				Author:     "Working Directory",
				TimeUnixMs: time.Now().UnixMilli(),
				// Staged, unstaged and untracked entries alike, counted by
				// the status call above.
				FilesChanged: int32(uncommitted),
			}}, branches, tags, stashes, false, nil
		}
		return nil, branches, tags, stashes, false, nil
	}

	format := "\x1e%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s\x1f%D"
	// --date-order keeps children before parents even when timestamps
	// tie; --decorate=full lets refs be classified by their full name.
	// No --shortstat: it tree-diffs every listed commit (the dominant cost
	// of the whole log call); the selected commit's stats come from Diff.
	// One extra commit reveals whether an older page exists.
	args := []string{"log", "--all", "--date-order", "--decorate=full", fmt.Sprintf("--pretty=format:%s", format), "-n", fmt.Sprintf("%d", CommitLimit+1)}
	if skip > 0 {
		args = append(args, "--skip", fmt.Sprintf("%d", skip))
	}
	if path != "" {
		args = append(args, "--", pathSpec(path))
	}
	cmd := gitCmd(dir, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, nil, nil, nil, false, fmt.Errorf("git: %s: %w", msg, err)
	}

	raw := out.String()
	commits := make([]*pb.GitCommit, 0)
	if raw != "" {
		chunks := strings.Split(raw, "\x1e")
		for _, chunk := range chunks {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}
			lines := strings.Split(chunk, "\n")
			headerLine := strings.TrimSpace(lines[0])
			fields := strings.Split(headerLine, "\x1f")
			if len(fields) < 7 {
				continue
			}
			t, _ := time.Parse(time.RFC3339, fields[4])
			c := &pb.GitCommit{
				Hash:       fields[0],
				Short:      fields[1],
				Author:     fields[2],
				Email:      fields[3],
				TimeUnixMs: t.UnixMilli(),
				Subject:    fields[6],
			}
			if parents := fields[5]; parents != "" {
				c.Parents = strings.Fields(parents)
			}
			c.Head = c.Hash == head

			if len(fields) >= 8 && fields[7] != "" {
				c.Refs = parseDecorations(fields[7], activeBranch)
			}

			commits = append(commits, c)
		}
	}

	// Trim the probe commit and note whether an older page exists.
	hasMore := len(commits) > CommitLimit
	if hasMore {
		commits = commits[:CommitLimit]
	}

	if uncommitted > 0 && skip == 0 && path == "" {
		workdirCommit := &pb.GitCommit{
			Hash:       "WORKDIR",
			Short:      "WORKDIR",
			Subject:    "Uncommitted Changes",
			Author:     "Working Directory",
			TimeUnixMs: time.Now().UnixMilli(),
			// Staged, unstaged and untracked entries alike, counted by the
			// status call above; no extra git process per log view.
			FilesChanged: int32(uncommitted),
		}
		if head != "" {
			workdirCommit.Parents = []string{head}
		}
		commits = append([]*pb.GitCommit{workdirCommit}, commits...)
	}

	return commits, branches, tags, stashes, hasMore, nil
}

// parseDecorations turns a --decorate=full %D list into refs.
func parseDecorations(decorations, activeBranch string) []*pb.GitRef {
	var refs []*pb.GitRef
	seen := make(map[string]bool)
	add := func(name, typ string, active bool) {
		if name == "" || seen[typ+"\x00"+name] {
			return
		}
		seen[typ+"\x00"+name] = true
		refs = append(refs, &pb.GitRef{Name: name, Type: typ, IsActive: active})
	}
	for _, raw := range strings.Split(decorations, ",") {
		ref := strings.TrimSpace(raw)
		switch {
		case ref == "":
		case strings.HasPrefix(ref, "HEAD -> "):
			name := strings.TrimPrefix(strings.TrimPrefix(ref, "HEAD -> "), "refs/heads/")
			add(name, "branch", true)
			add("HEAD", "head", true)
		case ref == "HEAD":
			add("HEAD", "head", true)
		case strings.HasPrefix(ref, "tag: "):
			add(strings.TrimPrefix(strings.TrimPrefix(ref, "tag: "), "refs/tags/"), "tag", false)
		case ref == "refs/stash":
			add("stash", "stash", false)
		case strings.HasPrefix(ref, "refs/heads/"):
			name := strings.TrimPrefix(ref, "refs/heads/")
			add(name, "branch", activeBranch != "" && name == activeBranch)
		case strings.HasPrefix(ref, "refs/remotes/"):
			name := strings.TrimPrefix(ref, "refs/remotes/")
			if !strings.HasSuffix(name, "/HEAD") {
				add(name, "remote", false)
			}
		}
	}
	return refs
}

func resolveActiveBranch(dir string) string {
	cmd := gitCmd(dir, "symbolic-ref", "--short", "-q", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// GetBranches returns all local and remote branches in dir.
func GetBranches(dir string, activeBranch string) []*pb.GitBranch {
	cmd := gitCmd(dir, "for-each-ref", "refs/heads", "refs/remotes", "--format=%(HEAD)\x1f%(refname)\x1f%(objectname)\x1f%(upstream:short)\x1f%(upstream:track,nobracket)\x1f%(symref)")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var branches []*pb.GitBranch
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 3 {
			continue
		}
		isHead := strings.TrimSpace(parts[0]) == "*"
		full := strings.TrimSpace(parts[1])
		if len(parts) >= 6 && strings.TrimSpace(parts[5]) != "" {
			continue // symbolic refs such as refs/remotes/origin/HEAD
		}
		isRemote := strings.HasPrefix(full, "refs/remotes/")
		name := strings.TrimPrefix(strings.TrimPrefix(full, "refs/heads/"), "refs/remotes/")
		hash := strings.TrimSpace(parts[2])
		upstream := ""
		if len(parts) >= 4 {
			upstream = strings.TrimSpace(parts[3])
		}
		var ahead, behind int32
		if len(parts) >= 5 {
			a, b := parseTracking(parts[4])
			ahead = int32(a)
			behind = int32(b)
		}

		isActive := isHead || (activeBranch != "" && name == activeBranch)

		branches = append(branches, &pb.GitBranch{
			Name:     name,
			Hash:     hash,
			IsActive: isActive,
			IsRemote: isRemote,
			Upstream: upstream,
			Ahead:    ahead,
			Behind:   behind,
		})
	}
	return branches
}

func parseTracking(trackStr string) (int, int) {
	trackStr = strings.TrimSpace(trackStr)
	var ahead, behind int
	if trackStr == "" {
		return 0, 0
	}
	for _, part := range strings.Split(trackStr, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "ahead ") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(part, "ahead "), "%d", &ahead)
		} else if strings.HasPrefix(part, "behind ") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(part, "behind "), "%d", &behind)
		}
	}
	return ahead, behind
}

// GetTags returns all tags in dir.
func GetTags(dir string) []*pb.GitTag {
	// %(*objectname) is the commit an annotated tag points to.
	cmd := gitCmd(dir, "tag", "-l", "--format=%(refname:short)\x1f%(objectname)\x1f%(*objectname)")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var tags []*pb.GitTag
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 2 {
			continue
		}
		hash := strings.TrimSpace(parts[1])
		if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
			hash = strings.TrimSpace(parts[2])
		}
		tags = append(tags, &pb.GitTag{Name: strings.TrimSpace(parts[0]), Hash: hash})
	}
	return tags
}

// GetStashes returns all stashes in dir.
func GetStashes(dir string) []*pb.GitStash {
	cmd := gitCmd(dir, "stash", "list", "--format=%gd\x1f%gs\x1f%H\x1f%aI")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var stashes []*pb.GitStash
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 3 {
			continue
		}
		st := &pb.GitStash{
			Index: strings.TrimSpace(parts[0]),
			Name:  strings.TrimSpace(parts[1]),
			Hash:  strings.TrimSpace(parts[2]),
		}
		if len(parts) >= 4 {
			t, _ := time.Parse(time.RFC3339, strings.TrimSpace(parts[3]))
			st.TimeUnixMs = t.UnixMilli()
		}
		stashes = append(stashes, st)
	}
	return stashes
}

// uncommittedCount counts the entries `git status --porcelain` reports:
// staged, unstaged and untracked alike. It is the cheap source for the
// WORKDIR pseudo-commit's file count.
func uncommittedCount(dir string) int {
	cmd := gitCmd(dir, "status", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// fileStats are per-file +/− counters from --numstat.
type fileStats struct{ add, del int32 }

// unquoteGitPath decodes the C-style quoting git applies to pathnames in
// --porcelain/--name-status/-z-less output when they contain special
// characters (e.g. `?? "sp ace.txt"`).
func unquoteGitPath(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		if unq, err := strconv.Unquote(s); err == nil {
			return unq
		}
	}
	return s
}

// resolveHead returns the full hash of HEAD ("" if it cannot be determined,
// e.g. an unborn branch in an empty repository).
func resolveHead(dir string) string {
	cmd := gitCmd(dir, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	cmd := gitCmd(dir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// RepoRoot returns the absolute path of the git work tree containing dir, or
// dir itself when it is not a git repository.
func RepoRoot(dir string) string {
	cmd := gitCmd(dir, "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return dir
	}
	return strings.TrimSpace(out.String())
}

// Diff returns the commit metadata, changed-file list and unified patch of
// hash (or HEAD when hash is empty) in a single git invocation: --raw gives
// the file list with statuses and rename sources, --numstat the per-file
// +/− counts and --patch the body. headHash is the repository's current HEAD
// hash ("" when unknown); it marks Head and the WORKDIR entry's parent
// without another git process.
func Diff(dir, hash, pathFilter, headHash string, ctxLines int) (*pb.GitDiffResult, error) {
	if dir == "" {
		return nil, fmt.Errorf("git: no working directory")
	}
	if ctxLines <= 0 {
		ctxLines = 3
	}
	if hash == "WORKDIR" {
		return diffWorkdir(dir, pathFilter, headHash, ctxLines)
	}

	if hash == "" {
		hash = resolveHead(dir)
		if hash == "" {
			return nil, fmt.Errorf("git: repository has no HEAD commit")
		}
	}

	format := "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s\x1f%b\x1e"
	args := []string{"show", "--raw", "--numstat", "--patch", fmt.Sprintf("-U%d", ctxLines), fmt.Sprintf("--pretty=format:%s", format)}
	if pathFilter != "" {
		args = append(args, "--", pathFilter)
	}
	args = append(args, hash)
	cmd := gitCmd(dir, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git: %s: %w", msg, err)
	}
	text := out.String()
	if strings.TrimSpace(text) == "" && pathFilter != "" {
		// A pathspec that matches nothing suppresses even the metadata;
		// resolve the commit header alone so the caller still gets it.
		commit, err := showCommitMeta(dir, hash, headHash)
		if err != nil {
			return nil, err
		}
		return &pb.GitDiffResult{Commit: commit}, nil
	}

	// The metadata region ends at its \x1e terminator (the message body may
	// span lines); the machine-readable raw and numstat lines follow it.
	end := strings.IndexByte(text, '\x1e')
	if end < 0 {
		return nil, fmt.Errorf("git: invalid commit metadata output")
	}
	commit, err := commitFromMeta(text[:end], headHash)
	if err != nil {
		return nil, err
	}
	rest := strings.TrimPrefix(text[end+1:], "\n")
	machine, patch := rest, ""
	if start := patchStart(rest); start >= 0 {
		machine, patch = rest[:start], rest[start:]
	}

	raws := map[string]rawEntry{}
	nums := map[string]fileStats{}
	var order []string
	for _, line := range strings.Split(machine, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, ":"):
			if status, oldPath, newPath, ok := parseRawLine(line); ok {
				raws[newPath] = rawEntry{status: status, oldPath: oldPath}
			}
		default:
			if path, stats, ok := parseNumstatLine(line); ok {
				if _, seen := nums[path]; !seen {
					order = append(order, path)
				}
				nums[path] = stats
			}
		}
	}

	var files []*pb.GitFileChange
	for _, path := range order {
		if pathFilter != "" && path != pathFilter {
			continue
		}
		r := raws[path]
		status := ""
		if len(r.status) > 0 {
			status = r.status[:1]
		}
		s := nums[path]
		files = append(files, &pb.GitFileChange{
			Path:      path,
			OldPath:   r.oldPath,
			Status:    status,
			Additions: s.add,
			Deletions: s.del,
		})
	}

	var totalAdd, totalDel int32
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	commit.Additions = totalAdd
	commit.Deletions = totalDel
	commit.FilesChanged = int32(len(files))

	return &pb.GitDiffResult{
		Commit: commit,
		Files:  files,
		Diff:   patch,
	}, nil
}

// rawEntry is a --raw file entry: the status letter with its rename/copy
// score, and for renames and copies the old path.
type rawEntry struct {
	status  string
	oldPath string
}

// parseRawLine parses a --raw line
// ":<oldmode> <newmode> <oldsha> <newsha> <status>\t<path>", where renames
// and copies carry two tab-separated paths, the old one first.
func parseRawLine(line string) (status, oldPath, newPath string, ok bool) {
	head, paths, found := strings.Cut(strings.TrimPrefix(line, ":"), "\t")
	if !found {
		return "", "", "", false
	}
	fields := strings.Fields(head)
	if len(fields) != 5 {
		return "", "", "", false
	}
	if old, next, renamed := strings.Cut(paths, "\t"); renamed {
		return fields[4], unquoteGitPath(old), unquoteGitPath(next), true
	}
	return fields[4], "", unquoteGitPath(paths), true
}

// parseNumstatLine parses a --numstat line
// "<add>\t<del>\t<path>", where binary files show "-" counts and renames
// show "<old> => <new>" in the path column. Renames are keyed by their new
// path; the old one comes from --raw.
func parseNumstatLine(line string) (path string, s fileStats, ok bool) {
	first := strings.IndexByte(line, '\t')
	if first < 0 {
		return "", fileStats{}, false
	}
	rest := line[first+1:]
	second := strings.IndexByte(rest, '\t')
	if second < 0 {
		return "", fileStats{}, false
	}
	addStr, delStr := line[:first], rest[:second]
	pathPart := rest[second+1:]
	if !isDiffCount(addStr) || !isDiffCount(delStr) {
		return "", fileStats{}, false
	}
	if _, newPath, renamed := strings.Cut(pathPart, " => "); renamed {
		pathPart = newPath
	}
	var stats fileStats
	if addStr != "-" {
		n, err := strconv.ParseInt(addStr, 10, 32)
		if err != nil {
			return "", fileStats{}, false
		}
		stats.add = int32(n)
	}
	if delStr != "-" {
		n, err := strconv.ParseInt(delStr, 10, 32)
		if err != nil {
			return "", fileStats{}, false
		}
		stats.del = int32(n)
	}
	return unquoteGitPath(pathPart), stats, true
}

func isDiffCount(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// patchStart returns the offset of the first patch header ("diff --git", or
// the "diff --cc" combined header of merges) in a combined
// --raw/--numstat/--patch output, or -1 when there is no patch.
func patchStart(text string) int {
	for _, prefix := range []string{"diff --git ", "diff --cc "} {
		if strings.HasPrefix(text, prefix) {
			return 0
		}
		if i := strings.Index(text, "\n"+prefix); i >= 0 {
			return i + 1
		}
	}
	return -1
}

// parseNumstat extracts the --numstat entries of a combined
// --numstat/--patch output: the machine-readable lines that precede the
// first patch header.
func parseNumstat(text string) map[string]fileStats {
	stats := map[string]fileStats{}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "diff --cc ") {
			break
		}
		if path, s, ok := parseNumstatLine(line); ok {
			stats[path] = s
		}
	}
	return stats
}

// commitFromMeta builds a commit from the metadata region of the pretty
// format "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s\x1f%b": the fields up to
// and including the subject, then the message body, which may span lines.
func commitFromMeta(meta, headHash string) (*pb.GitCommit, error) {
	fields := strings.Split(meta, "\x1f")
	if len(fields) < 8 {
		return nil, fmt.Errorf("git: invalid commit metadata output")
	}
	t, _ := time.Parse(time.RFC3339, fields[4])
	c := &pb.GitCommit{
		Hash:       fields[0],
		Short:      fields[1],
		Author:     fields[2],
		Email:      fields[3],
		TimeUnixMs: t.UnixMilli(),
		Subject:    fields[6],
		Head:       headHash != "" && fields[0] == headHash,
	}
	if parents := fields[5]; parents != "" {
		c.Parents = strings.Fields(parents)
	}
	if body := strings.TrimSpace(strings.Join(fields[7:], "\x1f")); body != "" {
		c.Body = body
	}
	return c, nil
}

// showCommitMeta resolves just a commit's metadata (`git show -s`), for
// outputs whose diff section is suppressed.
func showCommitMeta(dir, hash, headHash string) (*pb.GitCommit, error) {
	format := "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s\x1f%b"
	cmd := gitCmd(dir, "show", "-s", fmt.Sprintf("--pretty=format:%s", format), hash)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git: %s: %w", msg, err)
	}
	return commitFromMeta(strings.TrimSpace(out.String()), headHash)
}

// diffWorkdir builds the WORKDIR diff: the file list with staged/unstaged/
// untracked flags from `git status --porcelain`, and stats plus patches from
// one `git diff HEAD --numstat --patch` call. Untracked files are absent
// from `git diff HEAD`; their new-file patches are rendered in-process.
func diffWorkdir(dir, pathFilter, headHash string, ctxLines int) (*pb.GitDiffResult, error) {
	commit := &pb.GitCommit{
		Hash:       "WORKDIR",
		Short:      "WORKDIR",
		Author:     "Working Directory",
		Subject:    "Uncommitted Changes",
		TimeUnixMs: time.Now().UnixMilli(),
	}
	if headHash != "" {
		commit.Parents = []string{headHash}
	}

	cmdStatus := gitCmd(dir, "status", "--porcelain")
	var outStatus bytes.Buffer
	cmdStatus.Stdout = &outStatus
	_ = cmdStatus.Run()

	var files []*pb.GitFileChange
	statusLines := strings.Split(outStatus.String(), "\n")
	for _, rawLine := range statusLines {
		if len(rawLine) < 3 {
			continue
		}
		stX := rawLine[0]
		stY := rawLine[1]
		rest := strings.TrimSpace(rawLine[3:])
		var path string
		oldPath := ""
		if strings.Contains(rest, " -> ") {
			parts := strings.Split(rest, " -> ")
			oldPath = unquoteGitPath(parts[0])
			path = unquoteGitPath(parts[1])
		} else {
			path = unquoteGitPath(rest)
		}

		if pathFilter != "" && path != pathFilter {
			continue
		}

		// Check for untracked file
		if stX == '?' && stY == '?' {
			files = append(files, &pb.GitFileChange{
				Path:      path,
				Status:    "A",
				Untracked: true,
			})
			continue
		}

		// Check for staged changes (stX != ' ')
		if stX != ' ' {
			files = append(files, &pb.GitFileChange{
				Path:    path,
				OldPath: oldPath,
				Status:  string(stX),
				Staged:  true,
			})
		}

		// Check for unstaged changes (stY != ' ')
		if stY != ' ' {
			files = append(files, &pb.GitFileChange{
				Path:     path,
				OldPath:  oldPath,
				Status:   string(stY),
				Unstaged: true,
			})
		}
	}

	// Tracked changes: per-file +/− counts and the patch in one call. Only
	// the patch section is returned; the numstat lines above it feed stats.
	diffArgs := []string{"diff", "HEAD", "--numstat", "--patch", fmt.Sprintf("-U%d", ctxLines)}
	if pathFilter != "" {
		diffArgs = append(diffArgs, "--", pathFilter)
	}
	cmdDiff := gitCmd(dir, diffArgs...)
	var outDiff bytes.Buffer
	cmdDiff.Stdout = &outDiff
	_ = cmdDiff.Run()
	diffText := outDiff.String()
	stats := parseNumstat(diffText)
	if start := patchStart(diffText); start >= 0 {
		diffText = diffText[start:]
	} else {
		diffText = ""
	}
	for _, f := range files {
		if s, ok := stats[f.Path]; ok {
			f.Additions = s.add
			f.Deletions = s.del
		}
	}

	// Untracked (and, in a repo without commits, staged-new) files carry no
	// `git diff HEAD` entry: render their new-file patches in-process instead
	// of spawning `git diff --no-index` per file. A file only counts as
	// missing while the patch text has no `+++ b/<path>` header for it.
	for _, f := range files {
		if !(f.Untracked || f.Status == "A") || strings.Contains(diffText, "+++ b/"+f.Path) {
			continue
		}
		patch, add, _ := newFilePatch(dir, f.Path)
		if patch == "" {
			continue
		}
		f.Additions, f.Deletions = add, 0
		if diffText != "" && !strings.HasSuffix(diffText, "\n") {
			diffText += "\n"
		}
		diffText += patch
	}

	var totalAdd, totalDel int32
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	commit.Additions = totalAdd
	commit.Deletions = totalDel
	commit.FilesChanged = int32(len(files))

	return &pb.GitDiffResult{
		Commit: commit,
		Files:  files,
		Diff:   diffText,
	}, nil
}

// newFilePatch renders the new-file patch of a path in-process, in the same
// shape `git diff --no-index -- /dev/null <path>` produces, so the
// working-tree diff needs no per-file git invocation.
func newFilePatch(dir, path string) (patch string, add, del int32) {
	full := filepath.Join(dir, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", 0, 0
	}
	mode := "100644"
	if fi, err := os.Stat(full); err == nil && fi.Mode()&0o111 != 0 {
		mode = "100755"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
	fmt.Fprintf(&b, "new file mode %s\n", mode)
	fmt.Fprintf(&b, "index 0000000..%s\n", blobHash(data))
	if bytes.IndexByte(data, 0) >= 0 {
		fmt.Fprintf(&b, "Binary files /dev/null and b/%s differ\n", path)
		return b.String(), 0, 0
	}
	// A trailing newline ends the last line rather than starting an empty
	// one; without it the last line carries the "No newline" marker.
	lines := strings.Split(string(data), "\n")
	noNewline := false
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	} else {
		noNewline = true
	}
	if len(lines) > 0 {
		fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", path)
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, l := range lines {
			b.WriteString("+")
			b.WriteString(l)
			b.WriteString("\n")
		}
		if noNewline {
			b.WriteString("\\ No newline at end of file\n")
		}
	}
	return b.String(), int32(len(lines)), 0
}

// blobHash returns the abbreviated git blob hash of data.
func blobHash(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d", len(data))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))[:7]
}

// Stage stages or unstages files in the working directory.
func Stage(dir string, path string, stageAll bool, unstage bool) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}

	var args []string
	if unstage {
		args = []string{"restore", "--staged"}
		if stageAll {
			args = append(args, ".")
		} else if path != "" {
			args = append(args, "--", path)
		} else {
			return fmt.Errorf("git: no path specified for unstage")
		}
	} else {
		args = []string{"add"}
		if stageAll {
			args = append(args, "-A", ".")
		} else if path != "" {
			args = append(args, "--", path)
		} else {
			return fmt.Errorf("git: no path specified for stage")
		}
	}

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	setProcessGroup(cmd)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s: %w", args[0], msg, err)
	}
	return nil
}

// Commit creates a new commit with the given message.
func Commit(dir string, message string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("git: commit message cannot be empty")
	}

	cmd := exec.Command("git", "-C", dir, "commit", "-m", message)
	setProcessGroup(cmd)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git commit: %s: %w", msg, err)
	}
	return nil
}

// Amend folds the staged changes into HEAD instead of creating a commit:
// message replaces HEAD's message, or HEAD's message is kept when message
// is empty. Nothing is committed away without a message.
func Amend(dir, message string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	args := []string{"commit", "--amend"}
	if strings.TrimSpace(message) == "" {
		args = append(args, "--no-edit")
	} else {
		args = append(args, "-m", message)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	setProcessGroup(cmd)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git commit --amend: %s: %w", msg, err)
	}
	return nil
}

// stashIndexRe matches a stash reference as `git stash list` prints it.
var stashIndexRe = regexp.MustCompile(`^stash@\{\d+\}$`)

// stashTarget returns the stash to act on, defaulting to the newest.
func stashTarget(index string) (string, error) {
	if index == "" {
		return "stash@{0}", nil
	}
	if !stashIndexRe.MatchString(index) {
		return "", fmt.Errorf("git: invalid stash index %q", index)
	}
	return index, nil
}

// runStash runs a `git stash` subcommand in dir. op names the subcommand for
// error messages.
func runStash(dir, op string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir, "stash"}, args...)...)
	setProcessGroup(cmd)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git stash %s: %s: %w", op, msg, err)
	}
	return nil
}

// StashPush stashes the working tree changes (staged and unstaged), all of
// them or just paths. With includeUntracked, untracked files are stashed
// too (ignored files are never stashed).
func StashPush(dir string, paths []string, message string, includeUntracked bool) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	args := []string{"push"}
	if includeUntracked {
		args = append(args, "--include-untracked")
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "--message", message)
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return runStash(dir, "push", args...)
}

// StashPop restores a stash's changes into the working tree and removes the
// stash entry (index empty = the newest, "stash@{n}" otherwise). On a
// conflict the stash is kept and an error is returned.
func StashPop(dir, index string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	stash, err := stashTarget(index)
	if err != nil {
		return err
	}
	return runStash(dir, "pop", "pop", stash)
}

// StashApply restores a stash's changes into the working tree and keeps the
// stash entry (index empty = the newest). On a conflict nothing is applied
// and an error is returned.
func StashApply(dir, index string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	stash, err := stashTarget(index)
	if err != nil {
		return err
	}
	return runStash(dir, "apply", "apply", stash)
}

// StashDrop removes a stash entry (index empty = the newest).
func StashDrop(dir, index string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	stash, err := stashTarget(index)
	if err != nil {
		return err
	}
	return runStash(dir, "drop", "drop", stash)
}

// runGit runs a git subcommand in dir and wraps its stderr into the error.
// Unlike gitCmd it is not read-only: callers let it lock and refresh the
// index (checkout, branch, restore, clean).
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	setProcessGroup(cmd)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s: %w", args[0], msg, err)
	}
	return nil
}

// validBranchName reports whether name is acceptable as a branch name
// (`git check-ref-format --branch`); values that git would parse as options
// are rejected outright.
func validBranchName(dir, name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\n\r\t") {
		return fmt.Errorf("git: invalid branch name %q", name)
	}
	cmd := gitCmd(dir, "check-ref-format", "--branch", name)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git: %s", strings.TrimPrefix(msg, "fatal: "))
	}
	return nil
}

// validRev validates a start point (a branch name or commit hash): git
// resolves it and rejects what it cannot be, but it must not smuggle in
// options or whitespace.
func validRev(rev string) error {
	if rev == "" {
		return nil
	}
	if strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, " \x00\n\r\t") {
		return fmt.Errorf("git: invalid start point %q", rev)
	}
	return nil
}

// refExists reports whether the exactly named ref exists.
func refExists(dir, ref string) bool {
	return gitCmd(dir, "show-ref", "--verify", "--quiet", ref).Run() == nil
}

// Checkout checks out a local branch, or the local counterpart of a remote
// branch ("origin/feature" -> "feature", tracking the remote branch). The
// remote form fails when the local branch already exists; git refuses to
// switch when uncommitted changes would be overwritten.
func Checkout(dir, branch string) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	if err := validBranchName(dir, branch); err != nil {
		return err
	}
	if refExists(dir, "refs/heads/"+branch) {
		return runGit(dir, "checkout", branch)
	}
	if i := strings.IndexByte(branch, '/'); i > 0 && refExists(dir, "refs/remotes/"+branch) {
		return runGit(dir, "checkout", "-b", branch[i+1:], branch)
	}
	return fmt.Errorf("git: no branch %q", branch)
}

// BranchCreate creates a branch at start (at HEAD when start is empty) and
// optionally checks it out.
func BranchCreate(dir, name, start string, checkout bool) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	if err := validBranchName(dir, name); err != nil {
		return err
	}
	if err := validRev(start); err != nil {
		return err
	}
	args := []string{"branch", name}
	if checkout {
		args = []string{"checkout", "-b", name}
	}
	if start != "" {
		args = append(args, start)
	}
	return runGit(dir, args...)
}

// pathSpec matches path wherever it sits in the repository: paths as the
// porcelain formats report them (and so the dashboard's file lists) are
// repository-relative, while plain pathspecs resolve against the working
// directory, which need not be the repository root.
func pathSpec(path string) string {
	return ":(top,literal)" + path
}

// isTracked reports whether path is in the index.
func isTracked(dir, path string) bool {
	cmd := gitCmd(dir, "ls-files", "--", pathSpec(path))
	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// Restore discards the local changes of a path — or, with all, of every path
// under the project's directory: a tracked path goes back to its committed
// state (staged and unstaged changes alike) and an untracked path is
// deleted. Ignored files are never touched.
func Restore(dir, path string, all bool) error {
	if dir == "" {
		return fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return fmt.Errorf("git: %s is not a git repository", dir)
	}
	if all {
		if err := runGit(dir, "restore", "--source=HEAD", "--staged", "--worktree", "--", "."); err != nil {
			return err
		}
		return runGit(dir, "clean", "-fd", "--", ".")
	}
	if path == "" {
		return fmt.Errorf("git: no path specified for restore")
	}
	if isTracked(dir, path) {
		if err := runGit(dir, "restore", "--source=HEAD", "--staged", "--worktree", "--", pathSpec(path)); err != nil {
			return err
		}
	}
	return runGit(dir, "clean", "-fd", "--", pathSpec(path))
}

// Push pushes the current branch to its upstream remote.
func Push(ctx context.Context, dir string, remote string) (string, error) {
	return remoteCmd(ctx, dir, "push", remote)
}

// Pull pulls changes from the current branch's upstream remote.
func Pull(ctx context.Context, dir string, remote string) (string, error) {
	return remoteCmd(ctx, dir, "pull", remote)
}

// Fetch fetches refs from the remote without modifying the working directory.
func Fetch(ctx context.Context, dir string, remote string) (string, error) {
	return remoteCmd(ctx, dir, "fetch", remote)
}

// Status runs `git status --porcelain=v2 --branch` in dir and parses the result
// into a pb.GitStatus summary.
func Status(dir string) (*pb.GitStatus, error) {
	if dir == "" || !IsRepo(dir) {
		return &pb.GitStatus{
			IsRepo:  false,
			IsClean: true,
		}, nil
	}

	cmd := gitCmd(dir, "status", "--porcelain=v2", "--branch")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git status: %s: %w", msg, err)
	}

	res := &pb.GitStatus{
		IsRepo:  true,
		IsClean: true,
	}

	for _, line := range strings.Split(outBuf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# branch.oid ") {
			res.HeadHash = strings.TrimPrefix(line, "# branch.oid ")
			if res.HeadHash == "(initial)" {
				res.HeadHash = ""
			}
		} else if strings.HasPrefix(line, "# branch.head ") {
			res.Branch = strings.TrimPrefix(line, "# branch.head ")
		} else if strings.HasPrefix(line, "# branch.upstream ") {
			res.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		} else if strings.HasPrefix(line, "# branch.ab ") {
			parts := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			for _, p := range parts {
				if strings.HasPrefix(p, "+") {
					_, _ = fmt.Sscanf(p, "+%d", &res.Ahead)
				} else if strings.HasPrefix(p, "-") {
					_, _ = fmt.Sscanf(p, "-%d", &res.Behind)
				}
			}
		} else if strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && len(fields[1]) >= 2 {
				stagedChar := fields[1][0]
				dirtyChar := fields[1][1]
				if stagedChar != '.' {
					res.Staged++
				}
				if dirtyChar != '.' {
					res.Dirty++
				}
			}
		} else if strings.HasPrefix(line, "u ") {
			res.Conflicts++
		} else if strings.HasPrefix(line, "? ") {
			res.Untracked++
		}
	}

	res.IsClean = (res.Staged == 0 && res.Dirty == 0 && res.Untracked == 0 && res.Conflicts == 0)
	return res, nil
}

// Fingerprint summarizes the working tree: the porcelain status plus the
// size and modification time of every changed path, so edits to files that
// were already modified are noticed too. root is the repository root the
// caller caches ("" to resolve it here); resolving it per call would cost a
// git process on every poll. It returns "" for non-repos.
func Fingerprint(dir, root string) string {
	cmd := gitCmd(dir, "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=normal")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write(out)
	// Porcelain paths are relative to the repository root.
	if root == "" {
		root = RepoRoot(dir)
	}
	if root == "" {
		root = dir
	}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) < 4 || bytes.HasPrefix(entry, []byte("## ")) {
			continue
		}
		path := filepath.Join(root, string(entry[3:]))
		if fi, err := os.Stat(path); err == nil {
			_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00", path, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
