package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	// Aliased: this file's allocation helpers all take a `pool model.Pool`
	// parameter, which would shadow an unaliased import of this package.
	boxypool "github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/resourcepool"
	"github.com/Geogboe/boxy/pkg/segmentcidr"
	"github.com/Geogboe/boxy/pkg/store"
)

var (
	ErrSandboxDeleting = errors.New("sandbox is deleting")

	// ErrNoExpiry is returned by RequestExtend when the sandbox has no
	// Policies.AutoDestroyAfter-derived expiry to extend.
	ErrNoExpiry = errors.New("sandbox has no expiry to extend (policies.auto_destroy_after not set)")
)

// Clock abstracts time so expiry computation is deterministic in tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Manager creates sandboxes and consumes resources from pools.
//
// This is the "demand side" counterpart to PoolManager.
type Manager struct {
	store     store.Store
	allocator SandboxAllocator
	clock     Clock

	guestCredentialsMu sync.Mutex
	guestCredentials   map[model.SandboxID]map[model.ResourceID]*providersdk.GuestCredential
}

// New creates a Manager. allocator may be nil — if so, allocation-time hooks
// are skipped and resource Properties are not updated at allocation time.
func New(s store.Store, allocator SandboxAllocator) *Manager {
	return &Manager{
		store:            s,
		allocator:        allocator,
		clock:            realClock{},
		guestCredentials: make(map[model.SandboxID]map[model.ResourceID]*providersdk.GuestCredential),
	}
}

// SetClock overrides the manager's time source. Used by tests.
func (m *Manager) SetClock(c Clock) {
	if c != nil {
		m.clock = c
	}
}

func (m *Manager) now() time.Time {
	if m.clock == nil {
		return time.Now().UTC()
	}
	return m.clock.Now()
}

// expiresAt computes an absolute expiry from policies.AutoDestroyAfter,
// relative to now. Returns nil (no expiry) if the policy is unset.
func (m *Manager) expiresAt(policies model.SandboxPolicies) (*time.Time, error) {
	if policies.AutoDestroyAfter == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(policies.AutoDestroyAfter)
	if err != nil {
		return nil, fmt.Errorf("policies.auto_destroy_after %q: %w", policies.AutoDestroyAfter, err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("policies.auto_destroy_after %q must be positive", policies.AutoDestroyAfter)
	}
	t := m.now().Add(d)
	return &t, nil
}

// Create creates an empty sandbox request.
func (m *Manager) Create(ctx context.Context, sbName string, policies model.SandboxPolicies) (model.Sandbox, error) {
	return m.CreateRequested(ctx, sbName, policies, nil)
}

// CreateRequested creates a sandbox request in pending state.
func (m *Manager) CreateRequested(
	ctx context.Context,
	sbName string,
	policies model.SandboxPolicies,
	requests []model.ResourceRequest,
) (model.Sandbox, error) {
	return m.CreateRequestedOwned(ctx, sbName, policies, requests, "")
}

// CreateRequestedOwned creates a sandbox request and records the API-key
// identity that owns it. An empty owner preserves local unauthenticated mode.
func (m *Manager) CreateRequestedOwned(
	ctx context.Context,
	sbName string,
	policies model.SandboxPolicies,
	requests []model.ResourceRequest,
	ownerID string,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	sbID, err := newSandboxID()
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("new sandbox id: %w", err)
	}
	expiresAt, err := m.expiresAt(policies)
	if err != nil {
		return model.Sandbox{}, err
	}
	sb := model.Sandbox{
		ID:        sbID,
		Name:      sbName,
		OwnerID:   ownerID,
		Policies:  policies,
		Status:    model.SandboxStatusPending,
		Requests:  append([]model.ResourceRequest(nil), requests...),
		ExpiresAt: expiresAt,
	}
	if err := m.store.CreateSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	return sb, nil
}

// AddFromPool attaches N ready resources from a pool to an existing sandbox.
//
// Resources never return to the pool (see ADR-0002).
func (m *Manager) AddFromPool(
	ctx context.Context,
	sbID model.SandboxID,
	poolName model.PoolName,
	count int,
) (model.Sandbox, error) {
	return m.AddFromPoolWithPackages(ctx, sbID, poolName, count, nil)
}

// AddFromPoolWithPackages attaches ready resources and optionally applies
// allocation-scoped packages through an allocator capability. The original
// AddFromPool method remains the compatibility path for callers with no
// package request.
func (m *Manager) AddFromPoolWithPackages(
	ctx context.Context,
	sbID model.SandboxID,
	poolName model.PoolName,
	count int,
	packages []string,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}
	if poolName == "" {
		return model.Sandbox{}, fmt.Errorf("pool name is required")
	}
	if count <= 0 {
		return model.Sandbox{}, fmt.Errorf("count must be > 0")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
	}
	if sb.Status == model.SandboxStatusDeleting {
		return model.Sandbox{}, ErrSandboxDeleting
	}

	pool, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get pool: %w", err)
	}

	inv := resourcepool.Pool[invKey, keyedResource, struct{}]{
		Key:   invKey{Type: pool.Inventory.ExpectedType, Profile: pool.Inventory.ExpectedProfile},
		Items: wrapResources(pool.Inventory.Resources),
	}
	picked, err := inv.Take(count, func(r keyedResource) bool { return r.State == model.ResourceStateReady })
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("select from pool %q: %w", poolName, err)
	}
	selected := unwrapResources(picked)
	pool.Inventory.Resources = unwrapResources(inv.Items)

	if err := m.store.PutPool(ctx, pool); err != nil {
		return model.Sandbox{}, fmt.Errorf("put pool: %w", err)
	}

	sb.Resources = append(sb.Resources, resourceIDs(selected)...)
	for _, res := range selected {
		if res.ID == "" {
			return model.Sandbox{}, fmt.Errorf("selected resource has empty id")
		}
		if res.OriginPool == "" {
			res.OriginPool = pool.Name
		}
		if m.allocator != nil {
			var allocation providersdk.AllocationResult
			var err error
			if packageAllocator, ok := m.allocator.(PackageSandboxAllocator); ok {
				allocation, err = packageAllocator.AllocateWithPackages(ctx, pool, res, packages)
			} else {
				allocation, err = m.allocator.Allocate(ctx, pool, res)
			}
			if err != nil {
				return model.Sandbox{}, fmt.Errorf("allocate resource %q: %w", res.ID, err)
			}
			if allocation.Properties != nil {
				if res.Properties == nil {
					res.Properties = make(map[string]any)
				}
				maps.Copy(res.Properties, allocation.Properties)
			}
			if allocation.GuestCredential != nil {
				m.rememberGuestCredential(sb.ID, res.ID, allocation.GuestCredential)
			}
			if len(allocation.AppliedPackages) != 0 {
				res.AppliedPackages = append(res.AppliedPackages, allocation.AppliedPackages...)
			}
			if err := m.ensureNetworkSegment(ctx, &sb, pool, res); err != nil {
				return model.Sandbox{}, fmt.Errorf("ensure network segment for resource %q: %w", res.ID, err)
			}
		}
		res.State = model.ResourceStateAllocated
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Sandbox{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
	}

	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("put sandbox: %w", err)
	}

	return sb, nil
}

type invKey struct {
	Type    model.ResourceType
	Profile model.ResourceProfile
}

type keyedResource struct{ model.Resource }

func (r keyedResource) PoolKey() invKey {
	return invKey{Type: r.Type, Profile: r.Profile}
}

// CreateFromPool creates a sandbox and attaches N ready resources from a pool.
//
// Resources never return to the pool (see ADR-0002).
func (m *Manager) CreateFromPool(
	ctx context.Context,
	poolName model.PoolName,
	count int,
	sbName string,
	policies model.SandboxPolicies,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if poolName == "" {
		return model.Sandbox{}, fmt.Errorf("pool name is required")
	}
	if count <= 0 {
		return model.Sandbox{}, fmt.Errorf("count must be > 0")
	}

	pool, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get pool: %w", err)
	}

	inv := resourcepool.Pool[invKey, keyedResource, struct{}]{
		Key:   invKey{Type: pool.Inventory.ExpectedType, Profile: pool.Inventory.ExpectedProfile},
		Items: wrapResources(pool.Inventory.Resources),
	}
	picked, err := inv.Take(count, func(r keyedResource) bool { return r.State == model.ResourceStateReady })
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("select from pool %q: %w", poolName, err)
	}
	selected := unwrapResources(picked)
	pool.Inventory.Resources = unwrapResources(inv.Items)

	if err := m.store.PutPool(ctx, pool); err != nil {
		return model.Sandbox{}, fmt.Errorf("put pool: %w", err)
	}

	sbID, err := newSandboxID()
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("new sandbox id: %w", err)
	}
	expiresAt, err := m.expiresAt(policies)
	if err != nil {
		return model.Sandbox{}, err
	}

	sb := model.Sandbox{
		ID:        sbID,
		Name:      sbName,
		Policies:  policies,
		Status:    model.SandboxStatusReady,
		Resources: resourceIDs(selected),
		ExpiresAt: expiresAt,
	}
	if err := m.store.CreateSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}

	// Mark resources allocated in the global resource store.
	for _, res := range selected {
		if res.ID == "" {
			return model.Sandbox{}, fmt.Errorf("selected resource has empty id")
		}
		if res.OriginPool == "" {
			res.OriginPool = pool.Name
		}
		if m.allocator != nil {
			allocation, err := m.allocator.Allocate(ctx, pool, res)
			if err != nil {
				return model.Sandbox{}, fmt.Errorf("allocate resource %q: %w", res.ID, err)
			}
			if allocation.Properties != nil {
				if res.Properties == nil {
					res.Properties = make(map[string]any)
				}
				maps.Copy(res.Properties, allocation.Properties)
			}
			if allocation.GuestCredential != nil {
				m.rememberGuestCredential(sb.ID, res.ID, allocation.GuestCredential)
			}
			if err := m.ensureNetworkSegment(ctx, &sb, pool, res); err != nil {
				return model.Sandbox{}, fmt.Errorf("ensure network segment for resource %q: %w", res.ID, err)
			}
		}
		res.State = model.ResourceStateAllocated
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Sandbox{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
	}

	if len(sb.NetworkSegments) != 0 {
		if err := m.store.PutSandbox(ctx, sb); err != nil {
			return model.Sandbox{}, fmt.Errorf("put sandbox: %w", err)
		}
	}

	return sb, nil
}

// RequestDelete marks a sandbox for asynchronous deletion. Cleanup is performed
// by the daemon reconciliation loop.
func (m *Manager) RequestDelete(ctx context.Context, sbID model.SandboxID) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
	}
	if sb.Status == model.SandboxStatusDeleting {
		return sb, nil
	}
	sb.Status = model.SandboxStatusDeleting
	sb.Error = ""
	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("mark sandbox %q deleting: %w", sb.ID, err)
	}
	m.ForgetGuestCredentials(sb.ID)
	return sb, nil
}

// RequestExtend pushes a sandbox's automatic-expiry deadline further out by
// extension, measured from its current expiry (not from now), so extending
// twice compounds rather than resetting the clock. Fails if the sandbox has
// no expiry to extend (Policies.AutoDestroyAfter was never set) or is
// already being deleted.
func (m *Manager) RequestExtend(ctx context.Context, sbID model.SandboxID, extension time.Duration) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}
	if extension <= 0 {
		return model.Sandbox{}, fmt.Errorf("extension duration must be positive")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
	}
	if sb.Status == model.SandboxStatusDeleting {
		return model.Sandbox{}, ErrSandboxDeleting
	}
	if sb.ExpiresAt == nil {
		return model.Sandbox{}, ErrNoExpiry
	}

	newExpiry := sb.ExpiresAt.Add(extension)
	sb.ExpiresAt = &newExpiry
	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("extend sandbox %q: %w", sb.ID, err)
	}
	return sb, nil
}

func resourceIDs(rs []model.Resource) []model.ResourceID {
	ids := make([]model.ResourceID, 0, len(rs))
	for _, r := range rs {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// GuestCredentialDelivery is the one-time, process-local credential returned
// to a caller for a sandbox resource. It is deliberately not part of
// model.Resource, so store persistence cannot accidentally retain it.
type GuestCredentialDelivery struct {
	ResourceID model.ResourceID             `json:"resource_id"`
	Credential *providersdk.GuestCredential `json:"credential"`
}

func (m *Manager) rememberGuestCredential(sbID model.SandboxID, resourceID model.ResourceID, credential *providersdk.GuestCredential) {
	if credential == nil {
		return
	}
	m.guestCredentialsMu.Lock()
	defer m.guestCredentialsMu.Unlock()
	if m.guestCredentials == nil {
		m.guestCredentials = make(map[model.SandboxID]map[model.ResourceID]*providersdk.GuestCredential)
	}
	byResource := m.guestCredentials[sbID]
	if byResource == nil {
		byResource = make(map[model.ResourceID]*providersdk.GuestCredential)
		m.guestCredentials[sbID] = byResource
	}
	copyCredential := *credential
	copyCredential.Data = append([]byte(nil), credential.Data...)
	byResource[resourceID] = &copyCredential
}

// TakeGuestCredentials returns and removes all credentials currently held for
// a sandbox. The returned values are process-local and disappear on restart.
func (m *Manager) TakeGuestCredentials(sbID model.SandboxID) []GuestCredentialDelivery {
	m.guestCredentialsMu.Lock()
	defer m.guestCredentialsMu.Unlock()
	byResource := m.guestCredentials[sbID]
	if len(byResource) == 0 {
		return nil
	}
	delete(m.guestCredentials, sbID)
	out := make([]GuestCredentialDelivery, 0, len(byResource))
	for resourceID, credential := range byResource {
		copyCredential := *credential
		copyCredential.Data = append([]byte(nil), credential.Data...)
		out = append(out, GuestCredentialDelivery{ResourceID: resourceID, Credential: &copyCredential})
	}
	return out
}

// ForgetGuestCredentials drops any not-yet-fetched credentials during sandbox
// deletion. It is safe to call for a sandbox with no pending credentials.
func (m *Manager) ForgetGuestCredentials(sbID model.SandboxID) {
	m.guestCredentialsMu.Lock()
	defer m.guestCredentialsMu.Unlock()
	delete(m.guestCredentials, sbID)
}

// ensureNetworkSegment attaches res to sb's segment for res's (agent,
// provider type) pair, creating that segment first if this is the first
// resource with that pairing this sandbox has seen. Mutates
// sb.NetworkSegments in place and, on creating a new segment, persists sb
// immediately (see below).
//
// Reuse is keyed on BOTH the agent and the provider type, never the agent
// alone. A segment is a provider-specific host object -- a Hyper-V vSwitch, a
// Docker network -- so one agent hosting two drivers owns two unrelated
// segments for the same sandbox, and handing one driver's ref to the other is
// a hard failure inside that driver, not a graceful degradation. This is the
// ordinary daemon shape rather than an edge case: internal/cli/serve.go builds
// one embedded agent over every configured driver, so every mixed-provider
// sandbox shares a single agent ID across its pools.
//
// res.Provider.Name is the resolved provider type to match on:
// pool.AgentProvisioner.ProvisionLocked stamps the pool's resolved driver
// type onto each resource it creates, and CreateSegment returns that same
// resolved type for the segment it records (see CompatibleWithPool, which
// already treats the two as one value).
//
// That is an invariant, not a guarantee, and there is exactly one shape where
// it can break: editing a pool's spec.Type/spec.Provider in config *after* its
// resources were provisioned. The reuse key (res.Provider.Name, the old type)
// would then miss the segment CreateSegment records (the new type), and each
// resource would append its own duplicate segment record. Harmless in
// practice -- DestroySegment is idempotent, so duplicates tear down cleanly --
// and the same config drift already misroutes Allocate/Destroy upstream of
// here, so it is not worth a defensive check on this path. Noted so a reader
// does not have to reconstruct it.
//
// There are two distinct ways isolation is skipped rather than failed,
// matching this plan's Global Constraints:
//
//   - m.allocator doesn't implement NetworkIsolatingAllocator at all — no
//     isolation-capable provider is wired up in this deployment.
//   - The allocator reports pool.ErrNetworkIsolationUnsupported, meaning
//     the specific agent owning res advertises no providersdk.NetworkIsolator
//     support for res's provider type. A sandbox may legitimately mix
//     resources from an isolating pool and a non-isolating one; the latter
//     get no segment and no error. Every other error is a hard failure.
//
// The new-segment branch persists sb between CreateSegment and
// AttachToSegment, not at the caller's end-of-loop. CreateSegment has by
// then made a real host object (a vSwitch, a NAT, a Docker network) that
// only this ref can address, so any later error — a failing attach right
// below, or a different resource failing on a subsequent loop iteration —
// would otherwise discard the in-memory append along with the caller's
// stack frame and strand that object with no record of it anywhere. Once
// persisted, it is reachable by sandbox deletion's own segment teardown.
//
// store.PutSandbox writes the whole record, so this also lands whatever
// else the caller has already staged on sb — notably the resource-ID list
// AddFromPoolWithPackages appends before its loop. That is strictly safer
// than persisting it later: those resources are already out of the pool's
// ready inventory (PutPool ran first), and deletion tolerates a listed
// resource that has no store record.
func (m *Manager) ensureNetworkSegment(ctx context.Context, sb *model.Sandbox, pool model.Pool, res model.Resource) error {
	isolator, ok := m.allocator.(NetworkIsolatingAllocator)
	if !ok {
		return nil
	}
	agentID := res.Provider.AgentID
	providerType := res.Provider.Name
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == agentID && seg.ProviderType == providerType {
			return isolator.AttachToSegment(ctx, pool, res, providersdk.SegmentRef(seg.Ref))
		}
	}
	// createdType, not providerType: the allocator resolves the segment's
	// provider type itself and is the authority on what was actually
	// created. In production it equals providerType above (both are the
	// pool's resolved driver type); recording what the allocator returned
	// keeps the record true to the host object even if they ever diverge.
	ref, createdType, cidr, err := m.createSegmentWithCIDR(ctx, isolator, sb, pool, res)
	if errors.Is(err, boxypool.ErrNetworkIsolationUnsupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create network segment for sandbox %q: %w", sb.ID, err)
	}
	sb.NetworkSegments = append(sb.NetworkSegments, model.NetworkSegment{
		AgentID:      agentID,
		ProviderType: string(createdType),
		Ref:          string(ref),
		CIDR:         cidr,
	})
	if err := m.store.PutSandbox(ctx, *sb); err != nil {
		return fmt.Errorf("persist network segment for sandbox %q: %w", sb.ID, err)
	}
	if err := isolator.AttachToSegment(ctx, pool, res, ref); err != nil {
		return fmt.Errorf("attach resource %q to network segment: %w", res.ID, err)
	}
	// Mesh peering failure is logged, not returned. Cross-host overlay
	// traffic doesn't work yet (#379), and a host that can't create a
	// WireGuard device (no CAP_NET_ADMIN, no Wintun) would otherwise fail a
	// sandbox whose resources are each usable on their own host.
	if err := m.triggerMeshPeering(ctx, sb, agentID); err != nil {
		code, summary := diagnostics.DescribeError(err)
		slog.Default().Warn("mesh peering failed; sandbox resources on different hosts cannot reach each other",
			"sandbox_id", sb.ID, "agent_id", agentID, "operation", "mesh_peering",
			"error_code", code, "error_summary", summary)
	}
	return nil
}

// maxCIDRProposals bounds the propose/refuse loop in
// createSegmentWithCIDR. Each refusal means a range this host can't use, so
// the cap only matters on a host whose existing networks happen to blanket
// a long run of the base range; failing loudly after a bounded number of
// attempts is better than looping over all 8192 blocks one round trip at a
// time.
const maxCIDRProposals = 8

// createSegmentWithCIDR allocates a globally-unique range for the segment
// and creates it, re-proposing if the host refuses the range as locally
// conflicting.
//
// The split exists because neither side can allocate alone (#370). Only the
// server sees every host, so only it can pick a range no *other* host is
// using -- which is what cross-host mesh peering requires, since WireGuard
// drops decrypted traffic whose source isn't in the peer's allowed range.
// But only the agent can see what else already occupies that range on its
// own machine: docker0, a VPN, the corporate LAN. So the server proposes
// from the global view, the agent vetoes on local knowledge, and the server
// proposes again.
//
// In-use ranges come from the sandboxes themselves rather than a separate
// allocation ledger: a ledger has to be kept in sync and can drift from the
// segments that actually exist, while a derived view cannot, and releasing
// a range falls out of deleting the sandbox instead of being a thing to
// remember. The sandbox being built is included via sb's own segments,
// which matters for a sandbox spanning hosts -- its second segment must not
// reuse its first one's range.
func (m *Manager) createSegmentWithCIDR(
	ctx context.Context,
	isolator NetworkIsolatingAllocator,
	sb *model.Sandbox,
	pool model.Pool,
	res model.Resource,
) (providersdk.SegmentRef, providersdk.Type, string, error) {
	inUse, err := m.allocatedSegmentCIDRs(ctx, sb)
	if err != nil {
		return "", "", "", err
	}

	allocator := segmentcidr.NewDefault()
	var refused []string
	for attempt := 0; attempt < maxCIDRProposals; attempt++ {
		cidr, err := allocator.Allocate(inUse, refused)
		if err != nil {
			return "", "", "", err
		}
		ref, createdType, authoritativeCIDR, err := isolator.CreateSegment(ctx, pool, res, sb.ID, cidr)
		var conflict *providersdk.CIDRConflictError
		if errors.As(err, &conflict) {
			slog.Info("segment CIDR refused by host, proposing another",
				"component", "sandbox", "operation", "segment_cidr_conflict",
				"resource", string(res.ID), "agent", res.Provider.AgentID,
				"error_summary", conflict.Error())
			refused = append(refused, cidr)
			continue
		}
		if err != nil {
			return "", "", "", err
		}
		// authoritativeCIDR, not cidr: on the idempotent repeat path (a
		// retry against an already-created segment) the driver ignores this
		// proposal and returns the segment's real, already-assigned range.
		// Persisting the proposal instead would record a range nothing
		// actually holds while the real one goes untracked by
		// allocatedSegmentCIDRs -- reopening the collision this mechanism
		// exists to close (#370).
		return ref, createdType, authoritativeCIDR, nil
	}
	return "", "", "", fmt.Errorf(
		"no usable segment CIDR after %d proposals (host refused: %v)",
		maxCIDRProposals, refused)
}

// allocatedSegmentCIDRs returns every segment range currently recorded
// across all sandboxes, including the one being built.
//
// current is passed separately because it may not be persisted yet: a
// sandbox spanning two hosts creates its second segment while the first is
// still only in memory on this code path, and reusing that first range for
// the second host is precisely the collision this whole change exists to
// prevent.
func (m *Manager) allocatedSegmentCIDRs(ctx context.Context, current *model.Sandbox) ([]string, error) {
	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes to find allocated segment CIDRs: %w", err)
	}
	var inUse []string
	for _, sb := range sandboxes {
		if current != nil && sb.ID == current.ID {
			continue // superseded by current's in-memory state below
		}
		for _, seg := range sb.NetworkSegments {
			if seg.CIDR != "" {
				inUse = append(inUse, seg.CIDR)
			}
		}
	}
	if current != nil {
		for _, seg := range current.NetworkSegments {
			if seg.CIDR != "" {
				inUse = append(inUse, seg.CIDR)
			}
		}
	}
	return inUse, nil
}

// triggerMeshPeering peers the sandbox's newly-added agent (identified by
// newAgentID) with every agent already in sb.NetworkSegments -- full mesh,
// pairwise. A no-op if m.allocator doesn't support MeshPeeringAllocator, or
// if this is the sandbox's first (and so far only) segment (nothing to peer
// with yet) -- so a single-host sandbox never reaches the mesh path at all.
func (m *Manager) triggerMeshPeering(ctx context.Context, sb *model.Sandbox, newAgentID string) error {
	peerer, ok := m.allocator.(MeshPeeringAllocator)
	if !ok {
		return nil
	}
	if len(sb.NetworkSegments) < 2 {
		return nil
	}
	var newRef providersdk.SegmentRef
	var newProviderType providersdk.Type
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == newAgentID {
			newRef = providersdk.SegmentRef(seg.Ref)
			newProviderType = providersdk.Type(seg.ProviderType)
			break
		}
	}
	newPub, newEndpoint, newCIDR, err := peerer.MeshIdentity(ctx, newProviderType, newAgentID, newRef)
	if err != nil {
		return err
	}
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == newAgentID {
			continue
		}
		existingProviderType := providersdk.Type(seg.ProviderType)
		existingPub, existingEndpoint, existingCIDR, err := peerer.MeshIdentity(ctx, existingProviderType, seg.AgentID, providersdk.SegmentRef(seg.Ref))
		if err != nil {
			return err
		}
		if err := peerer.AddMeshPeer(ctx, existingProviderType, seg.AgentID, providersdk.SegmentRef(seg.Ref), newPub, newEndpoint, newCIDR); err != nil {
			return err
		}
		if err := peerer.AddMeshPeer(ctx, newProviderType, newAgentID, newRef, existingPub, existingEndpoint, existingCIDR); err != nil {
			return err
		}
	}
	return nil
}

func wrapResources(rs []model.Resource) []keyedResource {
	out := make([]keyedResource, 0, len(rs))
	for _, r := range rs {
		out = append(out, keyedResource{r})
	}
	return out
}

func unwrapResources(rs []keyedResource) []model.Resource {
	out := make([]model.Resource, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Resource)
	}
	return out
}
