package allocator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
)

type stubPool struct {
	allocateErr error
	releaseErr  error
	allocated   int
	released    []string
	resource    *provider.Resource
}

func (s *stubPool) Allocate(ctx context.Context, sandboxID string, expiresAt *time.Time) (*provider.Resource, error) {
	if s.allocateErr != nil {
		return nil, s.allocateErr
	}
	s.allocated++
	return s.resource, nil
}

func (s *stubPool) Release(ctx context.Context, resourceID string) error {
	if s.releaseErr != nil {
		return s.releaseErr
	}
	s.released = append(s.released, resourceID)
	return nil
}

type stubRepo struct {
	resources map[string]*provider.Resource
}

func (s *stubRepo) GetResourceByID(ctx context.Context, id string) (*provider.Resource, error) {
	res, ok := s.resources[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return res, nil
}

func (s *stubRepo) GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*provider.Resource, error) {
	var out []*provider.Resource
	for _, res := range s.resources {
		if res.SandboxID != nil && *res.SandboxID == sandboxID {
			out = append(out, res)
		}
	}
	return out, nil
}

func TestAllocatorAllocate(t *testing.T) {
	res := &provider.Resource{ID: "res-1", PoolID: "pool-a"}
	pool := &stubPool{resource: res}
	repo := &stubRepo{resources: map[string]*provider.Resource{"res-1": res}}

	alloc := New(map[string]PoolAllocator{"pool-a": pool}, repo, logrus.New())

	expiresAt := time.Now().Add(30 * time.Minute)
	got, err := alloc.Allocate(context.Background(), "pool-a", "sb-1", &expiresAt)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got.ID != "res-1" {
		t.Fatalf("unexpected resource id %s", got.ID)
	}

	if pool.allocated != 1 {
		t.Fatalf("expected allocate to be called once, got %d", pool.allocated)
	}
}

func TestAllocatorReleaseResource(t *testing.T) {
	sbID := "sb-1"
	res := &provider.Resource{ID: "res-1", PoolID: "pool-a", SandboxID: &sbID}
	pool := &stubPool{resource: res}
	repo := &stubRepo{resources: map[string]*provider.Resource{"res-1": res}}

	alloc := New(map[string]PoolAllocator{"pool-a": pool}, repo, logrus.New())

	if err := alloc.ReleaseResource(context.Background(), "res-1"); err != nil {
		t.Fatalf("expected no error releasing resource: %v", err)
	}

	if len(pool.released) != 1 || pool.released[0] != "res-1" {
		t.Fatalf("expected resource to be released, got %#v", pool.released)
	}
}

func TestAllocatorReleaseSandboxAggregatesErrors(t *testing.T) {
	sbID := "sb-err"
	res := &provider.Resource{ID: "res-err", PoolID: "pool-a", SandboxID: &sbID}
	pool := &stubPool{resource: res, releaseErr: errors.New("boom")}
	repo := &stubRepo{resources: map[string]*provider.Resource{"res-err": res}}

	alloc := New(map[string]PoolAllocator{"pool-a": pool}, repo, logrus.New())

	if err := alloc.ReleaseSandbox(context.Background(), sbID); err == nil {
		t.Fatalf("expected aggregated error from release")
	}
}
