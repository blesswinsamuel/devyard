//go:build unix

package health

import (
	"errors"
	"os/exec"
	"syscall"
)

// applyProcessGroup makes the probe its own process-group leader so a stuck
// CMD-SHELL pipeline can be killed wholesale via killProcessGroup.
func applyProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("command not initialized")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// killProcessGroup sends sig to every process in the probe's group. With
// Setpgid set, the probe's PGID equals its PID. A negative pid targets the
// whole group.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
