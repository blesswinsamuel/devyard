package paths

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func TestResolveXDG(t *testing.T) {
	d, err := Resolve(env(map[string]string{
		"XDG_STATE_HOME":  "/s",
		"XDG_RUNTIME_DIR": "/r",
		"XDG_CONFIG_HOME": "/c",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "/s/devyard" || d.Runtime != "/r/devyard" || d.Config != "/c/devyard" {
		t.Fatalf("unexpected dirs: %+v", d)
	}
	if d.Socket() != "/r/devyard/daemon.sock" {
		t.Fatalf("socket: %s", d.Socket())
	}
}

func TestResolveHomeFallback(t *testing.T) {
	d, err := Resolve(env(map[string]string{"HOME": "/h"}))
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "/h/.local/state/devyard" || d.Runtime != "/h/.local/state/devyard/run" || d.Config != "/h/.config/devyard" {
		t.Fatalf("unexpected dirs: %+v", d)
	}
}

func TestResolveNoHome(t *testing.T) {
	if _, err := Resolve(env(nil)); err == nil {
		t.Fatal("expected error without HOME")
	}
}

func TestValidateID(t *testing.T) {
	for _, ok := range []string{"a", "my-app", "app_2", "0x"} {
		if err := ValidateID(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", ".", "a/b", "A", "-a", "_a", "a b", strings.Repeat("a", 64), "../etc"} {
		if err := ValidateID(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("%q: expected ErrInvalidID, got %v", bad, err)
		}
	}
}

func TestProjectRejectsTraversal(t *testing.T) {
	d := Dirs{State: "/s", Runtime: "/r", Config: "/c"}
	if _, err := d.Project(".."); err == nil {
		t.Fatal("expected error")
	}
	p, err := d.Project("web")
	if err != nil {
		t.Fatal(err)
	}
	if p.File() != filepath.Join("/s/projects/web/project.json") {
		t.Fatalf("file: %s", p.File())
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My App":      "my-app",
		"devyard":     "devyard",
		"foo.bar":     "foo-bar",
		"__x":         "x",
		"a--b":        "a-b",
		"...":         "",
		"Weird!!Name": "weird-name",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if got := Slugify(in); got != "" {
			if err := ValidateID(got); err != nil {
				t.Errorf("Slugify(%q) produced invalid id: %v", in, err)
			}
		}
	}
}

func TestRunnerSocketShort(t *testing.T) {
	d := Dirs{Runtime: "/Users/someone/.local/state/devyard/run"}
	s := d.RunnerSocket(strings.Repeat("p", 63), "service", strings.Repeat("n", 200))
	if len(s) > 100 {
		t.Fatalf("socket path too long (%d): %s", len(s), s)
	}
}
