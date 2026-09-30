package cli

import (
	"os/exec"
	"runtime"
	"syscall"
)

// Version is set at build time via -ldflags "-X .../internal/cli.Version=...".
var Version = "dev"

var isDarwin = runtime.GOOS == "darwin"

// runDetached starts a helper program (e.g. a browser opener) in its own
// process group without waiting for it.
func runDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
