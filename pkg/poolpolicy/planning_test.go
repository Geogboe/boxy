package poolpolicy

import (
	"testing"
	"time"
)

func TestComputeToProvision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policy     PreheatPolicy
		readyCount int
		totalCount int
		want       int
	}{
		{
			name:       "no min ready target",
			policy:     PreheatPolicy{MinReady: 0, MaxTotal: 10},
			readyCount: 0,
			totalCount: 0,
			want:       0,
		},
		{
			name:       "provision full gap when under cap",
			policy:     PreheatPolicy{MinReady: 3, MaxTotal: 10},
			readyCount: 1,
			totalCount: 2,
			want:       2,
		},
		{
			name:       "cap provision by max total",
			policy:     PreheatPolicy{MinReady: 5, MaxTotal: 3},
			readyCount: 1,
			totalCount: 2,
			want:       1,
		},
		{
			name:       "unbounded max total",
			policy:     PreheatPolicy{MinReady: 4, MaxTotal: 0},
			readyCount: 1,
			totalCount: 100,
			want:       3,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeToProvision(tc.policy, tc.readyCount, tc.totalCount)
			if got != tc.want {
				t.Fatalf("ComputeToProvision() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCanSatisfyRequestedReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		policy         PreheatPolicy
		readyCount     int
		totalCount     int
		requestedReady int
		want           bool
	}{
		{
			name:           "max total disabled always satisfiable",
			policy:         PreheatPolicy{MaxTotal: 0},
			readyCount:     0,
			totalCount:     10,
			requestedReady: 100,
			want:           true,
		},
		{
			name:           "already enough ready",
			policy:         PreheatPolicy{MaxTotal: 5},
			readyCount:     2,
			totalCount:     2,
			requestedReady: 2,
			want:           true,
		},
		{
			name:           "enough headroom to satisfy",
			policy:         PreheatPolicy{MaxTotal: 3},
			readyCount:     1,
			totalCount:     2,
			requestedReady: 2,
			want:           true,
		},
		{
			name:           "insufficient headroom to satisfy",
			policy:         PreheatPolicy{MaxTotal: 1},
			readyCount:     0,
			totalCount:     1,
			requestedReady: 1,
			want:           false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CanSatisfyRequestedReady(tc.policy, tc.readyCount, tc.totalCount, tc.requestedReady)
			if got != tc.want {
				t.Fatalf("CanSatisfyRequestedReady() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPartitionByMaxAge(t *testing.T) {
	t.Parallel()

	type item struct {
		ID      string
		Created time.Time
		Updated time.Time
	}

	now := time.Unix(7200, 0).UTC()
	items := []item{
		{ID: "old-created", Created: time.Unix(0, 0).UTC()},
		{ID: "old-updated", Updated: time.Unix(0, 0).UTC()},
		{ID: "fresh", Created: time.Unix(7190, 0).UTC()},
		{ID: "zero"},
	}

	stale, kept := PartitionByMaxAge(items, now, time.Hour, func(it item) time.Time {
		return it.Created
	}, func(it item) time.Time {
		return it.Updated
	})

	if len(stale) != 2 {
		t.Fatalf("stale len = %d, want 2", len(stale))
	}
	if stale[0].ID != "old-created" || stale[1].ID != "old-updated" {
		t.Fatalf("unexpected stale ordering/content: %#v", stale)
	}

	if len(kept) != 2 {
		t.Fatalf("kept len = %d, want 2", len(kept))
	}
	if kept[0].ID != "fresh" || kept[1].ID != "zero" {
		t.Fatalf("unexpected kept ordering/content: %#v", kept)
	}
}
