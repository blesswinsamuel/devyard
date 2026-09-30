//go:build unix

package engine

import "syscall"

// killSession SIGKILLs a runner (a session and process-group leader). Its
// child lives in a separate group and is cleaned up when the lost run is
// resolved.
func killSession(runnerPID int) {
	if runnerPID > 0 {
		_ = syscall.Kill(-runnerPID, syscall.SIGKILL)
	}
}
