package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"connectrpc.com/connect"

	"github.com/blesswinsamuel/devyard/internal/client"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// groupArg reports the group named by args, when the only argument is
// "@name": a set of projects acted on together (see `groups` in the global
// config).
func groupArg(args []string) (string, bool) {
	if len(args) == 1 && strings.HasPrefix(args[0], "@") && len(args[0]) > 1 {
		return args[0][1:], true
	}
	return "", false
}

// groupMembers resolves a group to the ids of its projects.
func groupMembers(ctx context.Context, cl *client.Client, name string) ([]string, error) {
	resp, err := cl.GetGlobalConfig(ctx, connect.NewRequest(&pb.GetGlobalConfigRequest{}))
	if err != nil {
		return nil, err
	}
	var known []string
	for _, g := range resp.Msg.Config.GetGroups() {
		if g.Name == name {
			if len(g.Members) == 0 {
				return nil, fmt.Errorf("group %q has no projects", name)
			}
			return g.Members, nil
		}
		known = append(known, g.Name)
	}
	sort.Strings(known)
	if len(known) == 0 {
		return nil, fmt.Errorf("unknown group %q (no groups are defined: add a `groups:` map to the global config)", name)
	}
	return nil, fmt.Errorf("unknown group %q (groups: %s)", name, strings.Join(known, ", "))
}

// forEachInGroup runs fn for every project of the group, in order, and
// reports each failure instead of stopping at the first.
func (c *Context) forEachInGroup(ctx context.Context, cl *client.Client, group, verb string, fn func(id string) error) error {
	members, err := groupMembers(ctx, cl, group)
	if err != nil {
		return err
	}
	var failed []string
	for _, id := range members {
		if err := fn(id); err != nil {
			c.Errorf("devyard: project %q: %v\n", id, err)
			failed = append(failed, id)
			continue
		}
		c.Errorf("devyard: project %q %s\n", id, verb)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s failed for %s", verb, strings.Join(failed, ", "))
	}
	return nil
}
