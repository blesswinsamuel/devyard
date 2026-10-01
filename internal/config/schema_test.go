package config_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	jsonschema6 "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/blesswinsamuel/devyard/internal/config"
)

var update = flag.Bool("update", false, "regenerate devyard.schema.json")

// TestSchemaUpToDate checks the committed JSON Schema against the Go types.
// Regenerate with `go generate ./internal/config`.
func TestSchemaUpToDate(t *testing.T) {
	got, err := generateSchema()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("devyard.schema.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !bytes.Equal(got, config.SchemaJSON) {
		t.Fatal("devyard.schema.json is out of date; run `go generate ./internal/config`")
	}
}

// schemaExample exercises every field; it must load and validate.
const schemaExample = `
name: myapp
primary: web
env_files: [.env, .env.local]
env: {LOG_LEVEL: debug}
links:
  Docs: https://example.com/docs
services:
  db:
    run: postgres -D .data/pg
    port: 5432
    ready: {tcp: {}}
    stop: {signal: SIGINT, timeout: 30s}
  api:
    run: [cargo, run, --bin, api]
    dir: ./api
    port: auto
    depends_on: [db]
    restart: always
    env_files: [api/.env]
    env: {DATABASE_URL: "postgres://localhost:${db.port}/myapp"}
    ready:
      http: {path: /health, status: 200}
      interval: 1s
      timeout: 500ms
      retries: 10
      start_period: 5s
    build:
      run: cargo build --bin api
      dir: ./api
      env: {RUSTFLAGS: -g}
      sources: ["api/src/**", Cargo.lock]
  web:
    run: bun run dev --port $PORT
    ports: {http: auto, hmr: 24678}
    host: app
    tty: true
    depends_on: [api]
    env: {API_URL: "${api.url}"}
    build: bun install
    ready:
      exec: [curl, -fs, "http://127.0.0.1:${api.port}"]
  storybook:
    run: bun storybook --port $PORT
    port: auto
    autostart: false
    ready: {http: {port: 6006}}
tasks:
  migrate: sqlx migrate run
  lint: [bun, run, lint]
  seed:
    run: node scripts/seed.js
    dir: scripts
    env: {SEED: "1"}
    env_files: [.env.seed]
    depends_on: [db]
    tty: false
`

func compileSchema(t *testing.T) *jsonschema6.Schema {
	t.Helper()
	doc, err := jsonschema6.UnmarshalJSON(bytes.NewReader(config.SchemaJSON))
	if err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	c := jsonschema6.NewCompiler()
	if err := c.AddResource("devyard.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("devyard.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func validateYAML(sch *jsonschema6.Schema, content string) error {
	var v any
	if err := yaml.Unmarshal([]byte(content), &v); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	inst, err := jsonschema6.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

func TestSchemaMatchesLoader(t *testing.T) {
	t.Parallel()
	sch := compileSchema(t)
	if err := validateYAML(sch, schemaExample); err != nil {
		t.Errorf("schema rejects the example: %v", err)
	}
	p := mustLoad(t, schemaExample, nil)
	if len(p.Warnings) != 0 {
		t.Errorf("example has warnings: %v", p.Warnings)
	}
	repo, err := os.ReadFile("../../devyard.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateYAML(sch, string(repo)); err != nil {
		t.Errorf("schema rejects the repo's devyard.yml: %v", err)
	}
	for name, bad := range map[string]string{
		"unknown field":   "services:\n  api:\n    run: x\n    comand: y\n",
		"bad restart":     "services:\n  api:\n    run: x\n    restart: no\n",
		"bad port":        "services:\n  api:\n    run: x\n    port: any\n",
		"missing run":     "services:\n  api:\n    dir: x\n",
		"conditions":      "services:\n  db: x\n  api:\n    run: x\n    depends_on: {db: {condition: x}}\n",
		"bad duration":    "services:\n  api:\n    run: x\n    stop: {timeout: 10}\n",
		"unknown top key": "version: \"1\"\n",
	} {
		if err := validateYAML(sch, bad); err == nil {
			t.Errorf("%s: schema accepted %q", name, bad)
		}
	}
}

func generateSchema() ([]byte, error) {
	r := &jsonschema.Reflector{
		FieldNameTag:               "yaml",
		RequiredFromJSONSchemaTags: true,
		Anonymous:                  true,
		ExpandedStruct:             true,
		Mapper:                     schemaMapper,
	}
	if err := r.AddGoComments("github.com/blesswinsamuel/devyard/internal/config", "./"); err != nil {
		return nil, err
	}
	s := r.Reflect(&config.File{})
	s.Title = "devyard.yml"
	s.Description = "A devyard project: the services and tasks it runs. See docs/config-schema.md."
	s.Required = nil
	// Services, tasks and builds also accept a bare command.
	for _, name := range []string{"Service", "Task", "Build"} {
		if def, ok := s.Definitions[name]; ok {
			desc := def.Description
			def.Description = ""
			s.Definitions[name] = &jsonschema.Schema{Description: desc, OneOf: []*jsonschema.Schema{commandSchema(), def}}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func commandSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Description: "A string runs with `sh -c`; a list is executed directly.",
		OneOf: []*jsonschema.Schema{
			{Type: "string", MinLength: ptr(uint64(1))},
			{Type: "array", Items: &jsonschema.Schema{Type: "string"}, MinItems: ptr(uint64(1))},
		},
	}
}

func portSchema() *jsonschema.Schema {
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		{Type: "integer", Minimum: "1", Maximum: "65535"},
		{Const: "auto", Description: "devyard allocates a free port and keeps it across restarts."},
	}}
}

func schemaMapper(t reflect.Type) *jsonschema.Schema {
	switch t {
	case reflect.TypeOf(config.Command{}):
		return commandSchema()
	case reflect.TypeOf(config.PortValue{}):
		return portSchema()
	case reflect.TypeOf(config.PortMap{}):
		return &jsonschema.Schema{
			Type:                 "object",
			Description:          "Named ports; the first entry is the default port.",
			PropertyNames:        &jsonschema.Schema{Pattern: "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"},
			AdditionalProperties: portSchema(),
			MinProperties:        ptr(uint64(1)),
		}
	case reflect.TypeOf(config.PortRef("")):
		return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
			{Type: "integer", Minimum: "1", Maximum: "65535"},
			{Type: "string", Description: "A port name of the service."},
		}}
	case reflect.TypeOf(config.Links{}):
		return &jsonschema.Schema{
			Type:                 "object",
			Description:          "Named URLs shown with the project, in order.",
			AdditionalProperties: &jsonschema.Schema{Type: "string", Format: "uri"},
		}
	case reflect.TypeOf(config.RestartPolicy("")):
		return &jsonschema.Schema{Type: "string", Enum: []any{"never", "on-failure", "always"}, Default: "on-failure"}
	case reflect.TypeOf(time.Duration(0)):
		return &jsonschema.Schema{Type: "string", Pattern: `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`, Examples: []any{"500ms", "10s", "1m30s"}}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
