package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

const ResourceProvisionedEventType = "resource.provisioned"

type resourceProvisionedPayload struct {
	ResourceID model.ResourceID `json:"resource_id"`
	PoolName   model.PoolName   `json:"pool_name"`
}

// AdmissionPublisher records the durable event that starts resource
// preparation. Manager calls it again during reconciliation for pending
// resources, making the create/store/event sequence recoverable after a crash.
type AdmissionPublisher interface {
	PublishResourceProvisioned(context.Context, model.Resource) error
}

type EventPublisher struct {
	Events lifecycle.EventStore
	Now    func() time.Time
}

func (p *EventPublisher) PublishResourceProvisioned(ctx context.Context, res model.Resource) error {
	if p == nil || p.Events == nil {
		return fmt.Errorf("lifecycle event store is required")
	}
	if strings.TrimSpace(string(res.ID)) == "" || strings.TrimSpace(string(res.OriginPool)) == "" {
		return fmt.Errorf("resource ID and origin pool are required for admission")
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	payload, err := json.Marshal(resourceProvisionedPayload{ResourceID: res.ID, PoolName: res.OriginPool})
	if err != nil {
		return fmt.Errorf("encode resource admission event: %w", err)
	}
	event := lifecycle.Event{
		ID:          "resource.provisioned:" + string(res.ID),
		Type:        ResourceProvisionedEventType,
		Subject:     string(res.ID),
		Payload:     payload,
		RecordedAt:  now,
		AvailableAt: now,
	}
	return p.Events.Append(ctx, event)
}

// GuestAdmissionPersonalizer is implemented by provider adapters that can
// rotate a newly created guest before it becomes ready inventory.
type GuestAdmissionPersonalizer interface {
	PersonalizeGuestForPool(context.Context, model.Pool, model.Resource) (*providersdk.GuestPersonalizationResult, error)
}

// AdmissionFailureRecorder records a failed preparation as an observable
// resource error. The pool reconciler then uses its normal quarantine and
// replacement backoff path.
type AdmissionFailureRecorder interface {
	FailAdmission(context.Context, model.Resource, error) error
}

// AdmissionHandler applies the policy for resource.provisioned events.
type AdmissionHandler struct {
	Store        store.Store
	Secrets      boxysecrets.Store
	Personalizer GuestAdmissionPersonalizer
	Failures     AdmissionFailureRecorder
}

func (h *AdmissionHandler) Handle(ctx context.Context, event lifecycle.Event) (lifecycle.Outcome, error) {
	if h == nil || h.Store == nil {
		return lifecycle.OutcomeTerminal, fmt.Errorf("admission store is required")
	}
	var payload resourceProvisionedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return lifecycle.OutcomeTerminal, fmt.Errorf("decode resource admission event: %w", err)
	}
	if payload.ResourceID == "" {
		return lifecycle.OutcomeTerminal, fmt.Errorf("resource admission event has no resource ID")
	}
	res, err := h.Store.GetResource(ctx, payload.ResourceID)
	if errors.Is(err, store.ErrNotFound) {
		return lifecycle.OutcomeAck, nil
	}
	if err != nil {
		return lifecycle.OutcomeRetry, fmt.Errorf("get resource %q: %w", payload.ResourceID, err)
	}
	if res.State == model.ResourceStateReady || res.State == model.ResourceStateDestroyed || res.State == model.ResourceStateError {
		return lifecycle.OutcomeAck, nil
	}
	pool, err := h.Store.GetPool(ctx, res.OriginPool)
	if err != nil {
		return h.fail(ctx, res, fmt.Errorf("get origin pool %q: %w", res.OriginPool, err))
	}

	if h.Secrets != nil {
		if current, getErr := h.Secrets.Get(ctx, boxysecrets.ResourceCredentialKey(string(res.ID))); getErr == nil {
			var credential providersdk.GuestCredential
			if err := json.Unmarshal(current, &credential); err != nil {
				return h.fail(ctx, res, fmt.Errorf("validate stored resource credential: %w", err))
			}
			if strings.TrimSpace(string(credential.Data)) == "" {
				return h.fail(ctx, res, fmt.Errorf("validate stored resource credential: empty credential data"))
			}
			return h.markReady(ctx, res, nil)
		} else if !errors.Is(getErr, boxysecrets.ErrNotFound) {
			return lifecycle.OutcomeRetry, fmt.Errorf("get resource credential: %w", getErr)
		}
	}

	if h.Personalizer == nil {
		return h.markReady(ctx, res, nil)
	}
	result, err := h.Personalizer.PersonalizeGuestForPool(ctx, pool, res)
	if err != nil {
		return h.fail(ctx, res, fmt.Errorf("personalize resource %q: %w", res.ID, err))
	}
	if result == nil {
		// The pool's driver doesn't implement GuestPersonalizer (e.g.
		// docker, devfactory) — this is a routine, expected outcome, not a
		// missing-config error. A secret backend is only actually needed
		// below, to store a real credential a driver that DOES implement
		// GuestPersonalizer just returned. Checking h.Secrets before this
		// call would demand a secret backend for every pool in the server,
		// not just ones that need one. See #181's design spec follow-ups.
		return h.markReady(ctx, res, nil)
	}
	if h.Secrets == nil {
		return h.fail(ctx, res, fmt.Errorf("secret backend is required for guest admission"))
	}
	if result.EphemeralCredential == nil || len(result.EphemeralCredential.Data) == 0 {
		return h.fail(ctx, res, fmt.Errorf("personalization returned no credential for resource %q", res.ID))
	}
	credentialJSON, err := json.Marshal(result.EphemeralCredential)
	if err != nil {
		return h.fail(ctx, res, fmt.Errorf("encode resource credential: %w", err))
	}
	if err := h.Secrets.Put(ctx, boxysecrets.ResourceCredentialKey(string(res.ID)), credentialJSON); err != nil {
		return h.fail(ctx, res, fmt.Errorf("store resource credential: %w", err))
	}
	return h.markReady(ctx, res, result.AccessDetails.ToProperties())
}

func (h *AdmissionHandler) markReady(ctx context.Context, res model.Resource, properties map[string]any) (lifecycle.Outcome, error) {
	if properties != nil {
		if res.Properties == nil {
			res.Properties = make(map[string]any)
		}
		for key, value := range properties {
			res.Properties[key] = value
		}
	}
	res.State = model.ResourceStateReady
	res.UpdatedAt = time.Now().UTC()
	if err := h.Store.PutResource(ctx, res); err != nil {
		return lifecycle.OutcomeRetry, fmt.Errorf("mark resource %q ready: %w", res.ID, err)
	}
	return lifecycle.OutcomeAck, nil
}

func (h *AdmissionHandler) fail(ctx context.Context, res model.Resource, cause error) (lifecycle.Outcome, error) {
	if h.Failures != nil {
		if err := h.Failures.FailAdmission(ctx, res, cause); err != nil {
			return lifecycle.OutcomeTerminal, errors.Join(cause, err)
		}
	}
	return lifecycle.OutcomeTerminal, cause
}
