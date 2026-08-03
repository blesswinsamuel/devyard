package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseDotEnv parses a docker-compose-style env file (KEY=VALUE lines).
// Blank lines and lines starting with # are ignored. Lines may be prefixed
// with `export`. Values may be wrapped in single or double quotes.
func ParseDotEnv(data []byte) (map[string]string, error) {
	vars := make(map[string]string)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("env file line %d: expected KEY=VALUE", i+1)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("env file line %d: empty key", i+1)
		}
		value := strings.TrimSpace(line[eq+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		vars[key] = value
	}
	return vars, nil
}

// LoadDotEnv reads and parses an env file. It returns an error if the file
// cannot be read (including when it does not exist).
func LoadDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	vars, err := ParseDotEnv(data)
	if err != nil {
		return nil, fmt.Errorf("parse env file %q: %w", path, err)
	}
	return vars, nil
}

// ResolveDotEnv resolves the env file to use for a config file. An explicit
// envFile must exist and is returned as-is (absolute). When empty, it falls
// back to a .env file next to the config file, if one exists. The returned
// path is empty when no env file applies, and the returned map is nil.
func ResolveDotEnv(configPath, envFile string) (string, map[string]string, error) {
	if envFile != "" {
		abs, err := filepath.Abs(envFile)
		if err != nil {
			return "", nil, fmt.Errorf("resolve env file: %w", err)
		}
		vars, err := LoadDotEnv(abs)
		if err != nil {
			return "", nil, err
		}
		return abs, vars, nil
	}
	candidate := filepath.Join(filepath.Dir(configPath), ".env")
	if _, err := os.Stat(candidate); err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, err
	}
	vars, err := LoadDotEnv(candidate)
	if err != nil {
		return "", nil, err
	}
	return candidate, vars, nil
}

// Interpolate expands environment variable references in text. Supported
// forms:
//
//	$$           -> literal $
//	${VAR}       -> value of VAR (empty when unset)
//	${VAR:-def}  -> def when VAR is unset or empty
//	${VAR-def}   -> def when VAR is unset
//
// Bare $VAR references (e.g. inside shell commands) are left untouched. An
// unset variable without a default is replaced with an empty string and a
// warning is sent to warn (if non-nil).
func Interpolate(text string, env map[string]string, warn func(string)) string {
	if warn == nil {
		warn = func(string) {}
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] != '$' {
			b.WriteByte(text[i])
			i++
			continue
		}
		if i+1 >= len(text) {
			b.WriteByte('$')
			i++
			continue
		}
		switch text[i+1] {
		case '$':
			b.WriteByte('$')
			i += 2
		case '{':
			end := i + 2
			for end < len(text) && text[end] != '}' {
				end++
			}
			if end >= len(text) {
				// Unterminated: leave the reference literal.
				b.WriteByte('$')
				i++
				continue
			}
			value, set, name := interpolateExpr(text[i+2:end], env)
			if set {
				b.WriteString(value)
			} else {
				warn(fmt.Sprintf("the %q variable is not set, defaulting to a blank string", name))
			}
			i = end + 1
		default:
			// Bare $ (e.g. $VAR for the shell): leave untouched.
			b.WriteByte('$')
			i++
		}
	}
	return b.String()
}

// interpolateExpr evaluates one ${...} expression body. set is false only when
// the variable is unset and no default was supplied.
func interpolateExpr(expr string, env map[string]string) (value string, set bool, name string) {
	name = expr
	def := ""
	hasDef := false
	emptyDefaults := false // ":-" vs "-"
	if j := strings.Index(expr, ":-"); j >= 0 {
		name, def, hasDef, emptyDefaults = expr[:j], expr[j+2:], true, true
	} else if j := strings.Index(expr, "-"); j >= 0 {
		name, def, hasDef = expr[:j], expr[j+1:], true
	}

	v, ok := env[name]
	if ok && v != "" {
		return v, true, name
	}
	if ok && !emptyDefaults {
		return v, true, name
	}
	if hasDef {
		return def, true, name
	}
	return "", false, name
}
