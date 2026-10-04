package globalconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigName is the config file a project directory is expected to
// hold.
const DefaultConfigName = "devyard.yml"

// configNames are the file names looked for, in order, in a project
// directory entry.
var configNames = []string{"devyard.yml", "devyard.yaml"}

// ExpandHome replaces a leading "~" with the home directory. A trailing
// slash is kept: it distinguishes a directory from a name being typed.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			out := filepath.Join(home, strings.TrimPrefix(p, "~"))
			if strings.HasSuffix(p, "/") && !strings.HasSuffix(out, "/") {
				out += "/"
			}
			return out
		}
	}
	return p
}

// AbbreviateHome is the inverse of ExpandHome, for display and for entries
// written to the config.
func AbbreviateHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}

// EntryConfigPath resolves a `projects` entry to the project's config file
// path (which need not exist: a directory without a config is a git-only
// project). An entry is a directory, containing devyard.yml or devyard.yaml,
// or the path of a config file.
func EntryConfigPath(entry string) (string, error) {
	p := ExpandHome(strings.TrimSpace(entry))
	if p == "" {
		return "", errors.New("empty project path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("globalconfig: %w", err)
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return abs, nil
	}
	if ext := filepath.Ext(abs); ext == ".yml" || ext == ".yaml" {
		return abs, nil
	}
	for _, name := range configNames {
		if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
			return filepath.Join(abs, name), nil
		}
	}
	return filepath.Join(abs, DefaultConfigName), nil
}

// EntryFor is the entry written to the config for a project's config file:
// its directory when the file has the default name, the file path otherwise.
func EntryFor(configPath string) string {
	if filepath.Base(configPath) == DefaultConfigName {
		return AbbreviateHome(filepath.Dir(configPath))
	}
	return AbbreviateHome(configPath)
}

// ProjectPaths returns the config file path of every listed project, in
// order. Entries that cannot be resolved are skipped.
func (c *Config) ProjectPaths() []string {
	out := make([]string, 0, len(c.Projects))
	for _, e := range c.Projects {
		if p, err := EntryConfigPath(e); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// validateProjects checks the projects and groups sections.
func (c *Config) validateProjects() error {
	seen := map[string]string{}
	for _, e := range c.Projects {
		p, err := EntryConfigPath(e)
		if err != nil {
			return fmt.Errorf("globalconfig: projects: %w", err)
		}
		if prev, dup := seen[p]; dup {
			return fmt.Errorf("globalconfig: projects: %q and %q are the same project", prev, e)
		}
		seen[p] = e
	}
	for name, members := range c.Groups {
		if name == "" || strings.ContainsAny(name, " \t@") {
			return fmt.Errorf("globalconfig: groups: invalid group name %q", name)
		}
		for _, m := range members {
			if m == "" {
				return fmt.Errorf("globalconfig: groups.%s: empty project id", name)
			}
		}
	}
	return nil
}

// edit applies fn to the root mapping of the config file, preserving
// comments and unrelated content, and writes the result atomically. A
// missing file is created.
func edit(path string, fn func(root *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("globalconfig: read: %w", err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("globalconfig: parse: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("globalconfig: the config file must be a map")
	}
	if err := fn(root); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("globalconfig: marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("globalconfig: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("globalconfig: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("globalconfig: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("globalconfig: write: %w", err)
	}
	return nil
}

// mapValue returns the value node of key in a mapping, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// projectsSeq returns the `projects` sequence, creating it when create is
// set.
func projectsSeq(root *yaml.Node, create bool) (*yaml.Node, error) {
	if v := mapValue(root, "projects"); v != nil {
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			v.Kind, v.Tag, v.Value = yaml.SequenceNode, "!!seq", ""
		}
		if v.Kind != yaml.SequenceNode {
			return nil, errors.New("globalconfig: projects must be a list")
		}
		return v, nil
	}
	if !create {
		return nil, nil
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "projects"}, seq)
	return seq, nil
}

// indexOfEntry finds the entry that resolves to configPath.
func indexOfEntry(seq *yaml.Node, configPath string) int {
	for i, n := range seq.Content {
		if p, err := EntryConfigPath(n.Value); err == nil && p == configPath {
			return i
		}
	}
	return -1
}

// AddProject appends the project with the given config file path to the
// `projects` list, creating the list (and the file) when needed. It reports
// whether the list changed.
func AddProject(path, configPath string) (bool, error) {
	changed := false
	err := edit(path, func(root *yaml.Node) error {
		seq, err := projectsSeq(root, true)
		if err != nil {
			return err
		}
		if indexOfEntry(seq, configPath) >= 0 {
			return nil
		}
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: EntryFor(configPath)})
		changed = true
		return nil
	})
	return changed, err
}

// RemoveProject drops the project from the `projects` list. It reports
// whether the list changed.
func RemoveProject(path, configPath string) (bool, error) {
	changed := false
	err := edit(path, func(root *yaml.Node) error {
		seq, err := projectsSeq(root, false)
		if err != nil || seq == nil {
			return err
		}
		i := indexOfEntry(seq, configPath)
		if i < 0 {
			return nil
		}
		seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
		changed = true
		return nil
	})
	return changed, err
}

// MoveProject moves the project to index in the `projects` list (clamped to
// the list). Comments stay with their entries. It reports whether the order
// changed.
func MoveProject(path, configPath string, index int) (bool, error) {
	changed := false
	err := edit(path, func(root *yaml.Node) error {
		seq, err := projectsSeq(root, false)
		if err != nil {
			return err
		}
		if seq == nil {
			return fmt.Errorf("globalconfig: project %s is not in the list", configPath)
		}
		from := indexOfEntry(seq, configPath)
		if from < 0 {
			return fmt.Errorf("globalconfig: project %s is not in the list", configPath)
		}
		index = max(0, min(index, len(seq.Content)-1))
		if from == index {
			return nil
		}
		n := seq.Content[from]
		rest := append(append([]*yaml.Node{}, seq.Content[:from]...), seq.Content[from+1:]...)
		seq.Content = append(append(append([]*yaml.Node{}, rest[:index]...), n), rest[index:]...)
		changed = true
		return nil
	})
	return changed, err
}
