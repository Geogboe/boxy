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
