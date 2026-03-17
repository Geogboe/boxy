package resourcepool

import "fmt"

// Keyed is implemented by items that belong to a homogeneous pool.
type Keyed[K comparable] interface {
	PoolKey() K
}

// Pool is a homogeneous collection of items identified by a key, plus an
// attached policy of caller-defined type.
//
// Policy is opaque to this package: Pool carries it, but does not interpret it.
type Pool[K comparable, R Keyed[K], P any] struct {
	Name   string `json:"name" yaml:"name"`
	Key    K      `json:"key" yaml:"key"`
	Policy P      `json:"policy" yaml:"policy"`

	Items []R `json:"items,omitempty" yaml:"items,omitempty"`
}

func New[K comparable, R Keyed[K], P any](name string, key K, policy P) Pool[K, R, P] {
	return Pool[K, R, P]{
		Name:   name,
		Key:    key,
		Policy: policy,
	}
}

// Add appends item if it satisfies the pool's homogeneity invariant.
func (p *Pool[K, R, P]) Add(item R) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	if item.PoolKey() != p.Key {
		return fmt.Errorf("pool key mismatch: expected=%v got=%v", p.Key, item.PoolKey())
	}
	p.Items = append(p.Items, item)
	return nil
}

// AddMany appends items if all satisfy the pool's homogeneity invariant.
func (p *Pool[K, R, P]) AddMany(items []R) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	for i := range items {
		if items[i].PoolKey() != p.Key {
			return fmt.Errorf("item[%d] pool key mismatch: expected=%v got=%v", i, p.Key, items[i].PoolKey())
		}
	}
	p.Items = append(p.Items, items...)
	return nil
}

// Validate checks that the pool is internally consistent.
func (p Pool[K, R, P]) Validate() error {
	for i := range p.Items {
		if p.Items[i].PoolKey() != p.Key {
			return fmt.Errorf("item[%d] pool key mismatch: expected=%v got=%v", i, p.Key, p.Items[i].PoolKey())
		}
	}
	return nil
}

// Take removes up to n items matching pred, returning exactly n items or an error.
//
// On insufficient matches, the pool is left unchanged.
func (p *Pool[K, R, P]) Take(n int, pred func(R) bool) ([]R, error) {
	if p == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	if n <= 0 {
		return nil, fmt.Errorf("n must be > 0")
	}
	if pred == nil {
		return nil, fmt.Errorf("pred is nil")
	}

	picked := make([]R, 0, n)
	remaining := make([]R, 0, len(p.Items))

	for _, it := range p.Items {
		if len(picked) < n && pred(it) {
			picked = append(picked, it)
			continue
		}
		remaining = append(remaining, it)
	}

	if len(picked) < n {
		return nil, fmt.Errorf("insufficient matching items: need=%d got=%d", n, len(picked))
	}

	p.Items = remaining
	return picked, nil
}
