package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/ui"
)

var buildCmd = &cobra.Command{
	Use:   "build [service...]",
	Short: "Run build commands for services that declare them",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}
		return runBuilds(cfg, args...)
	},
}

// runAllBuilds runs the build step for every service (in start order) that
// declares one. It's the entry point used by `up --build`.
func runAllBuilds(cfg *loadedConfig) error {
	return runBuilds(cfg, cfg.Order...)
}

// runBuilds runs the build step for the named services (or all, in start
// order, when names is empty). Services without a build spec are skipped. A
// failed build aborts the run and returns its error so `up --build` doesn't
// start services on top of a broken build.
func runBuilds(cfg *loadedConfig, names ...string) error {
	if len(names) == 0 {
		names = cfg.Order
	}
	for _, name := range names {
		svc, ok := cfg.File.Services[name]
		if !ok {
			return fmt.Errorf("build: unknown service %q", name)
		}
		if svc.Build == nil {
			continue
		}
		if err := runOneBuild(cfg, name, svc.Build.Spec); err != nil {
			return fmt.Errorf("build %q: %w", name, err)
		}
	}
	return nil
}

// runOneBuild executes a single service's build spec: the command (run via the
// configured shell), with the spec's env layered over the parent env and the
// working_dir resolved against the config base dir. stdout/stderr stream to
// the terminal with a colored-ish prefix so multi-service builds stay legible.
func runOneBuild(cfg *loadedConfig, name string, spec config.BuildSpec) error {
	shell := spec.Shell
	if shell == "" {
		shell = config.DefaultShell
	}
	dir := spec.WorkingDir
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.BaseDir, dir)
	}

	fmt.Fprintf(os.Stderr, "local-compose: building %q\n", name)
	stdout := prefixWriter(name, os.Stdout)
	_, _ = fmt.Fprintf(stdout, "$ %s\n", spec.Command)
	cmd := exec.Command(shell, "-c", spec.Command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = config.BuildEnvOver(config.BaseEnv(cfg.DotEnv), spec.Env)
	cmd.Stdout = stdout
	cmd.Stderr = prefixWriter(name, os.Stderr)
	return cmd.Run()
}

// prefixWriter wraps a writer so each line written through it gets a service
// prefix, mirroring the supervisor's foreground log prefixing for builds.
func prefixWriter(name string, w io.Writer) io.Writer {
	return &linePrefixer{w: w, prefix: ui.ServicePrefix(name) + " │ "}
}

// linePrefixer inserts prefix at the start of each line. It buffers a partial
// trailing line until the next newline so multi-write lines stay prefixed
// exactly once.
type linePrefixer struct {
	w       io.Writer
	prefix  string
	pending bool
}

func (p *linePrefixer) Write(buf []byte) (int, error) {
	total := 0
	for len(buf) > 0 {
		if !p.pending {
			if _, err := io.WriteString(p.w, p.prefix); err != nil {
				return total, err
			}
			p.pending = true
		}
		i := strings.IndexByte(string(buf), '\n')
		if i < 0 {
			n, err := p.w.Write(buf)
			total += n
			return total, err
		}
		cleaned := ui.CleanLogLine(string(buf[:i]))
		n, err := io.WriteString(p.w, cleaned+"\n")
		total += n
		if err != nil {
			return total, err
		}
		p.pending = false
		buf = buf[i+1:]
	}
	return total, nil
}
