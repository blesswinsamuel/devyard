package engine

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

// Drift states.
const (
	// DriftNone: the running config is the one on disk.
	DriftNone = ""
	// DriftPending: the files changed and loading them would change
	// something; the running config is untouched until it is applied.
	DriftPending = "pending"
	// DriftInvalid: the files changed but do not load. The running config
	// stays as it is.
	DriftInvalid = "invalid"
)

// Reload policies: what a project does when its config files change.
const (
	// ReloadPrompt shows the pending change and waits for an explicit
	// reload.
	ReloadPrompt = "prompt"
	// ReloadAuto applies a valid change right away, restarting only the
	// services whose definition changed.
	ReloadAuto = "auto"
	// ReloadOff ignores changes until an explicit reload.
	ReloadOff = "off"
)

// Change is one difference between the running config and the files.
type Change struct {
	// Kind is "project", "service" or "task".
	Kind string
	Name string
	// Op is "added", "removed" or "changed".
	Op string
	// Details describe what changed, for example "run changed" or "env
	// DATABASE_URL changed". Environment values are never included.
	Details []string
	// Restart is true when applying the change restarts the service.
	Restart bool
}

// Drift is the state of a project's config files against what it runs.
type Drift struct {
	State   string
	Error   string
	Changes []Change
	// Diff is a unified diff of the YAML files (env files are left out:
	// they hold secrets).
	Diff  string
	Since time.Time
}

// maxDiffBytes bounds a published diff.
const maxDiffBytes = 64 << 10

// diffLoaded lists what applying next would change in cur, sorted by kind
// and name.
func diffLoaded(cur, next *loaded) []Change {
	var out []Change
	if d := diffProject(cur, next); len(d) > 0 {
		out = append(out, Change{Kind: "project", Name: cur.project.ID, Op: "changed", Details: d})
	}
	out = append(out, diffDefs("service", cur.services, next.services)...)
	out = append(out, diffDefs("task", cur.tasks, next.tasks)...)
	return out
}

func diffProject(cur, next *loaded) []string {
	var d []string
	a, b := cur.project.File, next.project.File
	if a.Primary != b.Primary {
		d = append(d, fmt.Sprintf("primary %s → %s", orNone(a.Primary), orNone(b.Primary)))
	}
	if !reflect.DeepEqual(a.Links, b.Links) {
		d = append(d, "links changed")
	}
	if !slices.Equal(base(cur.project.EnvFiles), base(next.project.EnvFiles)) {
		d = append(d, fmt.Sprintf("env files %s → %s", orNone(strings.Join(base(cur.project.EnvFiles), ", ")), orNone(strings.Join(base(next.project.EnvFiles), ", "))))
	}
	return d
}

func base(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = p[strings.LastIndexByte(p, '/')+1:]
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func diffDefs(kind string, cur, next map[string]*ProcessDef) []Change {
	names := map[string]bool{}
	for n := range cur {
		names[n] = true
	}
	for n := range next {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []Change
	for _, n := range sorted {
		a, b := cur[n], next[n]
		switch {
		case a == nil:
			out = append(out, Change{Kind: kind, Name: n, Op: "added", Details: []string{"run " + clip(b.Cmd.String())}})
		case b == nil:
			out = append(out, Change{Kind: kind, Name: n, Op: "removed"})
		default:
			if d := diffDef(a, b); len(d) > 0 {
				out = append(out, Change{Kind: kind, Name: n, Op: "changed", Details: d, Restart: kind == "service" && a.RuntimeHash != b.RuntimeHash})
			}
		}
	}
	return out
}

// diffDef describes the differences between two definitions of the same
// process.
func diffDef(a, b *ProcessDef) []string {
	var d []string
	if a.Cmd.String() != b.Cmd.String() {
		d = append(d, fmt.Sprintf("run %s → %s", clip(a.Cmd.String()), clip(b.Cmd.String())))
	}
	if a.Dir != b.Dir {
		d = append(d, fmt.Sprintf("dir %s → %s", a.Dir, b.Dir))
	}
	d = append(d, envChanges(a.Env, b.Env)...)
	if a.TTY != b.TTY {
		d = append(d, fmt.Sprintf("tty %v → %v", a.TTY, b.TTY))
	}
	if !slices.Equal(a.Deps, b.Deps) {
		d = append(d, fmt.Sprintf("depends_on [%s] → [%s]", strings.Join(a.Deps, " "), strings.Join(b.Deps, " ")))
	}
	if a.Restart != b.Restart {
		d = append(d, fmt.Sprintf("restart %s → %s", a.Restart, b.Restart))
	}
	if !reflect.DeepEqual(a.Ready, b.Ready) {
		d = append(d, "ready probe changed")
	}
	if !reflect.DeepEqual(a.Build, b.Build) || !slices.Equal(a.BuildSources, b.BuildSources) {
		d = append(d, "build changed")
	}
	if pa, pb := portSpec(a), portSpec(b); pa != pb {
		d = append(d, fmt.Sprintf("ports %s → %s", orNone(pa), orNone(pb)))
	}
	if a.ProxyHost != b.ProxyHost {
		d = append(d, fmt.Sprintf("host %s → %s", orNone(a.ProxyHost), orNone(b.ProxyHost)))
	}
	if a.StopSignal != b.StopSignal || a.StopGrace != b.StopGrace {
		d = append(d, "stop changed")
	}
	if a.Autostart != b.Autostart {
		d = append(d, fmt.Sprintf("autostart %v → %v", a.Autostart, b.Autostart))
	}
	return d
}

// portSpec renders a definition's ports as written: auto ports by name only,
// since their numbers are allocated.
func portSpec(d *ProcessDef) string {
	parts := make([]string, 0, len(d.Ports))
	for _, p := range d.Ports {
		switch {
		case p.Auto:
			parts = append(parts, p.Name+":auto")
		case p.Name != "":
			parts = append(parts, fmt.Sprintf("%s:%d", p.Name, p.Port))
		default:
			parts = append(parts, fmt.Sprint(p.Port))
		}
	}
	return strings.Join(parts, " ")
}

// envChanges lists the variables added, removed or changed between two
// environments, by name only: values never leave the daemon.
func envChanges(a, b []string) []string {
	ma, mb := config.EnvMap(a), config.EnvMap(b)
	var added, removed, changed []string
	for k, v := range mb {
		if old, ok := ma[k]; !ok {
			added = append(added, k)
		} else if old != v {
			changed = append(changed, k)
		}
	}
	for k := range ma {
		if _, ok := mb[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	var d []string
	for _, k := range changed {
		d = append(d, "env "+k+" changed")
	}
	for _, k := range added {
		d = append(d, "env +"+k)
	}
	for _, k := range removed {
		d = append(d, "env −"+k)
	}
	return d
}

func clip(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}

// textDiff is a unified diff of the YAML inputs that differ between two
// loads, one section per file, capped at maxDiffBytes.
func textDiff(cur, next map[string]string) string {
	paths := map[string]bool{}
	for p := range cur {
		paths[p] = true
	}
	for p := range next {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	var b strings.Builder
	for _, p := range sorted {
		a, c := cur[p], next[p]
		if a == c {
			continue
		}
		b.WriteString(unifiedDiff(p, a, c))
		if b.Len() > maxDiffBytes {
			return b.String()[:maxDiffBytes] + "\n… (diff truncated)\n"
		}
	}
	return b.String()
}

// maxDiffLines bounds the files the line diff is computed for.
const maxDiffLines = 4000

// unifiedDiff renders a unified diff (three lines of context) between two
// versions of the file at path.
func unifiedDiff(path, a, b string) string {
	la, lb := splitLines(a), splitLines(b)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", path, path)
	if len(la) > maxDiffLines || len(lb) > maxDiffLines {
		out.WriteString("(file too large to diff)\n")
		return out.String()
	}
	ops := lineDiff(la, lb)
	const ctx = 3
	// Group the operations into hunks.
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for j := i; j < len(ops); j++ {
			if ops[j].kind != ' ' {
				end = j
			} else if j-end > 2*ctx {
				break
			}
		}
		stop := min(len(ops), end+ctx+1)
		aLine, bLine := ops[start].a, ops[start].b
		var aN, bN int
		var body strings.Builder
		for _, op := range ops[start:stop] {
			switch op.kind {
			case ' ':
				aN++
				bN++
			case '-':
				aN++
			case '+':
				bN++
			}
			body.WriteString(string(op.kind) + op.text + "\n")
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n%s", aLine+1, aN, bLine+1, bN, body.String())
		i = stop
	}
	return out.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
	// a and b are the 0-based line numbers the op starts at in each file.
	a, b int
}

// lineDiff computes a minimal line diff with the longest common subsequence.
func lineDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i, j})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i, j})
			j++
		}
	}
	return ops
}
