package config

import _ "embed"

//go:generate go test -run TestSchemaUpToDate -update .

// SchemaJSON is the JSON Schema of devyard.yml, generated from the Go types.
//
//go:embed devyard.schema.json
var SchemaJSON []byte
