// Package graph contains small reusable graph primitives used by Boxy domain
// models that need explicit dependency traversal.
package graph

import "fmt"

// DAG is a directed acyclic graph with generic node identifiers.
type DAG[N comparable] struct {
	parents map[N]map[N]struct{}
}

func New[N comparable]() *DAG[N] {
	return &DAG[N]{parents: make(map[N]map[N]struct{})}
}

func (g *DAG[N]) Add(node N) {
	if g.parents == nil {
		g.parents = make(map[N]map[N]struct{})
	}
	if _, ok := g.parents[node]; !ok {
		g.parents[node] = make(map[N]struct{})
	}
}

// AddEdge adds parent -> child and rejects an edge that would create a cycle.
func (g *DAG[N]) AddEdge(parent, child N) error {
	g.Add(parent)
	g.Add(child)
	if parent == child || g.reachable(parent, child, nil) {
		return fmt.Errorf("adding edge would create a cycle")
	}
	g.parents[child][parent] = struct{}{}
	return nil
}

// Parents returns the direct parents of node. The returned slice is a copy;
// callers that need stable presentation ordering should sort their node type.
func (g *DAG[N]) Parents(node N) []N {
	parents := g.parents[node]
	result := make([]N, 0, len(parents))
	for parent := range parents {
		result = append(result, parent)
	}
	return result
}

// Ancestors returns every transitive parent of node once.
func (g *DAG[N]) Ancestors(node N) ([]N, error) {
	if _, ok := g.parents[node]; !ok {
		return nil, fmt.Errorf("node %v not found", node)
	}
	seen := make(map[N]struct{})
	result := make([]N, 0)
	var visit func(N) error
	visit = func(current N) error {
		for parent := range g.parents[current] {
			if _, ok := seen[parent]; ok {
				continue
			}
			seen[parent] = struct{}{}
			result = append(result, parent)
			if err := visit(parent); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(node); err != nil {
		return nil, err
	}
	return result, nil
}

func (g *DAG[N]) reachable(from, target N, seen map[N]struct{}) bool {
	if from == target {
		return true
	}
	if seen == nil {
		seen = make(map[N]struct{})
	}
	if _, ok := seen[from]; ok {
		return false
	}
	seen[from] = struct{}{}
	for parent := range g.parents[from] {
		if g.reachable(parent, target, seen) {
			return true
		}
	}
	return false
}
