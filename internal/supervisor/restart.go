package supervisor

import (
	"math/big"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
)

// BackoffConfig tunes restart backoff.
type BackoffConfig struct {
	// Base is the initial delay between restart attempts.
	Base time.Duration
	// Factor multiplies the delay after each attempt.
	Factor float64
	// Cap is the maximum delay between attempts.
	Cap time.Duration
	// MaxAttempts caps the number of restart attempts for on-failure
	// services. always/unless-stopped are unlimited.
	MaxAttempts int
	// Jitter is the +/- fraction of random jitter applied to each delay
	// (0.2 = +/-20%).
	Jitter float64
}

// DefaultBackoff returns the production backoff defaults.
func DefaultBackoff() BackoffConfig {
	return BackoffConfig{
		Base:        1 * time.Second,
		Factor:      2,
		Cap:         30 * time.Second,
		MaxAttempts: 10,
		Jitter:      0.2,
	}
}

// delay returns the backoff delay for the nth restart attempt (1-based),
// applying exponential growth, cap, and jitter.
func (b BackoffConfig) delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := float64(b.Base)
	for i := 1; i < attempt; i++ {
		d *= b.Factor
		if b.Cap > 0 && d > float64(b.Cap) {
			d = float64(b.Cap)
			break
		}
	}
	if b.Jitter > 0 {
		// jitter in [-Jitter, +Jitter] * d
		j := (rand.Float64()*2 - 1) * b.Jitter
		d *= (1 + j)
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// shouldRestart reports whether policy permits a restart after an exit with
// the given code. The unless-stopped "stopped" flag is checked separately by
// the caller.
func shouldRestart(policy config.RestartPolicy, exitCode int) bool {
	switch policy {
	case config.RestartAlways:
		return true
	case config.RestartOnFailure:
		return exitCode != 0
	case config.RestartUnlessStopped:
		return true
	default: // RestartNo
		return false
	}
}

// defaultColorEnv returns POSIX-standard environment variables that tell
// child processes to emit colors even when stdout is not a terminal. These
// are injected with lowest priority (parent env and service env win).
func defaultColorEnv() []string {
	return []string{
		"CLICOLOR=1",
		"CLICOLOR_FORCE=1",
		"TERM=xterm-256color",
	}
}

// applyEnvDefaults appends defaults to parent for any keys not already present.
func applyEnvDefaults(parent, defaults []string) []string {
	existing := make(map[string]bool, len(parent))
	for _, kv := range parent {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			existing[kv[:i]] = true
		}
	}
	out := make([]string, 0, len(parent)+len(defaults))
	out = append(out, parent...)
	for _, kv := range defaults {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if !existing[k] {
			out = append(out, kv)
		}
	}
	return out
}

// mergeEnv returns parent env with svc env overlaid (additive, not replacing
// the whole set — same rule as docker-compose).
func mergeEnv(parent []string, svc map[string]string) []string {
	if len(svc) == 0 {
		return parent
	}
	seen := make(map[string]bool, len(parent)+len(svc))
	out := make([]string, 0, len(parent)+len(svc))
	for _, kv := range parent {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if v, ok := svc[k]; ok {
			out = append(out, k+"="+v)
			seen[k] = true
			continue
		}
		out = append(out, kv)
	}
	for k, v := range svc {
		if !seen[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// resolveWorkingDir resolves a service working_dir against the config base
// dir when relative. Empty working_dir defaults to baseDir (the config
// file's directory).
func resolveWorkingDir(baseDir, workingDir string) string {
	if workingDir == "" {
		return baseDir
	}
	if filepath.IsAbs(workingDir) {
		return workingDir
	}
	return filepath.Join(baseDir, workingDir)
}

// exitCodeFrom extracts the exit code from an exec error. Returns 0 on nil,
// -1 if the process was killed by a signal (no code available), and the code
// otherwise.
func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}
	type exitErr interface{ ExitCode() int }
	if e, ok := err.(exitErr); ok {
		return e.ExitCode()
	}
	// Fall back: parse "exit status N" from a generic *exec.errorString.
	if strings.Contains(err.Error(), "exit status") {
		n := new(big.Int)
		for _, part := range strings.Fields(err.Error()) {
			if _, ok := n.SetString(part, 10); ok {
				return int(n.Int64())
			}
		}
	}
	return -1
}

// stoppedMarkerPath returns the path of the unless-stopped marker file.
func (s *Supervisor) stoppedMarkerPath(name string) string {
	return filepath.Join(s.opts.Locations.State, name+".stopped")
}

func (s *Supervisor) hasStoppedMarker(name string) bool {
	_, err := os.Stat(s.stoppedMarkerPath(name))
	return err == nil
}

func (s *Supervisor) writeStoppedMarker(name string) error {
	return os.WriteFile(s.stoppedMarkerPath(name), []byte("stopped\n"), 0o644)
}

func (s *Supervisor) removeStoppedMarker(name string) error {
	err := os.Remove(s.stoppedMarkerPath(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
