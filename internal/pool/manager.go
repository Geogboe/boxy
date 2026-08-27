package pool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/policycontroller"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type MaxTotalReachedError struct {
	PoolName       model.PoolName
	MaxTotal       int
	CurrentTotal   int
	ReadyCount     int
	RequestedReady int
}

func (e *MaxTotalReachedError) Error() string {
	return fmt.Sprintf(
		"pool %q is at max_total %d (%d total, %d ready), cannot satisfy requested ready count %d",
		e.PoolName,
		e.MaxTotal,
		e.CurrentTotal,
		e.ReadyCount,
		e.RequestedReady,
	)
}

type DrainedPoolError struct {
	PoolName       model.PoolName
	RequestedReady int
}

func (e *DrainedPoolError) Error() string {
	return fmt.Sprintf("pool %q is drained; cannot satisfy requested ready count %d", e.PoolName, e.RequestedReady)
}

type ConfigDeclaredDrainError struct {
	PoolName model.PoolName
}

func (e *ConfigDeclaredDrainError) Error() string {
	return fmt.Sprintf("pool %q is configured drained; edit config before filling it", e.PoolName)
}

// provisionBackoffState tracks repeated provisioning failures for a pool so
// the background reconcile loop can stop hammering a provider/host that's
// already failing (e.g. a degraded Hyper-V host, see #118) instead of
// retrying every tick forever.
type provisionBackoffState struct {
	failCount   int
	nextAttempt time.Time
}

const (
	provisionBackoffBase = 10 * time.Second
	provisionBackoffMax  = 5 * time.Minute
)

// ProvisionLocker serializes a driver's Create-then-store-write sequence
// against a concurrent reconciliation sweep's List()-then-store-read
// sequence for the same agent, closing a race where a resource visible
// through a driver's ResourceLister isn't yet visible through the store —
// see AgentRegistry.LockProvisioning's doc comment for the full mechanism
// and #181's design spec, "Follow-ups", for how it was found. AgentRegistry
// implements this; Manager and ReconcileAgent must share the same instance.
type ProvisionLocker interface {
	LockProvisioning(agentID string) func()
}

// Manager reconciles a pool's inventory against its policies.
type Manager struct {
	store           store.Store
	provisioner     Provisioner
	admission       AdmissionPublisher
	guestSecrets    boxysecrets.Store
	provisionLocker ProvisionLocker
	clock           Clock
	locksMu         sync.Mutex
	poolLocks       map[model.PoolName]*sync.Mutex

	backoffMu sync.Mutex
	backoff   map[model.PoolName]*provisionBackoffState
}

// SetAdmissionPublisher enables asynchronous resource admission. It is
// optional so existing embedders and unit tests retain the synchronous
// provisioner behavior until they opt into the durable lifecycle queue.
func (m *Manager) SetAdmissionPublisher(publisher AdmissionPublisher) {
	if m != nil {
		m.admission = publisher
	}
}

// SetGuestSecretStore enables best-effort cleanup of resource-scoped guest
// credentials when a resource is destroyed or force-orphaned before it is
// allocated. Allocation cleanup remains in AgentProvisioner because it owns
// the successful consumption point.
func (m *Manager) SetGuestSecretStore(secrets boxysecrets.Store) {
	if m != nil {
		m.guestSecrets = secrets
	}
}

// SetProvisionLocker enables per-agent mutual exclusion between this
// Manager's provisioning and a concurrent ReconcileAgent sweep for the same
// agent (see ProvisionLocker). Optional: nil is a safe default for
// embedders and unit tests that don't run agent-backed reconciliation
// concurrently with provisioning.
func (m *Manager) SetProvisionLocker(locker ProvisionLocker) {
	if m != nil {
		m.provisionLocker = locker
	}
}

// lockProvisioning acquires the ProvisionLocker for agentID if one is
// configured and agentID is non-empty (DriverProvisioner's resources have
// no agent concept and are never subject to ReconcileAgent's sweep, so
// there's nothing to serialize against). Always returns a valid release
// func, even as a no-op, so callers can call it unconditionally.
func (m *Manager) lockProvisioning(agentID string) func() {
	if m.provisionLocker == nil || agentID == "" {
		return func() {}
	}
	return m.provisionLocker.LockProvisioning(agentID)
}

// FailAdmission marks a resource whose pool-admission action cannot safely be
// retried in place. The next reconciliation pass quarantines and destroys it,
// while the existing capped provisioning backoff controls replacement.
func (m *Manager) FailAdmission(ctx context.Context, res model.Resource, cause error) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("pool manager store is required")
	}
	if res.Properties == nil {
		res.Properties = make(map[string]any)
	}
	res.Properties["lifecycle_error"] = cause.Error()
	res.State = model.ResourceStateError
	res.UpdatedAt = m.clock.Now().UTC()
	m.recordProvisionFailure(res.OriginPool, res.UpdatedAt)
	return m.store.PutResource(ctx, res)
}

// provisionBackoffActive reports whether pool provisioning is currently in
// a backoff window following repeated failures.
func (m *Manager) provisionBackoffActive(poolName model.PoolName, now time.Time) bool {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	st := m.backoff[poolName]
	if st == nil {
		return false
	}
	return now.Before(st.nextAttempt)
}

// recordProvisionFailure increments the pool's failure count and schedules
// the next allowed provisioning attempt using capped exponential backoff.
func (m *Manager) recordProvisionFailure(poolName model.PoolName, now time.Time) {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	if m.backoff == nil {
		m.backoff = make(map[model.PoolName]*provisionBackoffState)
	}
	st := m.backoff[poolName]
	if st == nil {
		st = &provisionBackoffState{}
		m.backoff[poolName] = st
	}
	st.failCount++
	delay := provisionBackoffBase
	for i := 1; i < st.failCount && delay < provisionBackoffMax; i++ {
		delay *= 2
	}
	if delay > provisionBackoffMax {
		delay = provisionBackoffMax
	}
	st.nextAttempt = now.Add(delay)
}

// recordProvisionSuccess clears any backoff state for the pool.
func (m *Manager) recordProvisionSuccess(poolName model.PoolName) {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	delete(m.backoff, poolName)
}

func New(s store.Store, p Provisioner) *Manager {
	return &Manager{
		store:       s,
		provisioner: p,
		clock:       realClock{},
		poolLocks:   make(map[model.PoolName]*sync.Mutex),
	}
}

func (m *Manager) SetClock(c Clock) {
	if c != nil {
		m.clock = c
	}
}

// Reconcile performs one reconciliation pass for the pool.
func (m *Manager) Reconcile(ctx context.Context, poolName model.PoolName) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	unlock := m.lockPool(poolName)
	defer unlock()
	return m.reconcileLocked(ctx, poolName, 0, false)
}

// EnsureReady ensures the pool has at least minReady resources available,
// without mutating the pool's configured preheat policy.
func (m *Manager) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	if minReady <= 0 {
		return nil
	}
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	unlock := m.lockPool(poolName)
	defer unlock()
	return m.reconcileLocked(ctx, poolName, minReady, true)
}

// Drain persists an operator drain override and immediately destroys unused ready inventory.
func (m *Manager) Drain(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	if m == nil {
		return model.Pool{}, fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return model.Pool{}, fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return model.Pool{}, fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return model.Pool{}, fmt.Errorf("pool name is required")
	}
	unlock := m.lockPool(poolName)
	defer unlock()

	p, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Pool{}, fmt.Errorf("get pool: %w", err)
	}
	p.Drain.Operator = true
	if err := m.store.PutPool(ctx, p); err != nil {
		return model.Pool{}, fmt.Errorf("put pool drain override: %w", err)
	}
	if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
		return model.Pool{}, err
	}
	return m.store.GetPool(ctx, poolName)
}

// Fill clears an operator drain override and immediately reconciles configured capacity.
func (m *Manager) Fill(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	if m == nil {
		return model.Pool{}, fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return model.Pool{}, fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return model.Pool{}, fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return model.Pool{}, fmt.Errorf("pool name is required")
	}
	unlock := m.lockPool(poolName)
	defer unlock()

	p, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Pool{}, fmt.Errorf("get pool: %w", err)
	}
	p.Drain.Operator = false
	if err := m.store.PutPool(ctx, p); err != nil {
		return model.Pool{}, fmt.Errorf("clear pool drain override: %w", err)
	}
	if p.Drain.ConfigDeclared {
		if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
			return model.Pool{}, err
		}
		updated, err := m.store.GetPool(ctx, poolName)
		if err != nil {
			return model.Pool{}, fmt.Errorf("get pool after config-declared drain fill: %w", err)
		}
		return updated, &ConfigDeclaredDrainError{PoolName: poolName}
	}
	if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
		return model.Pool{}, err
	}
	return m.store.GetPool(ctx, poolName)
}

// DestroyResource tears down a tracked resource through its origin pool's
// provider lifecycle and removes Boxy's resource record. Resources are
// single-use; this never returns resources to ready inventory.
func (m *Manager) DestroyResource(ctx context.Context, res model.Resource) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return fmt.Errorf("provisioner is nil")
	}
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	if res.OriginPool == "" {
		return fmt.Errorf("resource %q has no origin pool", res.ID)
	}

	unlock := m.lockPool(res.OriginPool)
	defer unlock()

	p, err := m.store.GetPool(ctx, res.OriginPool)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("origin pool %q for resource %q not found", res.OriginPool, res.ID)
	}
	if err != nil {
		return fmt.Errorf("get origin pool %q for resource %q: %w", res.OriginPool, res.ID, err)
	}

	if err := m.destroyAndMark(ctx, p, res, model.ResourceStateDestroying, m.clock.Now()); err != nil {
		return err
	}
	p.Inventory.Resources = removeInventoryResource(p.Inventory.Resources, res.ID)
	if err := m.store.PutPool(ctx, p); err != nil {
		return fmt.Errorf("put pool %q after destroying resource %q: %w", p.Name, res.ID, err)
	}
	if err := m.store.DeleteResource(ctx, res.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete resource %q: %w", res.ID, err)
	}
	m.deleteResourceGuestCredential(ctx, res.ID)
	return nil
}

// ForceOrphanResource detaches res from Boxy's bookkeeping (pool inventory +
// store) without ever contacting res's owning agent. Unlike DestroyResource,
// it never transitions the resource through a Destroying state and never
// calls m.provisioner.Destroy — the whole point is that the owning agent is
// permanently gone and cannot be reached. The provisioner must implement
// ForceOrphaner, which itself refuses unless the agent has already been
// deregistered (see AgentProvisioner.ForceOrphan) — callers should only
// reach this after an explicit `boxy agent revoke`.
func (m *Manager) ForceOrphanResource(ctx context.Context, res model.Resource, reason string) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return fmt.Errorf("provisioner is nil")
	}
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	if res.OriginPool == "" {
		return fmt.Errorf("resource %q has no origin pool", res.ID)
	}

	fo, ok := m.provisioner.(ForceOrphaner)
	if !ok {
		return fmt.Errorf("provisioner does not support force-orphan")
	}

	unlock := m.lockPool(res.OriginPool)
	defer unlock()

	p, err := m.store.GetPool(ctx, res.OriginPool)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("origin pool %q for resource %q not found", res.OriginPool, res.ID)
	}
	if err != nil {
		return fmt.Errorf("get origin pool %q for resource %q: %w", res.OriginPool, res.ID, err)
	}

	if err := fo.ForceOrphan(ctx, res); err != nil {
		return err
	}
	slog.Warn("resource force-orphaned", "resource_id", res.ID, "agent_id", res.Provider.AgentID, "origin_pool", res.OriginPool, "reason", reason)

	p.Inventory.Resources = removeInventoryResource(p.Inventory.Resources, res.ID)
	if err := m.store.PutPool(ctx, p); err != nil {
		return fmt.Errorf("put pool %q after force-orphaning resource %q: %w", p.Name, res.ID, err)
	}
	if err := m.store.DeleteResource(ctx, res.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete resource %q: %w", res.ID, err)
	}
	m.deleteResourceGuestCredential(ctx, res.ID)
	return nil
}

// ForceOrphanAgentResources force-orphans every resource currently
// attributed to agentID. Intended to run immediately after the agent has
// been deregistered (see internal/agentserver.Server.Revoke). Returns the
// count force-orphaned. A per-resource failure (e.g. a resource adopted by
// pool.ReconcileAgent with no OriginPool — see #133 — which
// ForceOrphanResource, like DestroyResource, cannot act on) does not abort
// the sweep: every other resource still gets a chance, since one
// problem resource must never block cleanup of every other resource this
// permanently-gone agent left behind. All per-resource errors are joined
// and returned alongside the count that did succeed, so the caller can
// log/report partial progress rather than losing it silently.
func (m *Manager) ForceOrphanAgentResources(ctx context.Context, agentID, reason string) (int, error) {
	all, err := m.store.ListResources(ctx)
	if err != nil {
		return 0, fmt.Errorf("list resources: %w", err)
	}
	var n int
	var errs []error
	for _, res := range all {
		if res.Provider.AgentID != agentID {
			continue
		}
		if err := m.ForceOrphanResource(ctx, res, reason); err != nil {
			errs = append(errs, fmt.Errorf("force-orphan resource %q: %w", res.ID, err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// isTransientDestroyState reports whether a resource is already mid-teardown
// (recycling or destroying), so a caller that's about to retry a destroy
// (e.g. the orphan sweep) doesn't need to re-persist the same transient
// state before calling the provisioner again.
func isTransientDestroyState(s model.ResourceState) bool {
	return s == model.ResourceStateRecycling || s == model.ResourceStateDestroying
}

// destroyAndMark is the shared "mark transient (if not already), call the
// provisioner, mark destroyed" sequence used by DestroyResource, the
// stale/recycle loop, and applyDrain — the three destroy call sites that all
// need the same pre-destroy observability write. Extracted after the three
// hand-written copies drifted (applyDrain's copy was missing the UpdatedAt
// stamp the other two had). now is the timestamp for both writes; callers
// mid-reconcile-tick should pass a single consistent value (e.g. pl.now)
// rather than re-querying the clock per resource.
func (m *Manager) destroyAndMark(ctx context.Context, p model.Pool, res model.Resource, transient model.ResourceState, now time.Time) error {
	if !isTransientDestroyState(res.State) {
		res.State = transient
		res.UpdatedAt = now
		if err := m.store.PutResource(ctx, res); err != nil {
			return fmt.Errorf("mark resource %q %s: %w", res.ID, transient, err)
		}
	}
	if err := m.provisioner.Destroy(ctx, p, res); err != nil {
		return fmt.Errorf("destroy resource %q in pool %q: %w", res.ID, p.Name, err)
	}
	res.State = model.ResourceStateDestroyed
	res.UpdatedAt = now
	if err := m.store.PutResource(ctx, res); err != nil {
		return fmt.Errorf("mark resource %q destroyed: %w", res.ID, err)
	}
	m.deleteResourceGuestCredential(ctx, res.ID)
	return nil
}

func (m *Manager) deleteResourceGuestCredential(ctx context.Context, resourceID model.ResourceID) {
	if m == nil || m.guestSecrets == nil || resourceID == "" {
		return
	}
	if err := m.guestSecrets.Delete(ctx, boxysecrets.ResourceCredentialKey(string(resourceID))); err != nil && !errors.Is(err, boxysecrets.ErrNotFound) {
		slog.Warn("could not remove destroyed resource guest credential", "resource_id", resourceID, "error", err)
	}
}

func (m *Manager) lockPool(poolName model.PoolName) func() {
	m.locksMu.Lock()
	if m.poolLocks == nil {
		m.poolLocks = make(map[model.PoolName]*sync.Mutex)
	}
	lock := m.poolLocks[poolName]
	if lock == nil {
		lock = &sync.Mutex{}
		m.poolLocks[poolName] = lock
	}
	m.locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (m *Manager) reconcileLocked(ctx context.Context, poolName model.PoolName, minReadyOverride int, requireMinReady bool) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}

	type observed struct {
		pool      model.Pool
		resources []model.Resource
		now       time.Time
	}
	type plan struct {
		pool             model.Pool
		stale            []model.Resource
		drainResources   []model.Resource
		drain            bool
		now              time.Time
		toProvision      int
		inventoryChanged bool
		reason           string
	}

	ctrl := policycontroller.Controller[observed, plan]{
		Observer: policycontroller.ObserverFunc[observed](func(ctx context.Context) (observed, error) {
			p, err := m.store.GetPool(ctx, poolName)
			if err != nil {
				return observed{}, fmt.Errorf("get pool: %w", err)
			}
			resources, err := m.store.ListResources(ctx)
			if err != nil {
				return observed{}, fmt.Errorf("list resources: %w", err)
			}
			if m.admission != nil {
				for _, res := range resources {
					if res.OriginPool != poolName || res.State != model.ResourceStateProvisioning {
						continue
					}
					if err := m.admission.PublishResourceProvisioned(ctx, res); err != nil {
						return observed{}, fmt.Errorf("publish admission event for resource %q: %w", res.ID, err)
					}
				}
			}
			return observed{pool: p, resources: resources, now: m.clock.Now()}, nil
		}),
		Evaluator: policycontroller.EvaluatorFunc[observed, plan](func(ctx context.Context, obs observed) (policycontroller.Decision[plan], error) {
			_ = ctx
			p := obs.pool
			fallbackInventoryIDs := resourceIDSet(p.Inventory.Resources)
			rebuilt, rebuildReport, err := RebuildReadyInventory(p, obs.resources, p.Inventory.Resources)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			p = rebuilt

			// Resources left mid-teardown by a reconcile pass that crashed
			// before the destroy completed drop out of p.Inventory.Resources
			// on rebuild (it only re-admits Ready resources), so neither
			// computeStale nor the drain path below would ever see them
			// again without an explicit sweep — they'd zombie forever, still
			// counted against MaxTotal, with their backing provider resource
			// possibly never torn down.
			//
			// Both branches sweep both transient states. A resource marked
			// "destroying" is not always sandbox-owned: applyDrain uses the
			// same state for pool-owned resources, and a pool can be
			// un-drained (fill) after a crashed drain leaves one orphaned —
			// at that point only this stale/recycle path ever runs again for
			// that pool, so it must be able to recover it too. This does
			// mean a sandbox-owned "destroying" orphan could occasionally be
			// retried redundantly alongside sandbox.DeletionReconciler's own
			// retry of the same resource; that's safe (not just tolerated)
			// because every driver's Delete (devfactory/docker/hyperv) is
			// idempotent on an already-gone resource, and the window closes
			// as soon as either retry succeeds and deletes the record.
			inInventory := resourceIDSet(p.Inventory.Resources)
			orphans := orphanedTransientResources(p.Name, obs.resources, inInventory, fallbackInventoryIDs)
			quarantined := quarantinedOrphans(p.Name, obs.resources)

			if p.EffectivelyDrained() {
				if requireMinReady {
					return policycontroller.Decision[plan]{}, &DrainedPoolError{
						PoolName:       p.Name,
						RequestedReady: minReadyOverride,
					}
				}
				toDrain := append([]model.Resource(nil), p.Inventory.Resources...)
				toDrain = append(toDrain, orphans...)
				toDrain = append(toDrain, quarantined...)
				reason := fmt.Sprintf("drain inventory_rebuilt=%t ready=%d orphans=%d quarantined=%d", rebuildReport.Changed, len(p.Inventory.Resources), len(orphans), len(quarantined))
				return policycontroller.Decision[plan]{
					ShouldAct: rebuildReport.Changed || len(toDrain) > 0,
					Plan: plan{
						pool:             p,
						drainResources:   toDrain,
						drain:            true,
						now:              obs.now,
						inventoryChanged: rebuildReport.Changed,
						reason:           reason,
					},
					Reason: reason,
				}, nil
			}

			stale, kept, err := computeStale(p, obs.now)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			stale = append(stale, orphans...)
			stale = append(stale, quarantined...)
			p.Inventory.Resources = kept

			readyCount := countReadyResources(p.Inventory.Resources)
			staleIDs := resourceIDSet(stale)
			totalCount := countTrackedResources(p.Name, obs.resources, p.Inventory.Resources, staleIDs)

			// Admission is gated on what the caller actually asked for
			// (minReadyOverride), never on the policy's configured
			// Preheat.MinReady -- that value sizes background preheat
			// provisioning only, below, and must never be substituted
			// here even by a future edit to this block. See #240.
			if requireMinReady {
				if capErr := maxTotalShortfall(p.Name, p.Policies.Preheat.MaxTotal, totalCount, readyCount, minReadyOverride); capErr != nil {
					return policycontroller.Decision[plan]{}, capErr
				}
			}

			// effectiveMinReady is the background preheat provisioning
			// target: the caller's own floor widened up to the pool's
			// configured min_ready. It must only ever feed
			// computeToProvision below, never the admission check above.
			effectiveMinReady := max(minReadyOverride, p.Policies.Preheat.MinReady)
			toProv := computeToProvision(p, effectiveMinReady, totalCount)
			// Background reconcile passes (requireMinReady=false) respect
			// provisioning backoff so a failing provider/host isn't hammered
			// every tick. Explicit EnsureReady calls always attempt, since
			// those are user/allocation-triggered and deserve a live answer.
			backoffActive := !requireMinReady && toProv > 0 && m.provisionBackoffActive(p.Name, obs.now)
			if backoffActive {
				toProv = 0
			}

			reason := "noop"
			should := false
			if rebuildReport.Changed || len(stale) > 0 || toProv > 0 {
				should = true
				reason = fmt.Sprintf("inventory_rebuilt=%t stale=%d provision=%d", rebuildReport.Changed, len(stale), toProv)
			} else if backoffActive {
				reason = fmt.Sprintf("provision backoff active for pool %q", p.Name)
			}

			return policycontroller.Decision[plan]{
				ShouldAct: should,
				Plan: plan{
					pool:             p,
					stale:            stale,
					now:              obs.now,
					toProvision:      toProv,
					inventoryChanged: rebuildReport.Changed,
					reason:           reason,
				},
				Reason: reason,
			}, nil
		}),
		Actuator: policycontroller.ActuatorFunc[plan](func(ctx context.Context, pl plan) error {
			p := pl.pool
			if pl.drain {
				return m.applyDrain(ctx, p, pl.drainResources, pl.now)
			}

			for _, res := range pl.stale {
				if err := m.destroyAndMark(ctx, p, res, model.ResourceStateRecycling, pl.now); err != nil {
					return err
				}
			}

			for i := 0; i < pl.toProvision; i++ {
				// persist finishes building whatever resource Create
				// produced (pool-inventory/admission-specific fields) and
				// writes it to the store. Quarantined resources (always
				// State == ResourceStateError) skip the inventory add —
				// they were never part of the pool's usable inventory.
				persist := func(res *model.Resource) error {
					if res.State != model.ResourceStateError {
						if res.OriginPool == "" {
							res.OriginPool = p.Name
						}
						if m.admission != nil {
							res.State = model.ResourceStateProvisioning
							res.UpdatedAt = pl.now
						}
						if err := p.Inventory.Add(*res); err != nil {
							return fmt.Errorf("add resource to pool %q inventory: %w", p.Name, err)
						}
					}
					return m.store.PutResource(ctx, *res)
				}

				var res model.Resource
				var created bool
				var err error
				if lp, ok := m.provisioner.(LockedProvisioner); ok {
					// Preferred path: the per-agent ProvisionLocker lock (if
					// any) is acquired by the provisioner itself around both
					// its Create call and persist above, closing the window
					// the fallback below cannot — a fast ResourceLister
					// driver (e.g. devfactory) can make a resource visible
					// via List() the instant Create returns, before Manager
					// gets control back to acquire anything. See
					// LockedProvisioner's doc comment.
					res, created, err = lp.ProvisionLocked(ctx, p, persist)
				} else {
					// Fallback for a Provisioner that doesn't implement
					// LockedProvisioner (e.g. the deprecated
					// DriverProvisioner, or a test fake) — no per-agent
					// concept to lock around Create itself, so this only
					// closes the narrower window around the store write,
					// same as before LockedProvisioner existed.
					res, err = m.provisioner.Provision(ctx, p)
					created = err == nil
					if err == nil || res.ID != "" {
						release := m.lockProvisioning(res.Provider.AgentID)
						perr := persist(&res)
						release()
						if perr != nil {
							err = perr
						}
					}
				}

				// created (Create itself succeeded), not err (persist may
				// have failed afterward), decides backoff bookkeeping: a
				// persist failure after a successful Create must not count
				// against the pool's provisioning backoff the way a real
				// Create failure does.
				if created {
					m.recordProvisionSuccess(p.Name)
				} else {
					m.recordProvisionFailure(p.Name, pl.now)
				}
				if err != nil {
					return fmt.Errorf("provision resource for pool %q: %w", p.Name, err)
				}
				if m.admission != nil {
					if err := m.admission.PublishResourceProvisioned(ctx, res); err != nil {
						return fmt.Errorf("publish admission event for resource %q: %w", res.ID, err)
					}
				}
			}

			if err := m.store.PutPool(ctx, p); err != nil {
				return fmt.Errorf("put pool: %w", err)
			}
			return nil
		}),
	}

	_, err := ctrl.Reconcile(ctx)
	if err != nil {
		var capErr *MaxTotalReachedError
		if errors.As(err, &capErr) {
			return capErr
		}
		var drainedErr *DrainedPoolError
		if errors.As(err, &drainedErr) {
			return drainedErr
		}
	}
	return err
}

func (m *Manager) applyDrain(ctx context.Context, p model.Pool, resources []model.Resource, now time.Time) error {
	if len(resources) == 0 {
		p.Inventory.Resources = nil
		if err := m.store.PutPool(ctx, p); err != nil {
			return fmt.Errorf("put drained pool: %w", err)
		}
		return nil
	}

	for _, res := range resources {
		if err := m.destroyAndMark(ctx, p, res, model.ResourceStateDestroying, now); err != nil {
			return err
		}
		p.Inventory.Resources = removeInventoryResource(p.Inventory.Resources, res.ID)
		if err := m.store.PutPool(ctx, p); err != nil {
			return fmt.Errorf("put drained pool: %w", err)
		}
		if err := m.store.DeleteResource(ctx, res.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("delete drained resource %q: %w", res.ID, err)
		}
	}
	return nil
}

func removeInventoryResource(resources []model.Resource, id model.ResourceID) []model.Resource {
	out := resources[:0]
	for _, res := range resources {
		if res.ID == id {
			continue
		}
		out = append(out, res)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func computeStale(p model.Pool, now time.Time) (stale []model.Resource, kept []model.Resource, _ error) {
	maxAgeStr := p.Policies.Recycle.MaxAge
	if maxAgeStr == "" {
		return nil, p.Inventory.Resources, nil
	}

	maxAge, err := time.ParseDuration(maxAgeStr)
	if err != nil {
		return nil, nil, fmt.Errorf("pool %q recycle max_age invalid: %w", p.Name, err)
	}
	if maxAge <= 0 {
		return nil, p.Inventory.Resources, nil
	}

	stale, kept = partitionByMaxAge(
		p.Inventory.Resources,
		now,
		maxAge,
		func(res model.Resource) time.Time { return res.CreatedAt },
		func(res model.Resource) time.Time { return res.UpdatedAt },
	)
	return stale, kept, nil
}

// orphanedTransientResources finds resources originating from poolName that
// are already mid-teardown (recycling or destroying) but are absent from the
// pool's rebuilt ready inventory — the signature of a reconcile pass that
// crashed between marking a resource transient and the destroy completing.
// For older state files that predate OriginPool, fallbackInventoryIDs retains
// the pool ownership signal from the embedded inventory that was used during
// the rebuild.
// Callers fold the result into the same tick's stale/drain list so the
// destroy gets retried instead of the resource zombying forever.
//
// Both transient states are always swept, from both the stale/recycle and
// drain branches. A "destroying" resource isn't always sandbox-owned:
// applyDrain uses the same state for pool-owned resources, and a pool can be
// un-drained (fill) after a crashed drain leaves one orphaned — at that
// point only the stale/recycle branch ever runs for that pool again, so it
// must be able to recover a drain-orphaned resource too. This can mean a
// sandbox-owned "destroying" orphan is occasionally retried redundantly
// alongside sandbox.DeletionReconciler's own independent retry of the same
// resource (it re-scans each deleting sandbox's own resource list every
// tick, regardless of pool inventory) — that overlap is safe, not just
// tolerated, because every driver's Delete (devfactory/docker/hyperv) is
// idempotent on an already-gone resource, and the window closes as soon as
// either retry succeeds and deletes the record.
func orphanedTransientResources(
	poolName model.PoolName,
	resources []model.Resource,
	inInventory map[model.ResourceID]struct{},
	fallbackInventoryIDs map[model.ResourceID]struct{},
) []model.Resource {
	var orphans []model.Resource
	for _, res := range resources {
		if !isTransientDestroyState(res.State) {
			continue
		}
		if res.OriginPool != poolName {
			if res.OriginPool != "" {
				continue
			}
			if _, legacyOwned := fallbackInventoryIDs[res.ID]; !legacyOwned {
				continue
			}
		}
		if _, ok := inInventory[res.ID]; ok {
			continue
		}
		orphans = append(orphans, res)
	}
	return orphans
}

// quarantinedOrphans finds resources this pool tried to provision that ended
// up recorded as ResourceStateError — a Create failed and its cleanup also
// failed (see #174, Task 4's Provisioner.Provision handling of
// providersdk.OrphanedResourceError). Unlike orphanedTransientResources,
// these haven't started teardown yet — isTransientDestroyState only covers
// Recycling/Destroying — so this is a separate filter rather than
// broadening that one; see the design spec's "Alternatives considered" for
// why isTransientDestroyState itself isn't touched.
func quarantinedOrphans(poolName model.PoolName, resources []model.Resource) []model.Resource {
	var quarantined []model.Resource
	for _, res := range resources {
		if res.State == model.ResourceStateError && res.OriginPool == poolName {
			quarantined = append(quarantined, res)
		}
	}
	return quarantined
}

func computeToProvision(p model.Pool, minReady int, totalCount int) int {
	readyCount := countReadyResources(p.Inventory.Resources)
	return computeToProvisionCount(
		model.PreheatPolicy{
			MinReady: minReady,
			MaxTotal: p.Policies.Preheat.MaxTotal,
		},
		readyCount,
		totalCount,
	)
}

func countReadyResources(resources []model.Resource) int {
	readyCount := 0
	for _, res := range resources {
		if res.State == model.ResourceStateReady {
			readyCount++
		}
	}
	return readyCount
}

func resourceIDSet(resources []model.Resource) map[model.ResourceID]struct{} {
	ids := make(map[model.ResourceID]struct{}, len(resources))
	for _, res := range resources {
		if res.ID == "" {
			continue
		}
		ids[res.ID] = struct{}{}
	}
	return ids
}

func countTrackedResources(
	poolName model.PoolName,
	resources []model.Resource,
	inventory []model.Resource,
	excludeIDs map[model.ResourceID]struct{},
) int {
	inventoryIDs := resourceIDSet(inventory)
	total := 0
	for _, res := range resources {
		if res.ID == "" {
			continue
		}
		if _, excluded := excludeIDs[res.ID]; excluded {
			continue
		}
		if res.State == model.ResourceStateDestroyed {
			continue
		}
		if res.OriginPool == poolName {
			total++
			continue
		}
		if res.OriginPool == "" {
			if _, ok := inventoryIDs[res.ID]; ok {
				total++
			}
		}
	}
	return total
}

func maxTotalShortfall(
	poolName model.PoolName,
	maxTotal int,
	totalCount int,
	readyCount int,
	requestedReady int,
) error {
	if maxTotal <= 0 || requestedReady <= 0 {
		return nil
	}
	if canSatisfyRequestedReady(
		maxTotal,
		readyCount,
		totalCount,
		requestedReady,
	) {
		return nil
	}
	return &MaxTotalReachedError{
		PoolName:       poolName,
		MaxTotal:       maxTotal,
		CurrentTotal:   totalCount,
		ReadyCount:     readyCount,
		RequestedReady: requestedReady,
	}
}
