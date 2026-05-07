package poolpolicy

import "time"

// PreheatPolicy is generic policy input for readiness/capacity planning.
type PreheatPolicy struct {
	MinReady int
	MaxTotal int
}

// ComputeToProvision returns how many additional ready resources are needed to
// satisfy MinReady, capped by MaxTotal when MaxTotal > 0.
func ComputeToProvision(policy PreheatPolicy, readyCount int, totalCount int) int {
	if policy.MinReady <= 0 {
		return 0
	}

	need := policy.MinReady - readyCount
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

// CanSatisfyRequestedReady reports whether a pool constrained by MaxTotal can
// satisfy requestedReady given current readyCount and totalCount.
func CanSatisfyRequestedReady(policy PreheatPolicy, readyCount int, totalCount int, requestedReady int) bool {
	if policy.MaxTotal <= 0 || requestedReady <= 0 {
		return true
	}
	availableToProvision := policy.MaxTotal - totalCount
	if availableToProvision < 0 {
		availableToProvision = 0
	}
	return readyCount+availableToProvision >= requestedReady
}

// PartitionByMaxAge splits items into stale/kept based on maxAge. CreatedAt is
// preferred as age base; UpdatedAt is a fallback when CreatedAt is zero.
func PartitionByMaxAge[T any](
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
