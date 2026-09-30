package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// artifacts are the per-test-binary build outputs shared by every sandbox.
type artifacts struct {
	repoRoot  string
	binDir    string // devyard + fixtures + stubs; prepended to PATH
	devyard   string
	buildErr  error // non-nil when the devyard binary failed to build
	runPrefix string
	seq       atomic.Int64
}

var (
	artMu sync.Mutex
	art   *artifacts
)

func current() *artifacts {
	artMu.Lock()
	defer artMu.Unlock()
	return art
}

// Fixtures lists the fixture programs under test/e2e/fixtures.
var Fixtures = []string{"httpecho", "ticker", "exiter", "prompter", "sigtrap", "forker"}

// Main is the TestMain of every e2e suite: it builds the binaries, reaps
// processes left behind by crashed earlier runs, runs the tests, then reaps
// anything this run leaked and removes the build dir.
func Main(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	cleanup, err := Setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e harness setup: %v\n", err)
		return 1
	}
	code := m.Run()
	if leaked := cleanup(); leaked > 0 && code == 0 {
		code = 1
	}
	return code
}

// Setup builds the devyard binary (with -race when this binary is
// race-enabled) and the fixtures into a temp dir. The returned cleanup
// reaps this run's leftover processes, removes the dir and reports how many
// processes it had to kill. A devyard build failure is not fatal here: it is
// reported by every test that needs the binary, so harness self-tests still
// run while the backend doesn't compile.
func Setup() (cleanup func() int, err error) {
	artMu.Lock()
	defer artMu.Unlock()
	if art != nil {
		return func() int { return 0 }, nil
	}
	root, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 4)
	_, _ = rand.Read(nonce)
	a := &artifacts{
		repoRoot:  root,
		runPrefix: fmt.Sprintf("%s%d-%s", sandboxIDPrefix, os.Getpid(), hex.EncodeToString(nonce)),
	}
	a.binDir, err = os.MkdirTemp("", "devyard-e2e-bin-")
	if err != nil {
		return nil, err
	}

	if stale, err := ReapStale(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e harness: stale sweep: %v\n", err)
	} else if len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "e2e harness: reaped %d processes left by earlier runs\n", len(stale))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	var fixErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		a.devyard, a.buildErr = buildDevyard(ctx, a)
	}()
	go func() {
		defer wg.Done()
		fixErr = goBuild(ctx, root, a.binDir+string(filepath.Separator), false, "./test/e2e/fixtures/...")
	}()
	wg.Wait()
	if fixErr != nil {
		_ = os.RemoveAll(a.binDir)
		return nil, fmt.Errorf("build fixtures: %w", fixErr)
	}
	if err := writeStubs(a.binDir); err != nil {
		_ = os.RemoveAll(a.binDir)
		return nil, err
	}
	art = a
	return func() int {
		leaked, err := ReapPrefix(a.runPrefix + "-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e harness: final sweep: %v\n", err)
		}
		for _, p := range leaked {
			fmt.Fprintf(os.Stderr, "e2e harness: killed leftover process %s\n", p)
		}
		if os.Getenv("DEVYARD_E2E_KEEP") == "" {
			_ = os.RemoveAll(a.binDir)
		}
		return len(leaked)
	}, nil
}

func buildDevyard(ctx context.Context, a *artifacts) (string, error) {
	if bin := os.Getenv("DEVYARD_E2E_BIN"); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return "", err
		}
		// Link it into binDir so PATH lookups of "devyard" find the binary
		// under test, never an installed one.
		dst := filepath.Join(a.binDir, "devyard")
		if err := os.Symlink(abs, dst); err != nil {
			return "", err
		}
		return dst, nil
	}
	out := filepath.Join(a.binDir, "devyard")
	if err := goBuild(ctx, a.repoRoot, out, RaceEnabled, "./cmd/devyard"); err != nil {
		return "", err
	}
	return out, nil
}

func goBuild(ctx context.Context, dir, out string, race bool, pkgs ...string) error {
	args := []string{"build", "-o", out}
	if race {
		args = append(args, "-race")
	}
	args = append(args, pkgs...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, buf.String())
	}
	return nil
}

// writeStubs shadows commands that would escape the sandbox (opening a
// browser) with no-ops that just record the call.
func writeStubs(dir string) error {
	for _, name := range []string{"open", "xdg-open"} {
		script := "#!/bin/sh\necho \"$0 $*\" >> \"${TMPDIR:-/tmp}/devyard-e2e-opened.log\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.Contains(data, []byte("module github.com/blesswinsamuel/devyard\n")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("harness: cannot find the devyard repo root (go.mod) above the working directory")
		}
		dir = parent
	}
}

// RepoRoot returns the repository root found by Setup.
func RepoRoot() string {
	if a := current(); a != nil {
		return a.repoRoot
	}
	return ""
}

// FixturePath returns the absolute path of a built fixture program.
func FixturePath(name string) string {
	a := current()
	if a == nil {
		panic("harness: Setup/Main not called")
	}
	return filepath.Join(a.binDir, name)
}

// DevyardPath returns the devyard binary under test, or the build error.
func DevyardPath() (string, error) {
	a := current()
	if a == nil {
		return "", errors.New("harness: Setup/Main not called")
	}
	return a.devyard, a.buildErr
}

// WebDistBuilt reports whether the embedded SPA was built before the
// binary (internal/web/dist/index.html exists).
func WebDistBuilt() bool {
	_, err := os.Stat(filepath.Join(RepoRoot(), "internal", "web", "dist", "index.html"))
	return err == nil
}
