package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/blesswinsamuel/devyard/internal/client"
	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/daemon"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/paths"
)

// Context carries streams and global flags.
type Context struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	ConfigPath string
	Project    string
	Format     string
}

// NewContext returns a Context bound to the process's standard streams.
func NewContext() *Context {
	return &Context{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Format: "table"}
}

// Errorf writes a status message to stderr.
func (c *Context) Errorf(format string, a ...any) { _, _ = fmt.Fprintf(c.Err, format, a...) }

// Printf writes to stdout.
func (c *Context) Printf(format string, a ...any) { _, _ = fmt.Fprintf(c.Out, format, a...) }

// JSON reports whether JSON output was requested.
func (c *Context) JSON() bool { return c.Format == "json" }

func (c *Context) table() *tabwriter.Writer { return tabwriter.NewWriter(c.Out, 0, 0, 2, ' ', 0) }

// printProtoJSON prints a list of messages as a JSON array.
func (c *Context) printProtoJSON(msgs []proto.Message) error {
	opts := protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: true}
	_, _ = fmt.Fprint(c.Out, "[")
	for i, m := range msgs {
		if i > 0 {
			_, _ = fmt.Fprint(c.Out, ",")
		}
		data, err := opts.Marshal(m)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(c.Out, "\n%s", data)
	}
	if len(msgs) > 0 {
		_, _ = fmt.Fprintln(c.Out)
	}
	_, _ = fmt.Fprintln(c.Out, "]")
	return nil
}

// errorText renders a CLI error; Connect errors show only the server
// message.
func errorText(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}

// --- daemon connection ---------------------------------------------------------

func dirs() (paths.Dirs, error) { return paths.Default() }

// dial connects to a running daemon.
func (c *Context) dial(ctx context.Context) (*client.Client, error) {
	d, err := dirs()
	if err != nil {
		return nil, err
	}
	cl := client.Dial(d.Socket())
	if _, err := cl.Ping(ctx, 2*time.Second); err != nil {
		cl.Close()
		return nil, errors.New("no daemon running (start one with `devyard start` or `devyard daemon start`)")
	}
	return cl, nil
}

// ensureDaemon connects to the daemon, starting it when none is running.
func (c *Context) ensureDaemon(ctx context.Context) (*client.Client, error) {
	d, err := dirs()
	if err != nil {
		return nil, err
	}
	cl := client.Dial(d.Socket())
	if _, err := cl.Ping(ctx, time.Second); err == nil {
		return cl, nil
	}
	pid := daemon.ReadPid(d)
	if pid == 0 || !daemon.Alive(pid) {
		if pid, err = daemon.Spawn(d, os.Environ()); err != nil {
			cl.Close()
			return nil, err
		}
		c.Errorf("devyard: daemon started (pid %d)\n", pid)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cl.Ping(ctx, 500*time.Millisecond); err == nil {
			return cl, nil
		}
		if !daemon.Alive(pid) && daemon.ReadPid(d) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cl.Close()
	if msg := daemon.LastError(d); msg != "" {
		return nil, fmt.Errorf("daemon failed to start: %s", msg)
	}
	return nil, fmt.Errorf("timed out waiting for the daemon (see %s)", d.DaemonLog())
}

// --- project resolution --------------------------------------------------------

// localConfig is the config found from flags or the working directory.
type localConfig struct {
	Path    string
	Project *config.Project
}

// loadLocalConfig loads the config named by --file or found by walking up
// from the working directory.
func (c *Context) loadLocalConfig() (*localConfig, error) {
	path := c.ConfigPath
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if path, err = config.FindConfig(wd); err != nil {
			return nil, errors.New("no devyard.yml found in this directory or its parents (use --file or -p)")
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	proj, err := config.Load(abs, os.Environ())
	if err != nil {
		return nil, err
	}
	for _, w := range proj.Warnings {
		c.Errorf("devyard: warning: %s\n", w)
	}
	return &localConfig{Path: abs, Project: proj}, nil
}

// projectID resolves the project to act on: -p (which must be registered)
// or the local config's project.
func (c *Context) projectID(ctx context.Context, cl *client.Client) (string, error) {
	if c.Project != "" {
		if err := paths.ValidateID(c.Project); err != nil {
			return "", err
		}
		if cl != nil {
			if _, err := findProject(ctx, cl, c.Project); err != nil {
				return "", err
			}
		}
		return c.Project, nil
	}
	// A registered project is found by its config path, without parsing the
	// file: the config may be broken, which is when status and diff matter.
	if cl != nil {
		if path, ok := c.localConfigPath(); ok {
			if snap, err := state(ctx, cl); err == nil {
				for _, p := range snap.Projects {
					if p.ConfigPath == path {
						return p.Id, nil
					}
				}
			}
		}
	}
	lc, err := c.loadLocalConfig()
	if err != nil {
		return "", err
	}
	return lc.Project.ID, nil
}

// localConfigPath is the absolute path of the config named by --file or found
// from the working directory.
func (c *Context) localConfigPath() (string, bool) {
	path := c.ConfigPath
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		if path, err = config.FindConfig(wd); err != nil {
			return "", false
		}
	}
	abs, err := filepath.Abs(path)
	return abs, err == nil
}

func state(ctx context.Context, cl *client.Client) (*pb.Snapshot, error) {
	resp, err := cl.GetState(ctx, connect.NewRequest(&pb.GetStateRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Snapshot, nil
}

func findProject(ctx context.Context, cl *client.Client, id string) (*pb.Project, error) {
	snap, err := state(ctx, cl)
	if err != nil {
		return nil, err
	}
	for _, p := range snap.Projects {
		if p.Id == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("project %q is not registered (see `devyard project list`)", id)
}

// captureEnv is the launch environment sent to the daemon.
func captureEnv() []string { return os.Environ() }
