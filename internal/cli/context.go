package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/blesswinsamuel/devyard/internal/control"
)

// CLIContext encapsulates standard I/O streams, flags, and dependency clients
// for testability and clean architecture.
type CLIContext struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	ConfigPath string
	Project    string
	EnvFile    string
	Format     string // "table" (default), "json"
}

// NewDefaultCLIContext creates a CLIContext with default OS streams.
func NewDefaultCLIContext() *CLIContext {
	return &CLIContext{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		Format: "table",
	}
}

// IsJSON reports whether the current output format is JSON.
func (c *CLIContext) IsJSON() bool {
	return c.Format == "json"
}

// PrintJSON writes the provided value as indented JSON to c.Out.
func (c *CLIContext) PrintJSON(v any) error {
	enc := json.NewEncoder(c.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NewTabWriter returns a new tabwriter writing to c.Out.
func (c *CLIContext) NewTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(c.Out, 0, 0, 2, ' ', 0)
}

// Printf formats according to a format specifier and writes to c.Out.
func (c *CLIContext) Printf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.Out, format, a...)
}

// Println formats using the default formats for its operands and writes to c.Out.
func (c *CLIContext) Println(a ...any) {
	_, _ = fmt.Fprintln(c.Out, a...)
}

// Errorf formats according to a format specifier and writes to c.Err.
func (c *CLIContext) Errorf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.Err, format, a...)
}

// Errorln formats using the default formats for its operands and writes to c.Err.
func (c *CLIContext) Errorln(a ...any) {
	_, _ = fmt.Fprintln(c.Err, a...)
}

// LoadConfig loads and resolves the project config using context flags.
func (c *CLIContext) LoadConfig() (*loadedConfig, error) {
	return loadConfigWith(c.ConfigPath, c.Project, c.EnvFile)
}

// ResolveProjectName resolves the active project name from config or project flag.
func (c *CLIContext) ResolveProjectName() (string, error) {
	return resolveProjectNameWith(c.ConfigPath, c.Project, c.EnvFile)
}

// DialDaemon dials the running global daemon's control socket.
func (c *CLIContext) DialDaemon() (*control.Client, error) {
	sock, err := dialDaemon()
	if err != nil {
		return nil, err
	}
	return control.Dial(sock)
}

// EnsureDaemon ensures the global daemon is running and returns a client connected to it.
func (c *CLIContext) EnsureDaemon() (*control.Client, error) {
	sock, err := ensureDaemonTo(c.Err)
	if err != nil {
		return nil, err
	}
	return control.Dial(sock)
}
