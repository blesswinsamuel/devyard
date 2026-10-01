package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	file := lc.Project.File
	order := names
	if len(order) == 0 {
		deps := map[string][]string{}
		for name, svc := range file.Services {
			deps[name] = svc.DependsOn
		}
		g, err := dag.New(deps)
		if err != nil {
			return err
		}
		if order, err = g.Order(); err != nil {
			return err
		}
	}
	// Reuse the daemon's auto-port assignments so builds see the same
	// ports as the running services.
	assigned := config.PortAssignments{}
	if d, err := dirs(); err == nil {
		if pd, err := d.Project(lc.Project.ID); err == nil {
			if a, err := config.ReadPortAssignments(pd.Ports()); err == nil {
				assigned = a
			}
		}
	}
	res, err := lc.Project.Resolve(os.Environ(), assigned, nil)
	if err != nil {
		return err
	}
	built := 0
	for _, name := range order {
		svc, ok := res.Services[name]
		if !ok {
			return fmt.Errorf("unknown service %q", name)
		}
		b := svc.Build
		if b == nil {
			if len(names) > 0 {
				c.Errorf("devyard: %s has no build step\n", name)
			}
			continue
		}
		c.Errorf("%s │ %s\n", ui.ServicePrefix(name), ui.Dim("$ "+b.Cmd.String()))
		argv := b.Cmd.Args()
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = b.Dir
		cmd.Env = b.Env
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
