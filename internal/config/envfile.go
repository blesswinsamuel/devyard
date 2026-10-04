package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
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
	return loadDotEnv(path, nil)
}

// loadDotEnv is LoadDotEnv recording what it read in sums.
func loadDotEnv(path string, sums Sums) (map[string]string, error) {
	data, err := sums.read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read env file: %w", err)
	}
	vars, err := ParseDotEnv(data)
	if err != nil {
		return nil, fmt.Errorf("parse env file %q: %w", path, err)
	}
	return vars, nil
}

// loadEnvFiles loads env files (relative to base) in order; later files
// win. Missing files are skipped. It returns the merged variables and the
// paths that exist.
func loadEnvFiles(base string, files []string, sums Sums) (map[string]string, []string, error) {
	vars := map[string]string{}
	var found []string
	for _, f := range files {
		path := resolvePath(base, f)
		m, err := loadDotEnv(path, sums)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		for k, v := range m {
			vars[k] = v
		}
		found = append(found, path)
	}
	return vars, found, nil
}

// Interpolate expands environment variable references in text. Supported
// forms:
//
//	$$           -> literal $
//	${VAR}       -> value of VAR (empty when unset)
//	${VAR:-def}  -> def when VAR is unset or empty
//	${VAR-def}   -> def when VAR is unset
//
// Bare $VAR references (e.g. inside shell commands) and service references
// (${api.port}, see expandRefs) are left untouched. An
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
			if refPattern.MatchString(text[i+2 : end]) {
				b.WriteString(text[i : end+1])
				i = end + 1
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

// refPattern matches the body of a service reference: ${api.port},
// ${api.url}, ${api.ports.metrics}. Env var names cannot contain dots, so
// the two never overlap.
var refPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)\.(port|url|ports\.[a-z0-9][a-z0-9-]*)$`)

// expandRefs replaces service references in s with lookup's result.
func expandRefs(s string, lookup func(service, field string) (string, error)) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		end += i
		m := refPattern.FindStringSubmatch(s[i+2 : end])
		if m == nil {
			b.WriteString(s[:end+1])
		} else {
			v, err := lookup(m[1], m[2])
			if err != nil {
				return "", err
			}
			b.WriteString(s[:i])
			b.WriteString(v)
		}
		s = s[end+1:]
	}
}
