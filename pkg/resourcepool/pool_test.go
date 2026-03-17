package resourcepool

import "testing"

type testItem struct {
	key string
	id  string
}

func (t testItem) PoolKey() string { return t.key }

func TestPool_Add_EnforcesHomogeneity(t *testing.T) {
	t.Parallel()

	p := New[string, testItem, struct{}]("p", "k1", struct{}{})
	if err := p.Add(testItem{key: "k1", id: "a"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := p.Add(testItem{key: "k2", id: "b"}); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestPool_Take_RemovesExactlyN(t *testing.T) {
	t.Parallel()

	p := New[string, testItem, struct{}]("p", "k1", struct{}{})
	_ = p.AddMany([]testItem{
		{key: "k1", id: "a"},
		{key: "k1", id: "b"},
		{key: "k1", id: "c"},
	})

	picked, err := p.Take(2, func(it testItem) bool { return it.id != "" })
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(picked) != 2 {
		t.Fatalf("picked len=%d, want 2", len(picked))
	}
	if len(p.Items) != 1 {
		t.Fatalf("remaining len=%d, want 1", len(p.Items))
	}
}

func TestPool_Take_InsufficientDoesNotMutate(t *testing.T) {
	t.Parallel()

	p := New[string, testItem, struct{}]("p", "k1", struct{}{})
	_ = p.AddMany([]testItem{{key: "k1", id: "a"}})

	if _, err := p.Take(2, func(it testItem) bool { return it.id != "" }); err == nil {
		t.Fatalf("expected error, got nil")
	}
	if len(p.Items) != 1 {
		t.Fatalf("remaining len=%d, want 1", len(p.Items))
	}
}
