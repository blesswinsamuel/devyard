package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

func TestRootCommandFlagBinding(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cliCtx := &CLIContext{
		In:     strings.NewReader(""),
		Out:    buf,
		Err:    errBuf,
		Format: "table",
	}

	rootCmd := NewRootCommand(cliCtx)
	rootCmd.SetArgs([]string{"-f", "/path/to/local-compose.yml", "-p", "my-project", "--env-file", ".env.test", "-o", "json"})
	_ = rootCmd.ParseFlags([]string{"-f", "/path/to/local-compose.yml", "-p", "my-project", "--env-file", ".env.test", "-o", "json"})

	if cliCtx.ConfigPath != "/path/to/local-compose.yml" {
		t.Errorf("ConfigPath = %q, want /path/to/local-compose.yml", cliCtx.ConfigPath)
	}
	if cliCtx.Project != "my-project" {
		t.Errorf("Project = %q, want my-project", cliCtx.Project)
	}
	if cliCtx.EnvFile != ".env.test" {
		t.Errorf("EnvFile = %q, want .env.test", cliCtx.EnvFile)
	}
	if cliCtx.Format != "json" {
		t.Errorf("Format = %q, want json", cliCtx.Format)
	}
	if !cliCtx.IsJSON() {
		t.Error("expected IsJSON() to be true")
	}
}

func TestCommandTreeStructure(t *testing.T) {
	t.Parallel()

	cliCtx := NewDefaultCLIContext()
	rootCmd := NewRootCommand(cliCtx)

	expectedCommands := []string{
		// Resource commands
		"project", "service", "task", "daemon", "ui", "version",
		// Daily shortcuts
		"start", "stop", "restart", "reload", "status", "logs", "top", "kill", "run", "build",
		// Transition aliases
		"up", "down", "ps", "ls",
	}

	for _, name := range expectedCommands {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil || cmd == nil {
			t.Errorf("expected command %q to be registered in root command tree", name)
		}
	}
}

func TestSubcommandAliases(t *testing.T) {
	t.Parallel()

	cliCtx := NewDefaultCLIContext()
	rootCmd := NewRootCommand(cliCtx)

	checks := []struct {
		input    []string
		wantName string
	}{
		{[]string{"proj", "list"}, "list"},
		{[]string{"p", "ls"}, "list"},
		{[]string{"svc", "list"}, "list"},
		{[]string{"s", "ps"}, "list"},
		{[]string{"t", "list"}, "list"},
		{[]string{"t", "ls"}, "list"},
		{[]string{"d", "status"}, "status"},
		{[]string{"web"}, "ui"},
		{[]string{"ps"}, "status"},
		{[]string{"ls"}, "ls"},
	}

	for _, c := range checks {
		cmd, _, err := rootCmd.Find(c.input)
		if err != nil || cmd == nil {
			t.Errorf("Find(%v): command not found, err: %v", c.input, err)
			continue
		}
		if cmd.Name() != c.wantName {
			t.Errorf("Find(%v): got name %q, want %q", c.input, cmd.Name(), c.wantName)
		}
	}
}

func TestRenderStatesHelperTableAndJSON(t *testing.T) {
	t.Parallel()

	states := []*protocol.ServiceState{
		{
			Name:     "api",
			Status:   "running",
			Pid:      1234,
			Restarts: 1,
			Health:   "healthy",
		},
	}

	// 1. Table output
	bufTable := &bytes.Buffer{}
	ctxTable := &CLIContext{Out: bufTable, Format: "table"}
	if err := renderStatesHelper(ctxTable, states, false); err != nil {
		t.Fatalf("renderStatesHelper table: %v", err)
	}
	outTable := bufTable.String()
	if !strings.Contains(outTable, "NAME") || !strings.Contains(outTable, "api") || !strings.Contains(outTable, "1234") {
		t.Errorf("unexpected table output: %s", outTable)
	}

	// 2. JSON output
	bufJSON := &bytes.Buffer{}
	ctxJSON := &CLIContext{Out: bufJSON, Format: "json"}
	if err := renderStatesHelper(ctxJSON, states, false); err != nil {
		t.Fatalf("renderStatesHelper json: %v", err)
	}
	var decoded []*protocol.ServiceState
	if err := json.Unmarshal(bufJSON.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Name != "api" || decoded[0].Pid != 1234 {
		t.Errorf("unexpected decoded json: %+v", decoded)
	}
}

func TestRenderTasksHelperTableAndJSON(t *testing.T) {
	t.Parallel()

	tasks := []*protocol.ActionState{
		{
			Name:    "migrate",
			Status:  "idle",
			Command: "go run ./cmd/migrate",
		},
	}

	// Table output
	bufTable := &bytes.Buffer{}
	ctxTable := &CLIContext{Out: bufTable, Format: "table"}
	if err := renderTasksHelper(ctxTable, tasks); err != nil {
		t.Fatalf("renderTasksHelper table: %v", err)
	}
	if !strings.Contains(bufTable.String(), "TASK") || !strings.Contains(bufTable.String(), "migrate") {
		t.Errorf("unexpected table output: %s", bufTable.String())
	}

	// JSON output
	bufJSON := &bytes.Buffer{}
	ctxJSON := &CLIContext{Out: bufJSON, Format: "json"}
	if err := renderTasksHelper(ctxJSON, tasks); err != nil {
		t.Fatalf("renderTasksHelper json: %v", err)
	}
	var decoded []*protocol.ActionState
	if err := json.Unmarshal(bufJSON.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Name != "migrate" {
		t.Errorf("unexpected decoded json: %+v", decoded)
	}
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	ctx := &CLIContext{Out: buf, Format: "table"}

	cmd := newVersionCmd(ctx)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version execute: %v", err)
	}
	if !strings.Contains(buf.String(), "local-compose version") {
		t.Errorf("unexpected version output: %s", buf.String())
	}

	bufJSON := &bytes.Buffer{}
	ctxJSON := &CLIContext{Out: bufJSON, Format: "json"}
	cmdJSON := newVersionCmd(ctxJSON)
	if err := cmdJSON.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version json execute: %v", err)
	}
	var res map[string]string
	if err := json.Unmarshal(bufJSON.Bytes(), &res); err != nil {
		t.Fatalf("version json decode: %v", err)
	}
	if res["version"] != Version {
		t.Errorf("got version %q, want %q", res["version"], Version)
	}
}
