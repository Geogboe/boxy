package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
)

// AgentProvisioner adapts agentsdk.Agent instances into the pool.Provisioner
// interface. It routes CRUD operations through an agent, which transparently
// dispatches to the appropriate driver (whether local or remote).
//
// Provision resolves an agent by provider type (optionally pinned via
// spec.Agent) since it's creating a brand new resource. Destroy and
// Allocate operate on an *existing* resource and must instead route back to
// res.Provider.AgentID — the exact agent instance that created it — via
// Registry.Get. Once more than one agent can advertise the same provider
// type, re-resolving by type at Destroy/Allocate time could silently route
// to a different agent than the one that owns the resource; because
// providersdk.Driver.Delete is contractually idempotent for an
// already-missing resource, a misrouted Destroy would report success while
// the resource keeps running, unmanaged, on its real host. See
// docs/adr/0005-remote-agent-transport-and-registration.md.
type AgentProvisioner struct {
	Registry  *AgentRegistry
	Specs     map[model.PoolName]boxyconfig.PoolSpec
	Providers map[string]providersdk.Instance
	Now       func() time.Time
	// GuestSecrets holds the resource-scoped credential produced during pool
	// admission. It is consumed and removed after allocation-time rotation.
	GuestSecrets  boxysecrets.Store
	PackageEngine *resourcepack.Engine
}

// Provision implements pool.Provisioner. It's a thin wrapper around
// ProvisionLocked with no persist callback — Manager calls ProvisionLocked
// directly instead (see LockedProvisioner's doc comment) whenever it can,
// but Provision stays fully functional on its own for any caller (tests,
// or a future Provisioner-only consumer) that only knows the base
// interface.
func (ap *AgentProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	res, _, err := ap.ProvisionLocked(ctx, pool, nil)
	return res, err
}

// ProvisionLocked implements pool.LockedProvisioner. See that interface's
// doc comment for the persist/created contract and why the lock must be
// acquired here rather than by Manager beforehand.
func (ap *AgentProvisioner) ProvisionLocked(ctx context.Context, pool model.Pool, persist func(*model.Resource) error) (model.Resource, bool, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return model.Resource{}, false, fmt.Errorf("unknown pool %q", pool.Name)
	}

	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.Registry.Resolve(driverType, spec.Agent)
	if err != nil {
		return model.Resource{}, false, fmt.Errorf("resolve agent for pool %q: %w", pool.Name, err)
	}

	// Acquired before Create — not after, like the version of this fix that
	// only wrapped the caller's own store write — because a fast
	// ResourceLister driver (e.g. devfactory) can make the resource visible
	// via List() the instant Create returns, before any code has a chance
	// to acquire anything. Held through persist below via defer, so a
	// concurrent ReconcileAgent sweep for this same agent can never
	// observe the driver's List() without also seeing the store write.
	release := ap.Registry.LockProvisioning(agent.Info().ID)
	defer release()

	res, err := agent.Create(ctx, driverType, spec.Config)
	if err != nil {
		wrapped := fmt.Errorf("agent create for pool %q: %w", pool.Name, err)
		var orphanErr *providersdk.OrphanedResourceError
		if errors.As(err, &orphanErr) {
			quarantined := newQuarantinedResource(pool.Name, string(driverType), agent.Info().ID, orphanErr, ap.now())
			if persist != nil {
				if perr := persist(&quarantined); perr != nil {
					return quarantined, false, perr
				}
			}
			return quarantined, false, wrapped
		}
		return model.Resource{}, false, wrapped
	}

	now := ap.now()

	// Merge connection info and metadata into properties.
	props := make(map[string]any, len(res.ConnectionInfo)+len(res.Metadata))
	for k, v := range res.ConnectionInfo {
		props[k] = v
	}
	for k, v := range res.Metadata {
		props[k] = v
	}

	built := model.Resource{
		ID:          model.ResourceID(res.ID),
		Type:        pool.Inventory.ExpectedType,
		Profile:     pool.Inventory.ExpectedProfile,
		OriginPool:  pool.Name,
		CurrentPool: pool.Name,
		Provider:    model.ProviderRef{Name: string(driverType), AgentID: agent.Info().ID},
		State:       model.ResourceStateReady,
		Properties:  props,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if persist != nil {
		if perr := persist(&built); perr != nil {
			return built, true, perr
		}
	}
	return built, true, nil
}

// now resolves ap.Now, defaulting to time.Now().UTC() when unset.
func (ap *AgentProvisioner) now() time.Time {
	if ap.Now != nil {
		return ap.Now().UTC()
	}
	return time.Now().UTC()
}

func (ap *AgentProvisioner) Allocate(ctx context.Context, pool model.Pool, res model.Resource) (providersdk.AllocationResult, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return providersdk.AllocationResult{}, fmt.Errorf("unknown pool %q", pool.Name)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return providersdk.AllocationResult{}, err
	}
	if gp, ok := agent.(agentsdk.GuestPersonalizingAgent); ok {
		result, err := gp.PersonalizeGuest(ctx, driverType, string(res.ID))
		if err != nil {
			return providersdk.AllocationResult{}, err
		}
		if result != nil {
			if ap.GuestSecrets != nil {
				if err := ap.GuestSecrets.Delete(ctx, boxysecrets.ResourceCredentialKey(string(res.ID))); err != nil && !errors.Is(err, boxysecrets.ErrNotFound) {
					slog.Default().Warn("could not remove consumed resource credential", "resource_id", res.ID, "error", err)
				}
			}
			return providersdk.AllocationResult{
				Properties:      result.AccessDetails.ToProperties(),
				GuestCredential: result.EphemeralCredential,
			}, nil
		}
	}
	properties, err := agent.Allocate(ctx, driverType, string(res.ID))
	return providersdk.AllocationResult{Properties: properties}, err
}

// AllocateWithPackages preserves the existing allocation behavior and then
// applies the explicitly requested allocation-scoped packages. It is an
// optional sandbox allocator capability so callers without package requests
// retain the old path.
func (ap *AgentProvisioner) AllocateWithPackages(ctx context.Context, pool model.Pool, res model.Resource, packages []string) (providersdk.AllocationResult, error) {
	allocation, err := ap.Allocate(ctx, pool, res)
	if err != nil || len(packages) == 0 {
		return allocation, err
	}
	if ap.PackageEngine == nil {
		return providersdk.AllocationResult{}, fmt.Errorf("resource package engine is not configured")
	}
	plan, err := ap.PackageEngine.Plan(ctx, resourcepack.Request{
		Target:     resourcepack.Target{ResourceID: string(res.ID), Provider: string(res.Provider.Name), AgentID: res.Provider.AgentID},
		Event:      resourcepack.EventAllocation,
		Scope:      resourcepack.ScopeAllocation,
		References: packages,
		Applied:    convertAppliedPackages(res.AppliedPackages),
	})
	if err != nil {
		return providersdk.AllocationResult{}, err
	}
	applied, err := ap.PackageEngine.Apply(ctx, plan, packageExecutor{provisioner: ap, credential: allocation.GuestCredential})
	if err != nil {
		return providersdk.AllocationResult{}, err
	}
	allocation.AppliedPackages = applied
	return allocation, nil
}

// ApplyResourcePackages applies resource-scoped packages for an admission or
// promotion event. The caller persists the returned applied records together
// with the resource state transition.
func (ap *AgentProvisioner) ApplyResourcePackages(ctx context.Context, pool model.Pool, res model.Resource, event resourcepack.Event) ([]resourcepack.AppliedPackage, error) {
	if len(pool.Packages) == 0 {
		return nil, nil
	}
	if ap.PackageEngine == nil {
		return nil, fmt.Errorf("resource package engine is not configured")
	}
	plan, err := ap.PackageEngine.Plan(ctx, resourcepack.Request{
		Target:     resourcepack.Target{ResourceID: string(res.ID), Provider: string(res.Provider.Name), AgentID: res.Provider.AgentID},
		Event:      event,
		Scope:      resourcepack.ScopeResource,
		References: pool.Packages,
		Applied:    res.AppliedPackages,
	})
	if err != nil {
		return nil, err
	}
	return ap.PackageEngine.Apply(ctx, plan, ap)
}

// Execute implements resourcepack.Executor. Package execution is translated
// to the existing provider-neutral ExecOperation and sent through the exact
// owning agent; package policy never crosses this boundary.
func (ap *AgentProvisioner) Execute(ctx context.Context, target resourcepack.Target, operation resourcepack.Operation) error {
	return ap.executePackage(ctx, target, operation, nil)
}

type packageExecutor struct {
	provisioner *AgentProvisioner
	credential  *providersdk.GuestCredential
}

func (e packageExecutor) Execute(ctx context.Context, target resourcepack.Target, operation resourcepack.Operation) error {
	return e.provisioner.executePackage(ctx, target, operation, e.credential)
}

func (ap *AgentProvisioner) executePackage(ctx context.Context, target resourcepack.Target, operation resourcepack.Operation, credential *providersdk.GuestCredential) error {
	if strings.TrimSpace(target.AgentID) == "" {
		return fmt.Errorf("package target agent is required")
	}
	agent, ok := ap.Registry.Get(target.AgentID)
	if !ok {
		return fmt.Errorf("agent %q unavailable for package target %q", target.AgentID, target.ResourceID)
	}
	if credential == nil && ap.GuestSecrets != nil {
		if raw, err := ap.GuestSecrets.Get(ctx, boxysecrets.ResourceCredentialKey(target.ResourceID)); err == nil {
			var stored providersdk.GuestCredential
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode resource package credential: %w", err)
			}
			credential = &stored
		} else if !errors.Is(err, boxysecrets.ErrNotFound) {
			return fmt.Errorf("get resource package credential: %w", err)
		}
	}
	command, err := packageCommand(operation)
	if err != nil {
		return err
	}
	env := make(map[string]string, len(operation.Parameters))
	for key, value := range operation.Parameters {
		env[key] = fmt.Sprint(value)
	}
	_, err = agent.Update(ctx, providersdk.Type(target.Provider), target.ResourceID, &providersdk.ExecOperation{
		Command:         command,
		Env:             env,
		GuestCredential: credential,
	})
	if err != nil {
		return fmt.Errorf("execute package %q: %w", operation.Reference, err)
	}
	return nil
}

func packageCommand(operation resourcepack.Operation) ([]string, error) {
	inline, _ := operation.Inputs["inline"].(string)
	script, _ := operation.Inputs["script"].(string)
	if len(operation.Content) != 0 {
		inline = string(operation.Content)
	}
	switch operation.Method {
	case resourcepack.MethodShell:
		if inline != "" {
			return []string{"sh", "-c", inline}, nil
		}
		if script != "" {
			return []string{"sh", script}, nil
		}
	case resourcepack.MethodPowerShell:
		if inline != "" {
			return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", inline}, nil
		}
		if script != "" {
			return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-File", script}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported package method %q", operation.Method)
	}
	return nil, fmt.Errorf("package %q must provide inputs.inline or inputs.script", operation.Reference)
}

func convertAppliedPackages(records []resourcepack.AppliedPackage) []resourcepack.AppliedPackage {
	return append([]resourcepack.AppliedPackage(nil), records...)
}

// PersonalizeGuestForPool runs only the guest-personalization capability for
// pool admission. It deliberately does not call generic Allocate, because the
// returned credential must be retained as the next bootstrap in the selected
// secret backend until the resource leaves the pool.
//
// SupportsGuestPersonalization must be checked (and, if true, a secret
// backend confirmed) before calling this — see its doc comment.
func (ap *AgentProvisioner) SupportsGuestPersonalization(_ context.Context, pool model.Pool, res model.Resource) (bool, error) {
	if _, ok := ap.Specs[pool.Name]; !ok {
		return false, fmt.Errorf("unknown pool %q", pool.Name)
	}
	agent, err := ap.agentForResource(res)
	if err != nil {
		return false, err
	}
	_, ok := agent.(agentsdk.GuestPersonalizingAgent)
	return ok, nil
}

func (ap *AgentProvisioner) PersonalizeGuestForPool(ctx context.Context, pool model.Pool, res model.Resource) (*providersdk.GuestPersonalizationResult, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return nil, fmt.Errorf("unknown pool %q", pool.Name)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return nil, err
	}
	gp, ok := agent.(agentsdk.GuestPersonalizingAgent)
	if !ok {
		return nil, nil
	}
	return gp.PersonalizeGuest(ctx, driverType, string(res.ID))
}

// ExecuteSandbox routes a provider-neutral command to the exact agent that
// owns a sandbox resource and requires that agent/provider to support live
// streaming.
func (ap *AgentProvisioner) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	spec, ok := ap.Specs[model.PoolName(res.OriginPool)]
	if !ok {
		return nil, fmt.Errorf("unknown origin pool %q", res.OriginPool)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return nil, err
	}
	streamer, ok := agent.(agentsdk.StreamingAgent)
	if !ok {
		return nil, fmt.Errorf("agent %q does not support streaming operations", agent.Info().ID)
	}
	operation.Command = append([]string(nil), operation.Command...)
	return streamer.UpdateStream(ctx, driverType, string(res.ID), &operation, sink)
}

func (ap *AgentProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return fmt.Errorf("unknown pool %q", pool.Name)
	}

	driverType := ap.driverTypeForPool(spec)
	id := strings.TrimSpace(string(res.ID))
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	agent, err := ap.agentForResource(res)
	if err != nil {
		return err
	}

	if err := agent.Delete(ctx, driverType, id); err != nil {
		return fmt.Errorf("agent delete for pool %q: %w", pool.Name, err)
	}
	return nil
}

// ForceOrphaner is implemented by provisioners that support force-orphaning
// a resource whose owning agent is permanently gone. It never contacts the
// agent — that's the whole point.
type ForceOrphaner interface {
	ForceOrphan(ctx context.Context, res model.Resource) error
}

// ForceOrphan detaches res from its (verified-gone) agent without any agent
// call. Refuses if the agent is still registered — see the precondition
// note on Manager.ForceOrphanResource.
func (ap *AgentProvisioner) ForceOrphan(ctx context.Context, res model.Resource) error {
	_ = ctx
	if _, ok := ap.Registry.Get(res.Provider.AgentID); ok {
		return fmt.Errorf("agent %q is still registered; force-orphan refused — deregister it first (`boxy agent revoke`) or use normal destroy", res.Provider.AgentID)
	}
	return nil
}

// agentForResource resolves the exact agent instance that created res, via
// its recorded AgentID — never by re-resolving the provider type, which
// could pick a different agent than the one that actually owns the
// resource. If that specific agent isn't currently registered/connected,
// the caller's existing retry/backoff path handles retrying later; this
// never silently substitutes a different agent.
func (ap *AgentProvisioner) agentForResource(res model.Resource) (agentsdk.Agent, error) {
	agent, ok := ap.Registry.Get(res.Provider.AgentID)
	if !ok {
		return nil, fmt.Errorf("agent %q unavailable for resource %q", res.Provider.AgentID, res.ID)
	}
	return agent, nil
}

// driverTypeForPool resolves the provider type for a pool spec.
// Priority:
// 1. If spec.Provider is set, resolve via Providers map or use as direct type
// 2. If spec.Type is docker/container, use "docker" driver type
// 3. Otherwise, use spec.Type as the driver type
func (ap *AgentProvisioner) driverTypeForPool(spec boxyconfig.PoolSpec) providersdk.Type {
	if strings.TrimSpace(spec.Provider) != "" {
		// Try to resolve as a named provider instance first
		if inst, ok := ap.Providers[spec.Provider]; ok {
			return inst.Type
		}
		// Otherwise treat provider name as a direct driver type
		return providersdk.Type(spec.Provider)
	}

	// Map pool type to driver type
	switch strings.TrimSpace(spec.Type) {
	case "docker", "container", "":
		return "docker"
	default:
		return providersdk.Type(spec.Type)
	}
}
