package dag_test

import (
	"errors"
	"testing"

	"github.com/blesswinsamuel/devyard/internal/dag"
)

func TestOrderSimpleChain(t *testing.T) {
	t.Parallel()
	// web -> api -> db  (web depends on api, api depends on db)
	g, err := dag.New(map[string][]string{
		"db":  nil,
		"api": {"db"},
		"web": {"api"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	order, err := g.Order()
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	want := []string{"db", "api", "web"}
	if !equal(order, want) {
		t.Fatalf("order: got %v want %v", order, want)
	}
}

func TestOrderDiamond(t *testing.T) {
	t.Parallel()
	//      top
	//     /   \
	//    a     b
	//     \   /
	//      base
	g, err := dag.New(map[string][]string{
		"base": nil,
		"a":    {"base"},
		"b":    {"base"},
		"top":  {"a", "b"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	order, err := g.Order()
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	// base must come before a, b; a and b must come before top.
	pos := indexMap(order)
	if pos["base"] >= pos["a"] || pos["base"] >= pos["b"] {
		t.Fatalf("base not before dependents: %v", order)
	}
	if pos["a"] >= pos["top"] || pos["b"] >= pos["top"] {
		t.Fatalf("deps not before top: %v", order)
	}
}

func TestOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	deps := map[string][]string{
		"db":  nil,
		"api": {"db"},
		"web": {"api"},
	}
	g, err := dag.New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := g.Order()
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	for i := 0; i < 5; i++ {
		second, err := g.Order()
		if err != nil {
			t.Fatalf("Order: %v", err)
		}
		if !equal(first, second) {
			t.Fatalf("non-deterministic order: %v vs %v", first, second)
		}
	}
}

func TestOrderIndependentNodesSorted(t *testing.T) {
	t.Parallel()
	g, err := dag.New(map[string][]string{
		"zeta":  nil,
		"alpha": nil,
		"mid":   nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	order, err := g.Order()
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if !equal(order, want) {
		t.Fatalf("order: got %v want %v", order, want)
	}
}

func TestCycleDetected(t *testing.T) {
	t.Parallel()
	// a -> b -> c -> a
	g, err := dag.New(map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = g.Order()
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	var ce *dag.CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *dag.CycleError, got %T: %v", err, err)
	}
	if len(ce.Cycle) < 3 {
		t.Fatalf("cycle too short: %v", ce.Cycle)
	}
	first := ce.Cycle[0]
	last := ce.Cycle[len(ce.Cycle)-1]
	if first != last {
		t.Fatalf("cycle not closed: %v", ce.Cycle)
	}
	// Every node in the cycle must be part of {a,b,c}.
	for _, n := range ce.Cycle {
		if n != "a" && n != "b" && n != "c" {
			t.Fatalf("unexpected node in cycle: %q in %v", n, ce.Cycle)
		}
	}
}

func TestSelfDepRejected(t *testing.T) {
	t.Parallel()
	if _, err := dag.New(map[string][]string{
		"a": {"a"},
	}); err == nil {
		t.Fatal("expected error for self-dependency")
	}
}

func TestUnknownDepRejected(t *testing.T) {
	t.Parallel()
	if _, err := dag.New(map[string][]string{
		"a": {"missing"},
	}); err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestDepsDedupedAndSorted(t *testing.T) {
	t.Parallel()
	g, err := dag.New(map[string][]string{
		"db":    nil,
		"cache": nil,
		"api":   {"db", "cache", "db"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := g.Deps("api"); !equal(got, []string{"cache", "db"}) {
		t.Fatalf("deps: got %v", got)
	}
}

func TestDepsUnknownPanics(t *testing.T) {
	t.Parallel()
	g, err := dag.New(map[string][]string{"a": nil})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown node")
		}
	}()
	_ = g.Deps("nope")
}

func TestNodesSortedCopy(t *testing.T) {
	t.Parallel()
	g, err := dag.New(map[string][]string{
		"b": nil,
		"a": nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nodes := g.Nodes()
	if !equal(nodes, []string{"a", "b"}) {
		t.Fatalf("nodes: %v", nodes)
	}
	nodes[0] = "mutated"
	if got := g.Nodes(); got[0] != "a" {
		t.Fatalf("Nodes did not return a copy: %v", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexMap(s []string) map[string]int {
	m := make(map[string]int, len(s))
	for i, v := range s {
		m[v] = i
	}
	return m
}
