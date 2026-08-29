package graph

import "testing"

func TestDAGRejectsCyclesAndReturnsAncestors(t *testing.T) {
	g := New[string]()
	for _, node := range []string{"base", "apps12", "apps123"} {
		g.Add(node)
	}
	if err := g.AddEdge("base", "apps12"); err != nil {
		t.Fatalf("AddEdge base->apps12: %v", err)
	}
	if err := g.AddEdge("apps12", "apps123"); err != nil {
		t.Fatalf("AddEdge apps12->apps123: %v", err)
	}
	ancestors, err := g.Ancestors("apps123")
	if err != nil {
		t.Fatalf("Ancestors: %v", err)
	}
	if got, want := len(ancestors), 2; got != want {
		t.Fatalf("ancestor count = %d, want %d", got, want)
	}
	if err := g.AddEdge("apps123", "base"); err == nil {
		t.Fatal("cycle edge succeeded")
	}
}
