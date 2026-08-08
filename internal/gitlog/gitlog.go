// Package gitlog runs `git log` in a working directory and parses the result
// into wire format. It is used by the orchestrator (project-level git view)
// and by the single-project control backend.
package gitlog

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// CommitLimit caps how many commits are returned per request so a large
// repository can't swamp the control socket in one frame.
const CommitLimit = 100

// Log runs `git log` in dir and returns up to CommitLimit commits, newest
// first. Field separators are ASCII unit (0x1f); each commit is one line.
// Git's raw author date (%aI) is RFC3339, matching protocol.FormatTime.
func Log(dir string) ([]protocol.GitCommit, error) {
	if dir == "" {
		return nil, fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return nil, fmt.Errorf("git: %s is not a git repository", dir)
	}

	head := resolveHead(dir)

	// An empty repository (unborn branch) has no commits, so `git log` would
	// fail with exit 128. Surface that as an empty log rather than an error.
	if head == "" {
		return nil, nil
	}

	format := "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s"
	cmd := exec.Command("git", "-C", dir, "log", fmt.Sprintf("--pretty=format:%s", format), "-n", fmt.Sprintf("%d", CommitLimit))
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

	raw := out.String()
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	commits := make([]protocol.GitCommit, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\x1f")
		if len(fields) < 7 {
			continue
		}
		c := protocol.GitCommit{
			Hash:    fields[0],
			Short:   fields[1],
			Author:  fields[2],
			Email:   fields[3],
			Time:    fields[4],
			Subject: fields[6],
		}
		if parents := fields[5]; parents != "" {
			c.Parents = strings.Fields(parents)
		}
		c.Head = c.Hash == head
		commits = append(commits, c)
	}
	return commits, nil
}

// resolveHead returns the full hash of HEAD ("" if it cannot be determined,
// e.g. an unborn branch in an empty repository).
func resolveHead(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// RepoRoot returns the absolute path of the git work tree containing dir, or
// dir itself when it is not a git repository.
func RepoRoot(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return dir
	}
	return strings.TrimSpace(out.String())
}

// Diff returns the commit metadata, list of changed files, and unified patch diff for hash (or HEAD if hash is empty).
func Diff(dir string, hash string) (*protocol.GitDiffResult, error) {
	if dir == "" {
		return nil, fmt.Errorf("git: no working directory")
	}
	if !IsRepo(dir) {
		return nil, fmt.Errorf("git: %s is not a git repository", dir)
	}

	if hash == "" {
		hash = resolveHead(dir)
	}
	if hash == "" {
		return nil, fmt.Errorf("git: repository has no HEAD commit")
	}

	format := "%H\x1f%h\x1f%an\x1f%ae\x1f%aI\x1f%P\x1f%s"
	cmdMeta := exec.Command("git", "-C", dir, "show", "-s", fmt.Sprintf("--pretty=format:%s", format), hash)
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
	commit := protocol.GitCommit{
		Hash:    fields[0],
		Short:   fields[1],
		Author:  fields[2],
		Email:   fields[3],
		Time:    fields[4],
		Subject: fields[6],
		Head:    fields[0] == head,
	}
	if parents := fields[5]; parents != "" {
		commit.Parents = strings.Fields(parents)
	}

	cmdStatus := exec.Command("git", "-C", dir, "show", "--name-status", "--format=", hash)
	var outStatus bytes.Buffer
	cmdStatus.Stdout = &outStatus
	_ = cmdStatus.Run()

	cmdNumstat := exec.Command("git", "-C", dir, "show", "--numstat", "--format=", hash)
	var outNumstat bytes.Buffer
	cmdNumstat.Stdout = &outNumstat
	_ = cmdNumstat.Run()

	type stats struct{ add, del int }
	numstatMap := make(map[string]stats)
	for _, line := range strings.Split(outNumstat.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			var add, del int
			_, _ = fmt.Sscanf(parts[0], "%d", &add)
			_, _ = fmt.Sscanf(parts[1], "%d", &del)
			path := parts[len(parts)-1]
			numstatMap[path] = stats{add: add, del: del}
		}
	}

	var files []protocol.GitFileChange
	for _, line := range strings.Split(outStatus.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			st := parts[0]
			path := parts[1]
			oldPath := ""
			if len(parts) >= 3 {
				oldPath = parts[1]
				path = parts[2]
			}
			s := numstatMap[path]
			statusLetter := st
			if len(st) > 0 {
				statusLetter = string(st[0])
			}
			files = append(files, protocol.GitFileChange{
				Path:      path,
				OldPath:   oldPath,
				Status:    statusLetter,
				Additions: s.add,
				Deletions: s.del,
			})
		}
	}

	cmdDiff := exec.Command("git", "-C", dir, "show", "--patch", "--format=", hash)
	var outDiff bytes.Buffer
	cmdDiff.Stdout = &outDiff
	_ = cmdDiff.Run()

	return &protocol.GitDiffResult{
		Commit: commit,
		Files:  files,
		Diff:   outDiff.String(),
	}, nil
}
