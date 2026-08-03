package orchestrator

import (
	"os"
	"os/exec"

	"github.com/blesswinsamuel/local-compose/internal/config"
)

// runBuildCommand executes a build command via the given shell, with the
// spec's env overlaid on the dotenv-layered parent env and the working
// directory set to dir. Output goes to the daemon's stderr (the daemon log
// file).
func runBuildCommand(shell, command, dir string, baseEnv []string, svcEnv map[string]string) error {
	cmd := exec.Command(shell, "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = config.BuildEnvOver(baseEnv, svcEnv)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
