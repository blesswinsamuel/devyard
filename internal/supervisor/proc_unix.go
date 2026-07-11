//go:build unix

package supervisor

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// command is a thin wrapper around exec.Cmd that keeps syscall usage confined
// to this file (so the rest of the package stays portable-ish and the
// macOS/Linux-only constraint is explicit via the build tag).
type command struct {
	cmd *exec.Cmd
	dir string
	env []string
}

func newCommand(name string, args ...string) *command {
	return &command{cmd: exec.Command(name, args...)}
}

// pipes wires stdout and stderr to pipes the supervisor will read.
func (c *command) pipes() (io.ReadCloser, io.ReadCloser, error) {
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, nil, err
	}
	return stdout, stderr, nil
}

// start applies the accumulated dir/env and starts the process.
func (c *command) start() error {
	if c.cmd == nil {
		return errors.New("command not initialized")
	}
	if c.dir != "" {
		c.cmd.Dir = c.dir
	}
	if c.env != nil {
		c.cmd.Env = c.env
	}
	return c.cmd.Start()
}

// startWithPTY allocates a pseudo-terminal and starts the process with
// stdin/stdout/stderr connected to the slave side. Returns the master fd
// which can be used to read output and write input.
func (c *command) startWithPTY() (*os.File, error) {
	if c.cmd == nil {
		return nil, errors.New("command not initialized")
	}
	if c.dir != "" {
		c.cmd.Dir = c.dir
	}
	if c.env != nil {
		c.cmd.Env = c.env
	}
	// Setpgid so the child gets its own process group for killGroup.
	// StartWithSize will add Setsid + Setctty for the PTY session.
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return pty.StartWithSize(c.cmd, nil)
}

func (c *command) processPID() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Wait waits for the process to exit and closes its pipes.
func (c *command) Wait() error {
	return c.cmd.Wait()
}

// applyProcessGroup makes the child its own session/process-group leader so
// the whole group (including any shell-spawned children) can be signalled
// together via killGroup. With Setpgid set, the child's PGID equals its PID.
func applyProcessGroup(c *command) error {
	if c.cmd == nil {
		return errors.New("command not initialized")
	}
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// killGroup sends sig to every process in pgid. A negative pid targets the
// whole process group.
func killGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	return syscall.Kill(-pgid, sig)
}

// resizePTY changes the PTY window size for a service's master fd.
func resizePTY(f *os.File, width, height int) error {
	return pty.Setsize(f, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
}
