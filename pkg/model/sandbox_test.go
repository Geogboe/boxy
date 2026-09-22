package model_test

import (
	"encoding/json"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestSandboxStatus_IsTransient(t *testing.T) {
	cases := []struct {
		status model.SandboxStatus
		want   bool
	}{
		{model.SandboxStatusPending, true},
		{model.SandboxStatusProvisioning, true},
		{model.SandboxStatusDeleting, true},
		{model.SandboxStatusReady, false},
		{model.SandboxStatusFailed, false},
		{model.SandboxStatus("unknown"), false},
	}
	for _, tc := range cases {
		if got := tc.status.IsTransient(); got != tc.want {
			t.Errorf("SandboxStatus(%q).IsTransient() = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestSandbox_NetworkSegmentsRoundTripsThroughJSON(t *testing.T) {
	sb := model.Sandbox{
		ID: "sb-1",
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"},
		},
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Sandbox
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.NetworkSegments) != 1 || got.NetworkSegments[0].Ref != "boxy-sb-sb-1" {
		t.Fatalf("NetworkSegments did not round-trip: %+v", got.NetworkSegments)
	}
}

func TestSandbox_NoNetworkSegmentsOmittedFromJSON(t *testing.T) {
	sb := model.Sandbox{ID: "sb-1"}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); containsField(got, "network_segments") {
		t.Fatalf("expected network_segments to be omitted when empty, got %s", got)
	}
}

func containsField(jsonStr, field string) bool {
	return len(jsonStr) > 0 && jsonContains(jsonStr, `"`+field+`"`)
}

func jsonContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
