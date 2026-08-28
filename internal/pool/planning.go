package pool

import (
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

// computeToProvisionCount returns how many additional resources are needed to
// satisfy minReady, capped by MaxTotal when MaxTotal > 0.
//
// The gap is measured against totalCount, not readyCount that arrived a
// resource ago: totalCount already includes resources this same pool
// provisioned but that haven't reached ResourceStateReady yet (they're
// mid-admission -- e.g. guest personalization, per AdmissionHandler). A
// reconcile tick that instead compared minReady only against readyCount
// would treat every one of those in-flight resources as if it didn't exist,
// re-request the full remaining gap on every tick until they landed, and
// overshoot minReady by however many ticks admission took -- see #258.
func computeToProvisionCount(policy model.PreheatPolicy, totalCount int) int {
	if policy.MinReady <= 0 {
		return 0
	}

	need := policy.MinReady - totalCount
	if need <= 0 {
		return 0
	}

	if policy.MaxTotal > 0 {
		avail := policy.MaxTotal - totalCount
		if avail <= 0 {
			return 0
		}
		if need > avail {
			need = avail
		}
	}

	return need
}

// canSatisfyRequestedReady reports whether a pool constrained by maxTotal can
// satisfy requestedReady given current readyCount and totalCount.
func canSatisfyRequestedReady(maxTotal int, readyCount int, totalCount int, requestedReady int) bool {
	if maxTotal <= 0 || requestedReady <= 0 {
		return true
	}
	availableToProvision := maxTotal - totalCount
	if availableToProvision < 0 {
		availableToProvision = 0
	}
	return readyCount+availableToProvision >= requestedReady
}

// partitionByMaxAge splits items into stale/kept based on maxAge. CreatedAt is
// preferred as age base; UpdatedAt is a fallback when CreatedAt is zero.
func partitionByMaxAge[T any](
	items []T,
	now time.Time,
	maxAge time.Duration,
	createdAt func(T) time.Time,
	updatedAt func(T) time.Time,
) (stale []T, kept []T) {
	if maxAge <= 0 {
		return nil, append([]T(nil), items...)
	}

	stale = make([]T, 0)
	kept = make([]T, 0, len(items))
	for _, it := range items {
		ageBase := createdAt(it)
		if ageBase.IsZero() {
			ageBase = updatedAt(it)
		}
		if ageBase.IsZero() || now.Sub(ageBase) <= maxAge {
			kept = append(kept, it)
			continue
		}
		stale = append(stale, it)
	}
	return stale, kept
}
