package pool

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/policycontroller"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// ReconcileAgent audits one agent's actual resources against the store's
// belief, closing the leak window described in #133: a dropped Create whose
// remote side actually succeeded leaves a resource the store never learns
// about. It uses pkg/policycontroller.Controller, the same Observe->Decide->
// Act shape internal/pool/manager.go already uses for pool inventory
// reconciliation — a second consumer of that package rather than a new
// abstraction.
//
// Deliberately scoped to two outcomes, not three: this cycle only implements
// providersdk.ResourceLister for the docker driver (see #133's PR
// description), and there is no existing convention anywhere in this
// codebase for mapping a driver-native ResourceStatus.State string (e.g.
// docker's "running"/"exited") onto model.ResourceState — inventing one here
// would be unscoped guesswork. So this only adopts orphans and reaps
// confirmed-gone resources; syncing state on resources both sides already
// agree exist is left alone.
//
// Runs on every successful registration, not just reconnects — even a
// brand-new agent identity can have pre-existing boxy-tagged resources from
// a prior life (e.g. process restarted with a fresh cert).
func ReconcileAgent(ctx context.Context, st store.Store, registry *AgentRegistry, agentID string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	ctrl := policycontroller.Controller[reconcileObserved, reconcilePlan]{
		Observer:  reconcileObserver(st, registry, agentID, logger),
		Evaluator: reconcileEvaluator(),
		Actuator:  reconcileActuator(st, logger),
		Logger:    logger,
	}
	_, err := ctrl.Reconcile(ctx)
	return err
}

// RunAgentReconciliation runs ReconcileAgent's Observe/Decide/Act cycle
// immediately, then repeatedly on interval, until ctx is cancelled —
// defense-in-depth for orphans #174's inline Create-failure handling can't
// see (e.g. an agent crash between New-VM succeeding and the failure branch
// running). Each pass is bounded by passTimeout, applied on top of (never
// beyond) ctx. A failed pass is logged and skipped rather than ending the
// loop, matching the previous one-shot call's guarantee that reconciliation
// trouble must never take down agent connectivity. interval is expected to
// be the connection's own heartbeat interval (see
// internal/agentserver/server.go) rather than a new standalone constant.
func RunAgentReconciliation(ctx context.Context, st store.Store, registry *AgentRegistry, agentID string, interval, passTimeout time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = defaultReconciliationInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		pctx, cancel := context.WithTimeout(ctx, passTimeout)
		if err := ReconcileAgent(pctx, st, registry, agentID, logger); err != nil {
			logger.Warn("periodic reconciliation failed", "agent_id", agentID, "error", err)
		}
		cancel()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// defaultReconciliationInterval is RunAgentReconciliation's fallback when no
// interval is supplied (interval <= 0) — production callers pass the
// connection's own heartbeat interval instead.
const defaultReconciliationInterval = 15 * time.Second

// DefaultReconciliationPassTimeout bounds a single ReconcileAgent pass
// inside RunAgentReconciliation so a slow or hung List call can't run
// forever; each pass is best-effort and logs rather than fails the caller
// either way. Shared by every RunAgentReconciliation caller — currently
// internal/agentserver (one goroutine per connected remote agent) and
// internal/cli's `boxy serve` (one goroutine for the embedded agent, so the
// in-process deployment topology gets the same #174 defense-in-depth sweep
// a remote agent gets on every connection).
const DefaultReconciliationPassTimeout = 30 * time.Second

// remoteEntry pairs a driver-reported resource with the provider type it
// came from, since providersdk.ResourceStatus itself carries no provider
// identity.
type remoteEntry struct {
	provider providersdk.Type
	status   providersdk.ResourceStatus
}

type reconcileObserved struct {
	agentID string
	tracked []model.Resource
	remote  map[model.ResourceID]remoteEntry
	// listedProviders holds, for every provider type successfully
	// enumerated this pass, how many remote entries came back. A provider
	// type absent from this map means List failed or is unsupported for it
	// this pass — never trust an absence of remote data as "confirmed
	// gone" for that provider type.
	listedProviders map[providersdk.Type]int
	now             time.Time
}

type reconcilePlan struct {
	adopt  []model.Resource
	reap   []model.ResourceID
	reason string
}

func reconcileObserver(st store.Store, registry *AgentRegistry, agentID string, logger *slog.Logger) policycontroller.ObserverFunc[reconcileObserved] {
	return func(ctx context.Context) (reconcileObserved, error) {
		agent, ok := registry.Get(agentID)
		if !ok {
			return reconcileObserved{}, fmt.Errorf("reconcile agent %q: not registered", agentID)
		}

		// Hold the same per-agent lock Manager's provision actuator holds
		// around its store write (see ProvisionLocker), across both reads
		// below — the driver's List() and the store's ListResources().
		// Without this, a resource whose Create() already returned but
		// whose store write hasn't landed yet can be seen here via List()
		// while still missing from tracked, and gets permanently
		// misclassified as an orphan to adopt. See AgentRegistry.LockProvisioning's
		// doc comment for the full mechanism.
		release := registry.LockProvisioning(agentID)
		defer release()

		remote := make(map[model.ResourceID]remoteEntry)
		listed := make(map[providersdk.Type]int)

		lister, ok := agent.(agentsdk.ResourceListingAgent)
		if !ok {
			logger.Warn("reconciliation: agent does not support listing, skipping audit", "agent_id", agentID)
		} else {
			for _, provider := range agent.Info().Providers {
				statuses, err := lister.List(ctx, provider)
				if err != nil {
					// Unsupported driver and a transient failure both land
					// here, deliberately indistinguishable (see
					// pkg/agentsdk.RemoteAgent.List) — either way this
					// pass can't trust data for this provider type.
					logger.Warn("reconciliation: list failed, skipping audit for this provider type",
						"agent_id", agentID, "provider", provider, "error", err)
					continue
				}
				listed[provider] = len(statuses)
				for _, s := range statuses {
					remote[model.ResourceID(s.ID)] = remoteEntry{provider: provider, status: s}
				}
			}
		}

		all, err := st.ListResources(ctx)
		if err != nil {
			return reconcileObserved{}, fmt.Errorf("reconcile agent %q: list resources: %w", agentID, err)
		}
		tracked := make([]model.Resource, 0, len(all))
		for _, res := range all {
			if res.Provider.AgentID == agentID {
				tracked = append(tracked, res)
			}
		}

		return reconcileObserved{
			agentID:         agentID,
			tracked:         tracked,
			remote:          remote,
			listedProviders: listed,
			now:             time.Now().UTC(),
		}, nil
	}
}

func reconcileEvaluator() policycontroller.EvaluatorFunc[reconcileObserved, reconcilePlan] {
	return func(_ context.Context, obs reconcileObserved) (policycontroller.Decision[reconcilePlan], error) {
		trackedIDs := make(map[model.ResourceID]struct{}, len(obs.tracked))
		trackedCountByProvider := make(map[providersdk.Type]int)
		for _, res := range obs.tracked {
			trackedIDs[res.ID] = struct{}{}
			trackedCountByProvider[providersdk.Type(res.Provider.Name)]++
		}

		var reap []model.ResourceID
		for _, res := range obs.tracked {
			provider := providersdk.Type(res.Provider.Name)
			remoteCount, listedThisPass := obs.listedProviders[provider]
			if !listedThisPass {
				continue
			}
			// Safety valve: a provider type that came back completely
			// empty while the store tracks resources under it is
			// suspicious enough to not trust for reaping this pass, even
			// though List returned no error. Defense in depth against a
			// future driver bug that returns an empty result instead of
			// an error on partial failure.
			if remoteCount == 0 && trackedCountByProvider[provider] > 0 {
				continue
			}
			if _, stillThere := obs.remote[res.ID]; !stillThere {
				reap = append(reap, res.ID)
			}
		}

		var adopt []model.Resource
		for id, entry := range obs.remote {
			if _, known := trackedIDs[id]; known {
				continue
			}
			adopt = append(adopt, model.Resource{
				ID:       id,
				Type:     model.ResourceTypeUnknown,
				Provider: model.ProviderRef{Name: string(entry.provider), AgentID: obs.agentID},
				State:    model.ResourceStateUnknown,
				Properties: map[string]any{
					"reconciled_driver_state": entry.status.State,
				},
				CreatedAt: obs.now,
				UpdatedAt: obs.now,
			})
		}

		reason := fmt.Sprintf("agent=%s adopt=%d reap=%d", obs.agentID, len(adopt), len(reap))
		return policycontroller.Decision[reconcilePlan]{
			ShouldAct: len(adopt) > 0 || len(reap) > 0,
			Plan:      reconcilePlan{adopt: adopt, reap: reap, reason: reason},
			Reason:    reason,
		}, nil
	}
}

func reconcileActuator(st store.Store, logger *slog.Logger) policycontroller.ActuatorFunc[reconcilePlan] {
	return func(ctx context.Context, plan reconcilePlan) error {
		for _, res := range plan.adopt {
			logger.Warn("reconciliation: adopting orphaned resource",
				"agent_id", res.Provider.AgentID, "resource_id", res.ID, "provider", res.Provider.Name)
			if err := st.PutResource(ctx, res); err != nil {
				return fmt.Errorf("adopt resource %q: %w", res.ID, err)
			}
		}
		for _, id := range plan.reap {
			res, err := st.GetResource(ctx, id)
			if err != nil {
				return fmt.Errorf("reap resource %q: get: %w", id, err)
			}
			res.State = model.ResourceStateDestroyed
			res.UpdatedAt = time.Now().UTC()
			logger.Warn("reconciliation: marking resource destroyed, agent no longer reports it",
				"agent_id", res.Provider.AgentID, "resource_id", id)
			if err := st.PutResource(ctx, res); err != nil {
				return fmt.Errorf("reap resource %q: put: %w", id, err)
			}
		}
		return nil
	}
}
