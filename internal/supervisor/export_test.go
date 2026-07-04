package supervisor

import (
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
)

// Internal test helpers that re-export unexported package functions to the
// external supervisor_test package. Lives in package supervisor (not _test).
func ShouldRestartForTest(p config.RestartPolicy, exitCode int) bool {
	return shouldRestart(p, exitCode)
}

func MergeEnvForTest(parent []string, svc map[string]string) []string {
	return mergeEnv(parent, svc)
}

func ResolveWorkingDirForTest(base, workingDir string) string {
	return resolveWorkingDir(base, workingDir)
}

func ExitCodeFromForTest(err error) int {
	return exitCodeFrom(err)
}

// BackoffDelayForTest exposes BackoffConfig.delay.
func BackoffDelayForTest(b BackoffConfig, attempt int) time.Duration {
	return b.delay(attempt)
}
