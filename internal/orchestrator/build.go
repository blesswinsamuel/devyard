package orchestrator

import (
	"os"
	"os/exec"
	"strings"
)

// runBuildCommand executes a build command via the given shell, with the
// spec's env overlaid on the parent env and the working directory set to dir.
// Output goes to the daemon's stderr (the daemon log file).
func runBuildCommand(shell, command, dir string, svcEnv map[string]string) error {
	cmd := exec.Command(shell, "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = buildEnv(svcEnv)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildEnv returns the parent environment with svcEnv overlaid (additive,
// matching the service env rule).
func buildEnv(svcEnv map[string]string) []string {
	parent := os.Environ()
	if len(svcEnv) == 0 {
		return parent
	}
	seen := make(map[string]bool, len(svcEnv))
	out := make([]string, 0, len(parent)+len(svcEnv))
	for _, kv := range parent {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if v, ok := svcEnv[k]; ok {
			out = append(out, k+"="+v)
			seen[k] = true
			continue
		}
		out = append(out, kv)
	}
	for k, v := range svcEnv {
		if !seen[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}
