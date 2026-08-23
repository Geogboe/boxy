package pool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

type admissionSecretStore struct {
	values map[string][]byte
}

func (s *admissionSecretStore) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, boxysecrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *admissionSecretStore) Put(_ context.Context, key string, value []byte) error {
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *admissionSecretStore) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func (s *admissionSecretStore) Check() error { return nil }

// admissionPersonalizer fakes GuestAdmissionPersonalizer. supports controls
// SupportsGuestPersonalization's answer independently of result/err, so
// tests can exercise "doesn't apply" (supports=false) and "applies but no
// secret backend" (supports=true, PersonalizeGuestForPool never reached)
// as distinct scenarios — mirroring how a real driver's capability is known
// before any rotation is attempted.
type admissionPersonalizer struct {
	supports   bool
	supportErr error
	result     *providersdk.GuestPersonalizationResult
	err        error
	calls      int
}

func (p *admissionPersonalizer) SupportsGuestPersonalization(context.Context, model.Pool, model.Resource) (bool, error) {
	return p.supports, p.supportErr
}

func (p *admissionPersonalizer) PersonalizeGuestForPool(context.Context, model.Pool, model.Resource) (*providersdk.GuestPersonalizationResult, error) {
	p.calls++
	return p.result, p.err
}

type admissionFailureRecorder struct {
	resource model.Resource
	cause    error
}

func (r *admissionFailureRecorder) FailAdmission(_ context.Context, resource model.Resource, cause error) error {
	r.resource = resource
	r.cause = cause
	return nil
}

func TestAdmissionHandlerRotatesAndPersistsBeforeReady(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithAdmissionResource(t)
	secrets := &admissionSecretStore{}
	personalizer := &admissionPersonalizer{supports: true, result: &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{Properties: map[string]string{"guest_address": "192.0.2.10"}},
		EphemeralCredential: &providersdk.GuestCredential{
			Kind: "password",
			Data: json.RawMessage(`{"username":"Administrator","password":"${BOXY_TEST_PASSWORD}"}`),
		},
	}}
	handler := &AdmissionHandler{Store: st, Secrets: secrets, Personalizer: personalizer}
	payload, _ := json.Marshal(resourceProvisionedPayload{ResourceID: "res-1", PoolName: "win-vm"})

	outcome, err := handler.Handle(ctx, lifecycle.Event{ID: "event-1", Type: ResourceProvisionedEventType, Subject: "res-1", Payload: payload})
	if err != nil || outcome != lifecycle.OutcomeAck {
		t.Fatalf("Handle() = (%v, %v), want (ack, nil)", outcome, err)
	}
	res, err := st.GetResource(ctx, "res-1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want ready", res.State)
	}
	if res.Properties["guest_address"] != "192.0.2.10" {
		t.Fatalf("resource properties = %#v, want safe access metadata", res.Properties)
	}
	stored, ok := secrets.values["resource/res-1/guest-credential"]
	if !ok || len(stored) == 0 {
		t.Fatalf("stored resource credential = %q, want non-empty opaque credential", stored)
	}
	if personalizer.calls != 1 {
		t.Fatalf("personalizer calls = %d, want 1", personalizer.calls)
	}
}

// TestAdmissionHandlerMarksReadyWithoutSecretsWhenPersonalizerDoesNotApply
// covers a driver that doesn't implement GuestPersonalizer (e.g. docker,
// devfactory) — SupportsGuestPersonalization correctly returns false for
// those, and admission must not demand a configured secret backend just
// because *some* pool in the server might need one. Before this fix, the
// handler required h.Secrets != nil unconditionally whenever a Personalizer
// was wired at all, which broke every non-guest-personalization pool
// (docker, devfactory) unless server.secrets happened to be configured —
// see #181's design spec, "Follow-ups".
func TestAdmissionHandlerMarksReadyWithoutSecretsWhenPersonalizerDoesNotApply(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithAdmissionResource(t)
	personalizer := &admissionPersonalizer{supports: false}
	handler := &AdmissionHandler{Store: st, Secrets: nil, Personalizer: personalizer}
	payload, _ := json.Marshal(resourceProvisionedPayload{ResourceID: "res-1", PoolName: "win-vm"})

	outcome, err := handler.Handle(ctx, lifecycle.Event{ID: "event-1", Type: ResourceProvisionedEventType, Subject: "res-1", Payload: payload})
	if err != nil || outcome != lifecycle.OutcomeAck {
		t.Fatalf("Handle() = (%v, %v), want (ack, nil)", outcome, err)
	}
	res, err := st.GetResource(ctx, "res-1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want ready", res.State)
	}
	if personalizer.calls != 0 {
		t.Fatalf("personalizer calls = %d, want 0 (PersonalizeGuestForPool must never run once capability is confirmed absent)", personalizer.calls)
	}
}

// TestAdmissionHandlerRequiresSecretsWhenPersonalizerAppliesWithoutBackend
// confirms the original protection still holds for a driver that DOES
// implement GuestPersonalizer (e.g. hyperv) when no secret backend is
// configured — only the not-applicable case from the test above should be
// exempt. Critically, PersonalizeGuestForPool must never be called in this
// case: it performs a real, live guest credential rotation, and calling it
// with no backend to store the result would rotate a real password and lose
// it permanently (see ADR-0010). This was a real, unaddressed bug in the
// initial version of this fix — the ordering swap that made the test above
// pass also allowed this call through before the h.Secrets check ran.
func TestAdmissionHandlerRequiresSecretsWhenPersonalizerAppliesWithoutBackend(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithAdmissionResource(t)
	personalizer := &admissionPersonalizer{supports: true, result: &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{Properties: map[string]string{"guest_address": "192.0.2.10"}},
		EphemeralCredential: &providersdk.GuestCredential{
			Kind: "password",
			Data: json.RawMessage(`{"username":"Administrator","password":"${BOXY_TEST_PASSWORD}"}`),
		},
	}}
	handler := &AdmissionHandler{Store: st, Secrets: nil, Personalizer: personalizer}
	payload, _ := json.Marshal(resourceProvisionedPayload{ResourceID: "res-1", PoolName: "win-vm"})

	outcome, err := handler.Handle(ctx, lifecycle.Event{ID: "event-1", Type: ResourceProvisionedEventType, Subject: "res-1", Payload: payload})
	if outcome != lifecycle.OutcomeTerminal || err == nil {
		t.Fatalf("Handle() = (%v, %v), want terminal error (no secret backend for a real credential)", outcome, err)
	}
	if personalizer.calls != 0 {
		t.Fatalf("personalizer calls = %d, want 0 (must fail before ever rotating a live guest credential)", personalizer.calls)
	}
}

func TestAdmissionHandlerFailureQuarantinesWithoutRetryingSameResource(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithAdmissionResource(t)
	failures := &admissionFailureRecorder{}
	handler := &AdmissionHandler{
		Store:        st,
		Secrets:      &admissionSecretStore{},
		Personalizer: &admissionPersonalizer{supports: true, err: errors.New("guest rotation failed")},
		Failures:     failures,
	}
	payload, _ := json.Marshal(resourceProvisionedPayload{ResourceID: "res-1", PoolName: "win-vm"})

	outcome, err := handler.Handle(ctx, lifecycle.Event{ID: "event-1", Type: ResourceProvisionedEventType, Subject: "res-1", Payload: payload})
	if outcome != lifecycle.OutcomeTerminal || err == nil {
		t.Fatalf("Handle() = (%v, %v), want terminal error", outcome, err)
	}
	if failures.resource.ID != "res-1" || failures.cause == nil {
		t.Fatalf("failure recorder = %+v, want resource and cause", failures)
	}
}

func TestEventPublisherUsesStableIdentifierOnlyPayload(t *testing.T) {
	events := newTestEventStore()
	now := time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)
	publisher := &EventPublisher{Events: events, Now: func() time.Time { return now }}
	res := model.Resource{ID: "res-1", OriginPool: "win-vm", Properties: map[string]any{"password": "${BOXY_TEST_PASSWORD}"}}
	if err := publisher.PublishResourceProvisioned(context.Background(), res); err != nil {
		t.Fatalf("PublishResourceProvisioned: %v", err)
	}
	claim, err := events.Claim(context.Background(), now, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Event.ID != "resource.provisioned:res-1" || claim.Event.RecordedAt != now {
		t.Fatalf("event = %+v, want stable ID and server receipt time", claim.Event)
	}
	if string(claim.Event.Payload) != `{"resource_id":"res-1","pool_name":"win-vm"}` {
		t.Fatalf("event payload = %s, want identifiers only", claim.Event.Payload)
	}
}

func TestManagerPersistsProvisioningResourceBeforeAdmission(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "web",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	events := store.NewMemoryStore()
	mgr := New(st, &fakeProvisioner{})
	mgr.SetClock(fixedClock{t: time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)})
	mgr.SetAdmissionPublisher(&EventPublisher{Events: events, Now: func() time.Time { return time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC) }})

	if err := mgr.Reconcile(ctx, "web"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	resources, err := st.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 || resources[0].State != model.ResourceStateProvisioning {
		t.Fatalf("resources = %+v, want one provisioning resource", resources)
	}
	claim, err := events.Claim(ctx, time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC), time.Minute)
	if err != nil {
		t.Fatalf("Claim admission event: %v", err)
	}
	if claim.Event.Subject != string(resources[0].ID) {
		t.Fatalf("event subject = %q, want resource %q", claim.Event.Subject, resources[0].ID)
	}
}

func TestManagerDestroyCleansUnallocatedResourceCredential(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{
		ID:         "res-1",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		Provider:   model.ProviderRef{Name: "docker"},
		State:      model.ResourceStateReady,
	}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:      "web",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{res}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	secrets := &admissionSecretStore{}
	if err := secrets.Put(ctx, "resource/res-1/guest-credential", []byte("${BOXY_TEST_PASSWORD}")); err != nil {
		t.Fatalf("Put credential: %v", err)
	}
	mgr := New(st, &fakeProvisioner{})
	mgr.SetGuestSecretStore(secrets)
	if err := mgr.DestroyResource(ctx, res); err != nil {
		t.Fatalf("DestroyResource: %v", err)
	}
	if _, err := secrets.Get(ctx, "resource/res-1/guest-credential"); !errors.Is(err, boxysecrets.ErrNotFound) {
		t.Fatalf("credential after destroy error = %v, want ErrNotFound", err)
	}
}

func newTestStoreWithAdmissionResource(t *testing.T) *store.MemoryStore {
	t.Helper()
	st := store.NewMemoryStore()
	if err := st.PutResource(context.Background(), model.Resource{ID: "res-1", OriginPool: "win-vm", State: model.ResourceStateProvisioning}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.PutPool(context.Background(), model.Pool{Name: "win-vm", Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM}}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	return st
}

func newTestEventStore() *testEventStore {
	return &testEventStore{records: make(map[string]lifecycle.Record)}
}

type testEventStore struct {
	records map[string]lifecycle.Record
}

func (s *testEventStore) Append(_ context.Context, event lifecycle.Event) error {
	s.records[event.ID] = lifecycle.Record{Event: event, Status: lifecycle.StatusPending}
	return nil
}

func (s *testEventStore) Claim(_ context.Context, now time.Time, lease time.Duration) (lifecycle.Claim, error) {
	for id, record := range s.records {
		if record.Status != lifecycle.StatusPending || record.Event.AvailableAt.After(now) {
			continue
		}
		record.Status = lifecycle.StatusLeased
		record.LeaseToken = "lease"
		record.LeaseUntil = now.Add(lease)
		s.records[id] = record
		return lifecycle.Claim{Event: record.Event, LeaseToken: record.LeaseToken, LeaseUntil: record.LeaseUntil}, nil
	}
	return lifecycle.Claim{}, lifecycle.ErrNoWork
}

func (s *testEventStore) Ack(_ context.Context, claim lifecycle.Claim) error {
	record := s.records[claim.Event.ID]
	record.Status = lifecycle.StatusAcked
	s.records[claim.Event.ID] = record
	return nil
}
func (s *testEventStore) Retry(context.Context, lifecycle.Claim, time.Time, error) error { return nil }
func (s *testEventStore) Fail(context.Context, lifecycle.Claim, error) error             { return nil }
func (s *testEventStore) Compact(context.Context, time.Time) error                       { return nil }
