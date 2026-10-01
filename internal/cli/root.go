// Package cli implements the devyard command line: a thin client of the
// daemon's control API.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/devyard/internal/daemon"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// Execute runs the CLI. The hidden --daemon and --runner flags turn the
// process into the daemon or a process runner before cobra is involved.
func Execute() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case daemon.Flag:
			if err := daemon.Run(daemon.Options{Version: Version}); err != nil {
				return 1
			}
			return 0
		case runner.Flag:
			if err := runner.Main(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
	}
	c := NewContext()
	cmd := NewRootCommand(c)
	cmd.SetArgs(os.Args[1:])
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		var exit *ExitCodeError
		if errors.As(err, &exit) {
			return exit.Code
		}
		c.Errorf("Error: %s\n", errorText(err))
		return 1
	}
	return 0
}

// ExitCodeError makes the CLI exit with Code without printing an error.
type ExitCodeError struct{ Code int }

func (e *ExitCodeError) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

// NewRootCommand builds the command tree bound to c.
func NewRootCommand(c *Context) *cobra.Command {
	root := &cobra.Command{
		Use:           "devyard",
		Short:         "Orchestrate local processes with a compose-style config (no Docker)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetOut(c.Out)
	root.SetErr(c.Err)
	root.PersistentFlags().StringVar(&c.ConfigPath, "file", "", "Path to devyard.yml (default: search upwards from the current directory)")
	root.PersistentFlags().StringVarP(&c.Project, "project", "p", "", "Registered project id (instead of the config in the current directory)")
	root.PersistentFlags().StringVarP(&c.Format, "format", "o", "table", "Output format: table or json")

	daily := &cobra.Group{ID: "daily", Title: "Daily commands:"}
	resources := &cobra.Group{ID: "resources", Title: "Resources:"}
	root.AddGroup(daily, resources)

	for _, cmd := range []*cobra.Command{
		newStartCmd(c), newStopCmd(c), newRestartCmd(c), newReloadCmd(c), newStatusCmd(c),
		newLogsCmd(c), newTopCmd(c), newKillCmd(c), newRunCmd(c), newAttachCmd(c), newBuildCmd(c), newWebCmd(c),
	} {
		cmd.GroupID = daily.ID
		root.AddCommand(cmd)
	}
	for _, cmd := range []*cobra.Command{newProjectCmd(c), newServiceCmd(c), newTaskCmd(c), newDaemonCmd(c)} {
		cmd.GroupID = resources.ID
		root.AddCommand(cmd)
	}
	root.AddCommand(newVersionCmd(c))
	root.AddCommand(newSchemaCmd(c))
	return root
}
