package globalconfig_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
)

func TestProjectsAndGroupsParse(t *testing.T) {
	cfg, warn := parse(t, "projects:\n  - ~/dev/a\n  - /tmp/b/devyard.yaml\ngroups:\n  work: [a, b]\n")
	if warn != "" {
		t.Fatalf("warnings: %s", warn)
	}
	if !cfg.ProjectsSet || !reflect.DeepEqual(cfg.Projects, []string{"~/dev/a", "/tmp/b/devyard.yaml"}) {
		t.Fatalf("projects: %v set=%v", cfg.Projects, cfg.ProjectsSet)
	}
	if !reflect.DeepEqual(cfg.Groups, map[string][]string{"work": {"a", "b"}}) {
		t.Fatalf("groups: %v", cfg.Groups)
	}
	if cfg, _ := parse(t, "web: {}\n"); cfg.ProjectsSet {
		t.Fatal("absent projects key reported as set")
	}
	if cfg, _ := parse(t, "projects: []\n"); !cfg.ProjectsSet || len(cfg.Projects) != 0 {
		t.Fatal("empty projects list must be set")
	}
}

func TestProjectsValidation(t *testing.T) {
	for name, content := range map[string]string{
		"duplicate":   "projects: [/tmp/x, /tmp/x/devyard.yml]\n",
		"bad group":   "groups:\n  'a b': [x]\n",
		"empty group": "groups:\n  g: ['']\n",
	} {
		if _, err := globalconfig.Parse([]byte(content), nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestEntryConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory without a config is a git-only project.
	got, err := globalconfig.EntryConfigPath("~/app")
	if err != nil || got != filepath.Join(dir, "devyard.yml") {
		t.Fatalf("empty dir: %q %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devyard.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := globalconfig.EntryConfigPath(dir); got != filepath.Join(dir, "devyard.yaml") {
		t.Fatalf("yaml fallback: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "devyard.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := globalconfig.EntryConfigPath(dir); got != filepath.Join(dir, "devyard.yml") {
		t.Fatalf("yml wins: %q", got)
	}
	other := filepath.Join(home, "x", "custom.yml")
	if got, _ := globalconfig.EntryConfigPath(other); got != other {
		t.Fatalf("explicit file: %q", got)
	}
	if _, err := globalconfig.EntryConfigPath("  "); err == nil {
		t.Fatal("blank entry accepted")
	}
	if e := globalconfig.EntryFor(filepath.Join(dir, "devyard.yml")); e != "~/app" {
		t.Fatalf("EntryFor dir: %q", e)
	}
	if e := globalconfig.EntryFor(other); e != "~/x/custom.yml" {
		t.Fatalf("EntryFor file: %q", e)
	}
}

const commented = `# my devyard config
web:
  host: 127.0.0.1 # loopback only
  port: 9090

# the projects
projects:
  - ~/dev/a # first
  # second one
  - ~/dev/b
  - ~/dev/c

groups:
  work: [a, b]
`

func TestProjectEditsPreserveComments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yml")
	if err := os.WriteFile(path, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := func(n string) string { return filepath.Join(home, "dev", n, "devyard.yml") }

	if changed, err := globalconfig.AddProject(path, cfgPath("d")); err != nil || !changed {
		t.Fatalf("add: %v %v", changed, err)
	}
	if changed, _ := globalconfig.AddProject(path, cfgPath("d")); changed {
		t.Fatal("adding twice changed the list")
	}
	if changed, err := globalconfig.RemoveProject(path, cfgPath("a")); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	if changed, _ := globalconfig.RemoveProject(path, cfgPath("zzz")); changed {
		t.Fatal("removing an absent project changed the list")
	}
	if _, err := globalconfig.MoveProject(path, cfgPath("d"), 0); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"# my devyard config", "# loopback only", "# the projects", "# second one"} {
		if !strings.Contains(text, want) {
			t.Errorf("comment %q lost:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# first") {
		t.Errorf("the comment of the removed entry should go with it:\n%s", text)
	}
	cfg, err := globalconfig.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"~/dev/d", "~/dev/b", "~/dev/c"}; !reflect.DeepEqual(cfg.Projects, want) {
		t.Fatalf("projects = %v, want %v", cfg.Projects, want)
	}
	if !reflect.DeepEqual(cfg.Groups["work"], []string{"a", "b"}) {
		t.Fatalf("groups lost: %v", cfg.Groups)
	}
	if _, err := globalconfig.MoveProject(path, cfgPath("nope"), 0); err == nil {
		t.Fatal("moving an unlisted project must fail")
	}
}

func TestMoveProjectClampsAndOrders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yml")
	p := func(n string) string { return filepath.Join(home, n, "devyard.yml") }
	for _, n := range []string{"a", "b", "c", "d"} {
		if _, err := globalconfig.AddProject(path, p(n)); err != nil {
			t.Fatal(err)
		}
	}
	order := func() string {
		cfg, err := globalconfig.Load(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(cfg.Projects, ",")
	}
	steps := []struct {
		name  string
		index int
		want  string
	}{{"a", 2, "~/b,~/c,~/a,~/d"}, {"d", 0, "~/d,~/b,~/c,~/a"}, {"d", 99, "~/b,~/c,~/a,~/d"}, {"c", -5, "~/c,~/b,~/a,~/d"}}
	for _, s := range steps {
		if _, err := globalconfig.MoveProject(path, p(s.name), s.index); err != nil {
			t.Fatal(err)
		}
		if got := order(); got != s.want {
			t.Fatalf("move %s to %d: got %s, want %s", s.name, s.index, got, s.want)
		}
	}
}

func TestAddProjectCreatesFileAndKeepsOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "sub", "config.yml")
	if _, err := globalconfig.AddProject(path, filepath.Join(home, "x", "devyard.yml")); err != nil {
		t.Fatal(err)
	}
	cfg, err := globalconfig.Load(path, nil)
	if err != nil || !cfg.ProjectsSet || len(cfg.Projects) != 1 {
		t.Fatalf("created config: %+v %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("future_key: 1\nprojects: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := globalconfig.AddProject(path, filepath.Join(home, "y", "devyard.yml")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "future_key: 1") {
		t.Fatalf("unknown key dropped:\n%s", data)
	}
}

func TestSaveKeepsProjectsAndComments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yml")
	if err := os.WriteFile(path, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := globalconfig.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Web.Port = 9191
	cfg.Web.AllowedHosts = []string{"x.example"}
	if err := globalconfig.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"# my devyard config", "# loopback only", "# the projects", "# second one", "port: 9191", "x.example", "~/dev/b"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q missing after Save:\n%s", want, text)
		}
	}
	out, err := globalconfig.Load(path, nil)
	if err != nil || out.Web.Port != 9191 || len(out.Projects) != 3 || len(out.Groups) != 1 {
		t.Fatalf("reload: %+v %v", out, err)
	}
	cfg.Web.AllowedHosts = nil
	if err := globalconfig.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "x.example") {
		t.Fatalf("cleared allowed_hosts survived:\n%s", data)
	}
}
