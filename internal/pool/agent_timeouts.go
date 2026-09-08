package pool

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

// AgentOperationTimeouts bounds AgentProvisioner's agent-backed calls with
// per-operation-class durations (#333). It intentionally holds resolved
// time.Duration values rather than raw config strings so internal/pool stays
// decoupled from internal/config — the same pattern already used for other
// injected Manager/AgentProvisioner fields (e.g. Now func() time.Time).
// Callers (internal/cli/serve.go) resolve internal/config.AgentTimeoutsSpec
// into this struct before constructing an AgentProvisioner. A zero value in
// any field means "unbounded" for that operation class — used by tests and
// any embedder that hasn't opted in yet.
type AgentOperationTimeouts struct {
	Create           time.Duration
	PersonalizeGuest time.Duration
	Delete           time.Duration
	// Default is a reserved fallback with no current consumer in
	// internal/pool — mirrors internal/config.AgentTimeoutsSpec.Default's
	// doc comment. internal/sandbox.Fulfiller's per-sandbox pass bound is
	// deliberately internal/config.AgentTimeoutsSpec.
	// EffectiveSandboxFulfillTimeout (Create+PersonalizeGuest+Delete
	// summed), not this field.
	Default time.Duration
}

// withTimeout wraps ctx with d if d > 0, returning a no-op cancel func
// otherwise so callers can defer it unconditionally.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// effectiveDeadlineBound returns the actual bound a call wrapped with
// withTimeout(ctx, configured) is subject to: whichever is sooner between
// ctx's own inherited deadline (if any — e.g. internal/sandbox.Fulfiller's
// own per-sandbox pass timeout) and configured. context.WithTimeout already
// respects the earlier of the two when actually enforcing the deadline; this
// mirrors that so a timeout log/error reports the limit that actually fired
// rather than always reporting configured, which would be actively
// misleading whenever a shorter caller-imposed deadline is what triggered
// the timeout (see #333's sizing note on EffectiveSandboxFulfillTimeout).
func effectiveDeadlineBound(ctx context.Context, configured time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		if configured <= 0 || remaining < configured {
			return remaining
		}
	}
	return configured
}

// GuestPersonalizationTimeoutError reports that an allocation-time
// PersonalizeGuest call exceeded its configured bound. Callers must
// quarantine the affected resource (mark it ResourceStateError and never
// return it to Ready inventory) instead of treating this like an ordinary
// allocation failure, because the guest's real credential state after a
// timed-out rotation is unknown (see ADR-0010): handing a possibly-rotated
// credential resource to the next caller would be unsafe. CredentialDeleted
// records whether AgentProvisioner successfully removed the now-untrusted
// stored credential as part of handling the timeout.
type GuestPersonalizationTimeoutError struct {
	ResourceID        model.ResourceID
	PoolName          model.PoolName
	AgentID           string
	Timeout           time.Duration
	Elapsed           time.Duration
	CredentialDeleted bool
}

func (e *GuestPersonalizationTimeoutError) Error() string {
	return fmt.Sprintf(
		"personalize guest for resource %q in pool %q (agent %q) timed out after %s (limit %s)",
		e.ResourceID, e.PoolName, e.AgentID, e.Elapsed, e.Timeout,
	)
}

func (e *GuestPersonalizationTimeoutError) Unwrap() error {
	return context.DeadlineExceeded
}
