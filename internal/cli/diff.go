package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/ui"
)

func newDiffCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Show how the config files differ from what the project runs",
		Long: "Shows what `devyard reload` would change: the services and tasks added, removed or\n" +
			"changed (and which of them restart), and a diff of the YAML files as written. Changes\n" +
			"to env files are listed by variable name only; their values are never shown.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, err := c.dial(ctx)
			if err != nil {
				return err
			}
			defer cl.Close()
			id, err := c.projectID(ctx, cl)
			if err != nil {
				return err
			}
			p, err := findProject(ctx, cl, id)
			if err != nil {
				return err
			}
			if c.JSON() {
				return c.printProtoJSON([]proto.Message{p.GetDrift()})
			}
			return c.renderDrift(p)
		},
	}
}

// renderDrift prints the drift of a project.
func (c *Context) renderDrift(p *pb.Project) error {
	d := p.GetDrift()
	switch d.GetState() {
	case "":
		c.Printf("%s: the config files match what is running.\n", p.Id)
		return nil
	case "invalid":
		c.Printf("%s: the config files changed but do not load; what runs is unchanged.\n  %s\n", p.Id, strings.ReplaceAll(d.Error, "\n", "\n  "))
		return nil
	}
	c.Printf("%s: %d pending %s (reload policy: %s)\n\n", p.Id, len(d.Changes), plural(len(d.Changes), "change", "changes"), p.ReloadPolicy)
	for _, ch := range d.Changes {
		c.Printf("  %s %s %s", changeMark(ch.Op), ch.Kind, ch.Name)
		if ch.Restart {
			c.Printf("  %s", ui.Dim("(restarts)"))
		}
		c.Printf("\n")
		for _, detail := range ch.Details {
			c.Printf("      %s\n", detail)
		}
	}
	if d.Diff != "" {
		c.Printf("\n%s", d.Diff)
	}
	c.Printf("\nApply with `devyard reload`.\n")
	return nil
}

func changeMark(op string) string {
	switch op {
	case "added":
		return "+"
	case "removed":
		return "-"
	}
	return "~"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// driftNote is the one-line summary `status` prints for a project whose
// config files changed, or "".
func driftNote(p *pb.Project) string {
	d := p.GetDrift()
	switch d.GetState() {
	case "pending":
		return fmt.Sprintf("devyard: project %q: the config changed (%d %s); `devyard diff` shows it, `devyard reload` applies it\n", p.Id, len(d.Changes), plural(len(d.Changes), "change", "changes"))
	case "invalid":
		return fmt.Sprintf("devyard: project %q: the config changed but does not load (%s); what runs is unchanged\n", p.Id, firstLine(d.Error))
	}
	return ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
