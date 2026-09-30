// Package gitlog runs `git log` in a working directory and parses the result
// into wire format. It is used by the orchestrator (project-level git view)
// and by the single-project control backend.
package gitlog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// first, along with branches, tags, and stashes.
func Log(dir string) ([]*pb.GitCommit, []*pb.GitBranch, []*pb.GitTag, []*pb.GitStash, error) {
	if dir == "" {
		return nil, nil, nil, nil, fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return nil, nil, nil, nil, fmt.Errorf("git: %s is not a git repository", dir)
	}

	head := resolveHead(dir)
	activeBranch := resolveActiveBranch(dir)
	hasUncommitted := checkUncommitted(dir)

	branches := GetBranches(dir, activeBranch)
	tags := GetTags(dir)
	stashes := GetStashes(dir)

	// An empty repository (unborn branch) has no commits, so `git log` would
	// fail with exit 128. Surface that as an empty log (or uncommitted changes) rather than an error.
	if head == "" {
		if hasUncommitted {
			return []*pb.GitCommit{{
				Hash:       "WORKDIR",
				Short:      "WORKDIR",
				Subject:    "Uncommitted Changes",
				Author:     "Working Directory",
				TimeUnixMs: time.Now().UnixMilli(),
			}}, branches, tags, stashes, nil
		}
		return nil, branches, tags, stashes, nil
	}

	format := "\x1e%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s\x1f%D"
	cmd := gitCmd(dir, "log", "--all", "--shortstat", fmt.Sprintf("--pretty=format:%s", format), "-n", fmt.Sprintf("%d", CommitLimit))
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, nil, nil, nil, fmt.Errorf("git: %s: %w", msg, err)
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

			for _, l := range lines[1:] {
				l = strings.TrimSpace(l)
				if strings.Contains(l, "changed") {
					fc, add, del := parseShortstat(l)
					c.FilesChanged = fc
					c.Additions = add
					c.Deletions = del
					break
				}
			}

			if len(fields) >= 8 && fields[7] != "" {
				var refs []*pb.GitRef
				seenRefs := make(map[string]bool)

				for _, rawRef := range strings.Split(fields[7], ",") {
					ref := strings.TrimSpace(rawRef)
					if ref == "" {
						continue
					}
					if strings.HasPrefix(ref, "HEAD -> ") {
						bName := strings.TrimPrefix(ref, "HEAD -> ")
						if !seenRefs[bName] {
							seenRefs[bName] = true
							refs = append(refs, &pb.GitRef{
								Name:     bName,
								Type:     "branch",
								IsActive: true,
							})
						}
						if !seenRefs["HEAD"] {
							seenRefs["HEAD"] = true
							refs = append(refs, &pb.GitRef{
								Name:     "HEAD",
								Type:     "head",
								IsActive: true,
							})
						}
					} else if ref == "HEAD" {
						if !seenRefs["HEAD"] {
							seenRefs["HEAD"] = true
							refs = append(refs, &pb.GitRef{
								Name:     "HEAD",
								Type:     "head",
								IsActive: true,
							})
						}
					} else if strings.HasPrefix(ref, "tag: ") {
						tName := strings.TrimPrefix(ref, "tag: ")
						if !seenRefs[tName] {
							seenRefs[tName] = true
							refs = append(refs, &pb.GitRef{
								Name: tName,
								Type: "tag",
							})
						}
					} else if ref == "stash" || strings.HasPrefix(ref, "refs/stash") {
						if !seenRefs[ref] {
							seenRefs[ref] = true
							refs = append(refs, &pb.GitRef{
								Name: ref,
								Type: "stash",
							})
						}
					} else {
						if !seenRefs[ref] {
							seenRefs[ref] = true
							isRemote := strings.Contains(ref, "/") && !strings.HasPrefix(ref, "heads/")
							refType := "branch"
							if isRemote {
								refType = "remote"
							}
							isActive := activeBranch != "" && ref == activeBranch
							refs = append(refs, &pb.GitRef{
								Name:     ref,
								Type:     refType,
								IsActive: isActive,
							})
						}
					}
				}
				c.Refs = refs
			}

			commits = append(commits, c)
		}
	}

	if hasUncommitted {
		workdirCommit := &pb.GitCommit{
			Hash:       "WORKDIR",
			Short:      "WORKDIR",
			Subject:    "Uncommitted Changes",
			Author:     "Working Directory",
			TimeUnixMs: time.Now().UnixMilli(),
		}
		if head != "" {
			workdirCommit.Parents = []string{head}
		}
		cmdWorkdirStat := gitCmd(dir, "diff", "HEAD", "--shortstat")
		var outWorkdirStat bytes.Buffer
		cmdWorkdirStat.Stdout = &outWorkdirStat
		if cmdWorkdirStat.Run() == nil {
			fc, add, del := parseShortstat(outWorkdirStat.String())
			workdirCommit.FilesChanged = fc
			workdirCommit.Additions = add
			workdirCommit.Deletions = del
		}
		commits = append([]*pb.GitCommit{workdirCommit}, commits...)
	}

	return commits, branches, tags, stashes, nil
}

func parseShortstat(s string) (filesChanged, additions, deletions int32) {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "file changed") || strings.Contains(part, "files changed") {
			var n int32
			if _, err := fmt.Sscanf(part, "%d", &n); err == nil {
				filesChanged = n
			}
		} else if strings.Contains(part, "insertion") {
			var n int32
			if _, err := fmt.Sscanf(part, "%d", &n); err == nil {
				additions = n
			}
		} else if strings.Contains(part, "deletion") {
			var n int32
			if _, err := fmt.Sscanf(part, "%d", &n); err == nil {
				deletions = n
			}
		}
	}
	return filesChanged, additions, deletions
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
	cmd := gitCmd(dir, "branch", "-a", "--format=%(HEAD)\x1f%(refname:short)\x1f%(objectname)\x1f%(upstream:short)\x1f%(upstream:track,nobracket)")
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
		name := strings.TrimSpace(parts[1])
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

		isRemote := strings.HasPrefix(name, "remotes/") || strings.HasPrefix(name, "origin/")
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
	cmd := gitCmd(dir, "tag", "-l", "--format=%(refname:short)\x1f%(objectname)")
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
		tags = append(tags, &pb.GitTag{
			Name: strings.TrimSpace(parts[0]),
			Hash: strings.TrimSpace(parts[1]),
		})
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

func checkUncommitted(dir string) bool {
	cmd := gitCmd(dir, "status", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return len(bytes.TrimSpace(out.Bytes())) > 0
}

// fileStats are per-file +/- counters computed from a patch body.
type fileStats struct{ add, del int32 }

// parseStatsFromPatch computes +/− stats per file from a unified patch,
// avoiding a separate `git show --numstat` spawn. The current file is the
// `diff --git a/x b/y` header; lines starting with + / - count as
// additions / deletions, with `--- ` and `+++ ` headers excluded — the same
// rules as the frontend parser.
func parseStatsFromPatch(patch string) map[string]fileStats {
	stats := make(map[string]fileStats)
	current := ""
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			idx := strings.LastIndex(line, " b/")
			if idx < 0 {
				current = ""
				continue
			}
			current = unquoteGitPath(line[idx+len(" b/"):])
			continue
		}
		if current == "" {
			continue
		}
		s := stats[current]
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			s.add++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			s.del++
		}
		stats[current] = s
	}
	return stats
}

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

// Diff returns the commit metadata, list of changed files, and unified patch diff for hash (or HEAD if hash is empty).
func Diff(dir string, hash string, pathFilter string, contextLines ...int) (*pb.GitDiffResult, error) {
	if dir == "" {
		return nil, fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return nil, fmt.Errorf("git: %s is not a git repository", dir)
	}

	ctxLines := 3
	if len(contextLines) > 0 && contextLines[0] > 0 {
		ctxLines = contextLines[0]
	}

	if hash == "WORKDIR" {
		return diffWorkdir(dir, pathFilter, ctxLines)
	}

	if hash == "" {
		hash = resolveHead(dir)
	}
	if hash == "" {
		return nil, fmt.Errorf("git: repository has no HEAD commit")
	}

	format := "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s"
	cmdMeta := gitCmd(dir, "show", "-s", fmt.Sprintf("--pretty=format:%s", format), hash)
	var outMeta, errMeta bytes.Buffer
	cmdMeta.Stdout = &outMeta
	cmdMeta.Stderr = &errMeta
	if err := cmdMeta.Run(); err != nil {
		msg := strings.TrimSpace(errMeta.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git: %s: %w", msg, err)
	}

	fields := strings.Split(strings.TrimSpace(outMeta.String()), "\x1f")
	if len(fields) < 7 {
		return nil, fmt.Errorf("git: invalid commit metadata output")
	}

	head := resolveHead(dir)
	t, _ := time.Parse(time.RFC3339, fields[4])
	commit := &pb.GitCommit{
		Hash:       fields[0],
		Short:      fields[1],
		Author:     fields[2],
		Email:      fields[3],
		TimeUnixMs: t.UnixMilli(),
		Subject:    fields[6],
		Head:       fields[0] == head,
	}
	if parents := fields[5]; parents != "" {
		commit.Parents = strings.Fields(parents)
	}

	diffArgs := []string{"show", fmt.Sprintf("-U%d", ctxLines), "--patch", "--format=", hash}
	if pathFilter != "" {
		diffArgs = append(diffArgs, "--", pathFilter)
	}
	cmdDiff := gitCmd(dir, diffArgs...)
	var outDiff bytes.Buffer
	cmdDiff.Stdout = &outDiff
	_ = cmdDiff.Run()
	statsFromPatch := parseStatsFromPatch(outDiff.String())

	cmdStatus := gitCmd(dir, "show", "--name-status", "--format=", hash)
	var outStatus bytes.Buffer
	cmdStatus.Stdout = &outStatus
	_ = cmdStatus.Run()

	var files []*pb.GitFileChange
	for _, line := range strings.Split(outStatus.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			st := parts[0]
			path := unquoteGitPath(parts[1])
			oldPath := ""
			if len(parts) >= 3 {
				oldPath = unquoteGitPath(parts[1])
				path = unquoteGitPath(parts[2])
			}
			if pathFilter != "" && path != pathFilter {
				continue
			}
			s := statsFromPatch[path]
			statusLetter := st
			if len(st) > 0 {
				statusLetter = string(st[0])
			}
			files = append(files, &pb.GitFileChange{
				Path:      path,
				OldPath:   oldPath,
				Status:    statusLetter,
				Additions: s.add,
				Deletions: s.del,
			})
		}
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
		Diff:   outDiff.String(),
	}, nil
}

func diffWorkdir(dir string, pathFilter string, ctxLines int) (*pb.GitDiffResult, error) {
	head := resolveHead(dir)
	commit := &pb.GitCommit{
		Hash:       "WORKDIR",
		Short:      "WORKDIR",
		Author:     "Working Directory",
		Subject:    "Uncommitted Changes",
		TimeUnixMs: time.Now().UnixMilli(),
	}
	if head != "" {
		commit.Parents = []string{head}
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

	diffArgs := []string{"diff", "HEAD", fmt.Sprintf("-U%d", ctxLines)}
	if pathFilter != "" {
		diffArgs = append(diffArgs, "--", pathFilter)
	}
	cmdDiff := gitCmd(dir, diffArgs...)
	var outDiff bytes.Buffer
	cmdDiff.Stdout = &outDiff
	_ = cmdDiff.Run()

	diffText := outDiff.String()

	for _, f := range files {
		// An added/untracked file only appears in the `git diff HEAD` output
		// once it carries a `+++ b/<path>` patch header; use that instead of a
		// raw substring match so unrelated paths can't suppress the fallback.
		if (f.Untracked || f.Status == "A") && !strings.Contains(diffText, "+++ b/"+f.Path) {
			cmdUntracked := gitCmd(dir, "diff", "--no-index", "/dev/null", f.Path)
			var outUntracked bytes.Buffer
			cmdUntracked.Stdout = &outUntracked
			_ = cmdUntracked.Run()
			if outUntracked.Len() > 0 {
				if diffText != "" && !strings.HasSuffix(diffText, "\n") {
					diffText += "\n"
				}
				diffText += outUntracked.String()
			}
		}
	}

	// Fill per-file +/− stats from the patch body (including appended
	// new-file fallbacks) instead of spawning `git diff --numstat`.
	statsFromPatch := parseStatsFromPatch(diffText)
	for _, f := range files {
		if s, ok := statsFromPatch[f.Path]; ok {
			f.Additions = s.add
			f.Deletions = s.del
		}
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
// were already modified are noticed too. It returns "" for non-repos.
func Fingerprint(dir string) string {
	cmd := gitCmd(dir, "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=normal")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write(out)
	// Porcelain paths are relative to the repository root.
	root := RepoRoot(dir)
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
