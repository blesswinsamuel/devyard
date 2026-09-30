package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/dag"
	"github.com/blesswinsamuel/devyard/internal/ui"
)

func newBuildCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "build [service...]",
		Short: "Run build steps in the foreground (all services with a build, in dependency order)",
		RunE: func(cmd *cobra.Command, args []string) error {
			lc, err := c.loadLocalConfig()
			if err != nil {
				return err
			}
			return c.runBuilds(lc, args)
		},
	}
}

// runBuilds runs build commands locally, in the current environment, each
// in its own process group. The first failure aborts.
func (c *Context) runBuilds(lc *localConfig, names []string) error {
	order := names
	if len(order) == 0 {
		deps := map[string][]string{}
		for name, svc := range lc.File.Services {
			deps[name] = svc.DependsOn.Order
		}
		g, err := dag.New(deps)
		if err != nil {
			return err
		}
		if order, err = g.Order(); err != nil {
			return err
		}
	}
	_, dotenv, err := config.ResolveDotEnv(lc.Path, c.EnvFile)
	if err != nil {
		return err
	}
	base := filepath.Dir(lc.Path)
	built := 0
	for _, name := range order {
		svc, ok := lc.File.Services[name]
		if !ok {
			return fmt.Errorf("unknown service %q", name)
		}
		if svc.Build == nil {
			if len(names) > 0 {
				c.Errorf("devyard: %s has no build step\n", name)
			}
			continue
		}
		b := svc.Build.Spec
		dir := b.WorkingDir
		if dir == "" {
			dir = base
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		c.Errorf("%s │ %s\n", ui.ServicePrefix(name), ui.Dim("$ "+b.Command))
		cmd := exec.Command(b.Shell, "-c", b.Command)
		cmd.Dir = dir
		cmd.Env = config.ChildEnv(os.Environ(), dotenv, b.Env)
		cmd.Stdout = c.Out
		cmd.Stderr = c.Err
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return fmt.Errorf("build of %s failed with exit code %d", name, exit.ExitCode())
			}
			return fmt.Errorf("build of %s: %w", name, err)
		}
		built++
	}
	if built == 0 && len(names) == 0 {
		c.Errorf("devyard: no service defines a build step\n")
	}
	return nil
}
