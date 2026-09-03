package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Geogboe/boxy/pkg/fulfillment"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type readyEnsurer interface {
	EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error
}

// Fulfiller reconciles pending sandbox requests into ready sandboxes.
type Fulfiller struct {
	store      store.Store
	pools      readyEnsurer
	sandboxMgr *Manager
}

type poolAllocation struct {
	poolName model.PoolName
	count    int
	packages []string
}

type allocationSnapshot struct {
	sandbox   model.Sandbox
	pools     map[model.PoolName]model.Pool
	resources map[model.ResourceID]model.Resource
}

// NewFulfiller creates a sandbox request fulfiller.
func NewFulfiller(st store.Store, pools readyEnsurer, sandboxMgr *Manager) *Fulfiller {
	return &Fulfiller{store: st, pools: pools, sandboxMgr: sandboxMgr}
}

// Reconcile processes all pending or provisioning sandbox requests.
func (f *Fulfiller) Reconcile(ctx context.Context) error {
	if f == nil {
		return fmt.Errorf("sandbox fulfiller is nil")
	}
	if f.store == nil {
		return fmt.Errorf("store is nil")
	}
	if f.pools == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if f.sandboxMgr == nil {
		return fmt.Errorf("sandbox manager is nil")
	}

	sandboxes, err := f.store.ListSandboxes(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	sort.Slice(sandboxes, func(i, j int) bool {
		return sandboxes[i].ID < sandboxes[j].ID
	})

	for _, sb := range sandboxes {
		if sb.Status != model.SandboxStatusPending && sb.Status != model.SandboxStatusProvisioning {
			continue
		}
		if err := f.reconcileSandbox(ctx, sb.ID); err != nil {
			return err
		}
	}

	return nil
}

func (f *Fulfiller) reconcileSandbox(ctx context.Context, id model.SandboxID) error {
	sb, err := f.store.GetSandbox(ctx, id)
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sandbox %q: %w", id, err)
	}
	if sb.Status != model.SandboxStatusPending && sb.Status != model.SandboxStatusProvisioning {
		return nil
	}

	if len(sb.Requests) == 0 {
		return f.failSandbox(ctx, sb, "sandbox requests are required")
	}

	pools, err := f.store.ListPools(ctx)
	if err != nil {
		return fmt.Errorf("list pools: %w", err)
	}

	allocations, err := buildAllocations(sb.Requests, pools)
	if err != nil {
		return f.failSandbox(ctx, sb, err.Error())
	}

	groups := make([]fulfillment.Group[model.PoolName], 0, len(allocations))
	packagesByPool := make(map[model.PoolName][]string, len(allocations))
	for _, alloc := range allocations {
		groups = append(groups, fulfillment.Group[model.PoolName]{Key: alloc.poolName, Count: alloc.count})
		packagesByPool[alloc.poolName] = append([]string(nil), alloc.packages...)
	}

	needsProvisioning := false
	for _, group := range groups {
		pl, err := f.store.GetPool(ctx, group.Key)
		if err != nil {
			return fmt.Errorf("get pool %q: %w", group.Key, err)
		}
		if readyCount(pl) < group.Count {
			needsProvisioning = true
			break
		}
	}

	if needsProvisioning && sb.Status != model.SandboxStatusProvisioning {
		sb.Status = model.SandboxStatusProvisioning
		sb.Error = ""
		if err := f.store.PutSandbox(ctx, sb); err != nil {
			return fmt.Errorf("mark sandbox %q provisioning: %w", sb.ID, err)
		}
	}

	transaction := fulfillment.Transaction[model.PoolName, allocationSnapshot]{
		Prepare: func(ctx context.Context, group fulfillment.Group[model.PoolName]) error {
			if err := f.pools.EnsureReady(ctx, group.Key, group.Count); err != nil {
				return fmt.Errorf("ensure ready for pool %q: %w", group.Key, err)
			}
			pl, err := f.store.GetPool(ctx, group.Key)
			if err != nil {
				return fmt.Errorf("get pool %q after reconcile: %w", group.Key, err)
			}
			if readyCount(pl) < group.Count {
				return fmt.Errorf("pool %q has %d ready resource(s), need %d", group.Key, readyCount(pl), group.Count)
			}
			return nil
		},
		Snapshot: func(ctx context.Context, groups []fulfillment.Group[model.PoolName]) (allocationSnapshot, error) {
			allocationGroups := make([]poolAllocation, 0, len(groups))
			for _, group := range groups {
				allocationGroups = append(allocationGroups, poolAllocation{
					poolName: group.Key,
					count:    group.Count,
					packages: packagesByPool[group.Key],
				})
			}
			return f.captureAllocationSnapshot(ctx, sb, allocationGroups)
		},
		Fulfill: func(ctx context.Context, group fulfillment.Group[model.PoolName]) error {
			current, err := f.store.GetSandbox(ctx, sb.ID)
			if err == store.ErrNotFound {
				return fulfillment.Abort(store.ErrNotFound)
			}
			if err != nil {
				return fmt.Errorf("get sandbox %q before allocation: %w", sb.ID, err)
			}
			if current.Status == model.SandboxStatusDeleting {
				return fulfillment.Abort(ErrSandboxDeleting)
			}
			if _, err := f.sandboxMgr.AddFromPoolWithPackages(ctx, sb.ID, group.Key, group.Count, packagesByPool[group.Key]); err != nil {
				if errors.Is(err, ErrSandboxDeleting) {
					return fulfillment.Abort(err)
				}
				return fmt.Errorf("allocate from pool %q: %w", group.Key, err)
			}
			return nil
		},
		Rollback: func(ctx context.Context, snapshot allocationSnapshot, cause error) error {
			return f.rollbackAllocation(ctx, snapshot, cause.Error())
		},
	}

	if err := transaction.Run(ctx, groups); err != nil {
		var rolledBack fulfillment.RolledBackError
		if errors.As(err, &rolledBack) || errors.Is(err, ErrSandboxDeleting) || errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return f.failSandbox(ctx, sb, err.Error())
	}

	final, err := f.store.GetSandbox(ctx, sb.ID)
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sandbox %q after allocation: %w", sb.ID, err)
	}
	if final.Status == model.SandboxStatusDeleting {
		return nil
	}
	final.Status = model.SandboxStatusReady
	final.Error = ""
	if err := f.store.PutSandbox(ctx, final); err != nil {
		return fmt.Errorf("mark sandbox %q ready: %w", final.ID, err)
	}
	return nil
}

func (f *Fulfiller) captureAllocationSnapshot(
	ctx context.Context,
	sb model.Sandbox,
	allocations []poolAllocation,
) (allocationSnapshot, error) {
	snapshot := allocationSnapshot{
		sandbox:   sb,
		pools:     make(map[model.PoolName]model.Pool, len(allocations)),
		resources: make(map[model.ResourceID]model.Resource),
	}

	for _, alloc := range allocations {
		pl, err := f.store.GetPool(ctx, alloc.poolName)
		if err != nil {
			return allocationSnapshot{}, fmt.Errorf("get pool %q: %w", alloc.poolName, err)
		}
		snapshot.pools[alloc.poolName] = pl
		for _, res := range pl.Inventory.Resources {
			if _, exists := snapshot.resources[res.ID]; exists {
				continue
			}
			stored, err := f.store.GetResource(ctx, res.ID)
			if err != nil {
				return allocationSnapshot{}, fmt.Errorf("get resource %q: %w", res.ID, err)
			}
			snapshot.resources[res.ID] = stored
		}
	}

	return snapshot, nil
}

func (f *Fulfiller) rollbackAllocation(ctx context.Context, snapshot allocationSnapshot, msg string) error {
	resourceIDs := make([]string, 0, len(snapshot.resources))
	for id := range snapshot.resources {
		resourceIDs = append(resourceIDs, string(id))
	}
	sort.Strings(resourceIDs)
	for _, id := range resourceIDs {
		res := snapshot.resources[model.ResourceID(id)]
		if err := f.store.PutResource(ctx, res); err != nil {
			return fmt.Errorf("restore resource %q: %w", res.ID, err)
		}
	}

	poolNames := make([]string, 0, len(snapshot.pools))
	for name := range snapshot.pools {
		poolNames = append(poolNames, string(name))
	}
	sort.Strings(poolNames)
	for _, name := range poolNames {
		pl := snapshot.pools[model.PoolName(name)]
		if err := f.store.PutPool(ctx, pl); err != nil {
			return fmt.Errorf("restore pool %q: %w", pl.Name, err)
		}
	}

	current, err := f.store.GetSandbox(ctx, snapshot.sandbox.ID)
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sandbox %q after rollback: %w", snapshot.sandbox.ID, err)
	}
	if current.Status == model.SandboxStatusDeleting {
		deleting := snapshot.sandbox
		deleting.Status = model.SandboxStatusDeleting
		deleting.Error = current.Error
		if err := f.store.PutSandbox(ctx, deleting); err != nil {
			return fmt.Errorf("restore deleting sandbox %q after rollback: %w", deleting.ID, err)
		}
		return nil
	}

	failed := snapshot.sandbox
	failed.Status = model.SandboxStatusFailed
	failed.Error = msg
	if err := f.store.PutSandbox(ctx, failed); err != nil {
		return fmt.Errorf("mark sandbox %q failed after rollback: %w", failed.ID, err)
	}
	return nil
}

func (f *Fulfiller) failSandbox(ctx context.Context, sb model.Sandbox, msg string) error {
	current, err := f.store.GetSandbox(ctx, sb.ID)
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sandbox %q for failure update: %w", sb.ID, err)
	}
	if current.Status == model.SandboxStatusDeleting {
		return nil
	}
	current.Status = model.SandboxStatusFailed
	current.Error = msg
	if err := f.store.PutSandbox(ctx, current); err != nil {
		return fmt.Errorf("mark sandbox %q failed: %w", sb.ID, err)
	}
	return nil
}

func buildAllocations(requests []model.ResourceRequest, pools []model.Pool) ([]poolAllocation, error) {
	indexByPool := make(map[model.PoolName]int)
	allocations := make([]poolAllocation, 0, len(requests))

	for _, req := range requests {
		if err := req.Validate(); err != nil {
			return nil, fmt.Errorf("invalid sandbox request: %v", err)
		}
		poolName, matchErr := matchPool(req, pools)
		if matchErr != nil {
			return nil, matchErr
		}
		if idx, ok := indexByPool[poolName]; ok {
			allocations[idx].count += req.Count
			allocations[idx].packages = appendUnique(allocations[idx].packages, req.Packages...)
			continue
		}
		indexByPool[poolName] = len(allocations)
		allocations = append(allocations, poolAllocation{poolName: poolName, count: req.Count, packages: append([]string(nil), req.Packages...)})
	}

	return allocations, nil
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func matchPool(req model.ResourceRequest, pools []model.Pool) (model.PoolName, error) {
	var matches []model.PoolName
	for _, pl := range pools {
		if pl.Inventory.ExpectedType != req.Type {
			continue
		}
		if pl.Inventory.ExpectedProfile != req.Profile {
			continue
		}
		matches = append(matches, pl.Name)
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no pool matches request type=%q profile=%q", req.Type, req.Profile)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, name := range matches {
			names = append(names, string(name))
		}
		sort.Strings(names)
		return "", fmt.Errorf("multiple pools match request type=%q profile=%q: %s", req.Type, req.Profile, strings.Join(names, ", "))
	}
}

func readyCount(p model.Pool) int {
	count := 0
	for _, res := range p.Inventory.Resources {
		if res.State == model.ResourceStateReady {
			count++
		}
	}
	return count
}
