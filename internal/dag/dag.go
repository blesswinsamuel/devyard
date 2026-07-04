// Package dag builds dependency graphs, detects cycles, and produces a
// topological start order (dependencies before dependents).
//
// The package is intentionally decoupled from internal/config: callers pass a
// plain map of node -> dependencies, so the graph logic can be reused and
// tested without pulling in YAML parsing or validation.
package dag

import (
	"fmt"
	"sort"
	"strings"
)

// Graph is a directed dependency graph. An edge from node N to node D means
// "N depends on D", so D must be started before N.
type Graph struct {
	nodes []string            // sorted, stable node list
	deps  map[string][]string // node -> sorted, deduped dependencies
}

// New builds a Graph from a map of node name -> its dependencies. Every
// dependency must itself be a key in deps (a node of the graph); unknown or
// self dependencies are rejected so callers can't silently drop edges.
func New(deps map[string][]string) (*Graph, error) {
	nodes := make([]string, 0, len(deps))
	for n := range deps {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	g := &Graph{
		nodes: nodes,
		deps:  make(map[string][]string, len(deps)),
	}
	for _, n := range nodes {
		seen := make(map[string]bool)
		var out []string
		for _, d := range deps[n] {
			if d == n {
				return nil, fmt.Errorf("node %q depends on itself", n)
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			if _, ok := deps[d]; !ok {
				return nil, fmt.Errorf("node %q depends on unknown node %q", n, d)
			}
			out = append(out, d)
		}
		sort.Strings(out)
		g.deps[n] = out
	}
	return g, nil
}

// color states for the three-color DFS used in Order.
const (
	white = 0 // unvisited
	gray  = 1 // on the current DFS path
	black = 2 // fully processed
)

// CycleError describes a cycle found during topological sort. Cycle lists the
// nodes in the cycle in edge order, repeating the first node at the end
// (e.g. ["a", "b", "c", "a"]).
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return "cycle detected: " + strings.Join(e.Cycle, " -> ")
}

// Order returns the graph's nodes in topological order: every node appears
// after all of its dependencies. The result is deterministic: within a given
// node's dependency set, dependencies are visited in sorted order, and roots
// are visited in sorted order. Returns a *CycleError if the graph contains a
// cycle.
func (g *Graph) Order() ([]string, error) {
	color := make(map[string]int, len(g.nodes))
	for _, n := range g.nodes {
		color[n] = white
	}

	var order []string
	stack := make([]string, 0, len(g.nodes)) // current DFS path, for cycle reporting

	var dfs func(n string) error
	dfs = func(n string) error {
		color[n] = gray
		stack = append(stack, n)
		for _, d := range g.deps[n] {
			switch color[d] {
			case white:
				if err := dfs(d); err != nil {
					return err
				}
			case gray:
				// Back edge: d is on the current path. Slice the path from d
				// to the current node and close the loop.
				for i := range stack {
					if stack[i] == d {
						cyc := append([]string{}, stack[i:]...)
						cyc = append(cyc, d)
						return &CycleError{Cycle: cyc}
					}
				}
				return &CycleError{Cycle: []string{n, d, n}}
			case black:
				// Already emitted; nothing to do.
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		order = append(order, n)
		return nil
	}

	for _, n := range g.nodes {
		if color[n] == white {
			if err := dfs(n); err != nil {
				return nil, err
			}
		}
	}
	return order, nil
}

// Nodes returns the graph's nodes in sorted order. The returned slice is a
// copy; callers may mutate it freely.
func (g *Graph) Nodes() []string {
	out := make([]string, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Deps returns the sorted dependencies of n. The returned slice is a copy.
// Panics if n is not a node of the graph.
func (g *Graph) Deps(n string) []string {
	out, ok := g.deps[n]
	if !ok {
		panic(fmt.Sprintf("dag: unknown node %q", n))
	}
	cp := make([]string, len(out))
	copy(cp, out)
	return cp
}
