package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPoolSpecUnmarshalJSONTracksPolicyAliasesAndExplicitDrain(t *testing.T) {
	var policy PoolSpec
	if err := json.Unmarshal([]byte(`{
		"name":"web",
		"type":"container",
		"provider":"docker-local",
		"config":{"image":"alpine"},
		"policy":{"preheat":{"min_ready":0,"max_total":0},"recycle":{"max_age":"1h"}}
	}`), &policy); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	if !policy.PolicySet() || policy.PoliciesSet() {
		t.Fatalf("policy flags policy=%t policies=%t, want only policy", policy.PolicySet(), policy.PoliciesSet())
	}
	if !policy.EffectivePolicy().Preheat.MaxTotalSet() || !policy.EffectivePolicy().Preheat.ConfiguresDrain() {
		t.Fatalf("preheat = %+v, want explicit max_total drain", policy.EffectivePolicy().Preheat)
	}
	if policy.EffectivePolicy().Recycle.MaxAge != "1h" {
		t.Fatalf("recycle max_age = %q, want 1h", policy.EffectivePolicy().Recycle.MaxAge)
	}

	var policies PoolSpec
	if err := json.Unmarshal([]byte(`{"name":"web","policies":{"preheat":{"max_total":2}}}`), &policies); err != nil {
		t.Fatalf("unmarshal policies: %v", err)
	}
	if policies.PolicySet() || !policies.PoliciesSet() {
		t.Fatalf("alias flags policy=%t policies=%t, want only policies", policies.PolicySet(), policies.PoliciesSet())
	}
	if policies.EffectivePolicy().Preheat.MaxTotal != 2 || !policies.EffectivePolicy().Preheat.MaxTotalSet() {
		t.Fatalf("preheat = %+v, want explicit max_total 2", policies.EffectivePolicy().Preheat)
	}
}

func TestPoolSpecUnmarshalYAMLTracksExplicitPreheatFields(t *testing.T) {
	var spec PoolSpec
	if err := yaml.Unmarshal([]byte(`
name: win
type: vm
provider: hyperv-local
config:
  template_vhd: base.vhdx
policy:
  preheat:
    min_ready: 0
    max_total: 0
  recycle:
    max_age: 2h
`), &spec); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
	}
	if !spec.PolicySet() || spec.PoliciesSet() {
		t.Fatalf("policy flags policy=%t policies=%t, want only policy", spec.PolicySet(), spec.PoliciesSet())
	}
	preheat := spec.EffectivePolicy().Preheat
	if !preheat.MinReadySet() || !preheat.MaxTotalSet() || !preheat.ConfiguresDrain() {
		t.Fatalf("preheat = %+v, want explicit min_ready/max_total drain", preheat)
	}
	if spec.EffectivePolicy().Recycle.MaxAge != "2h" {
		t.Fatalf("recycle max_age = %q, want 2h", spec.EffectivePolicy().Recycle.MaxAge)
	}
}

func TestPoolSpecUnmarshalRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "pool field", body: `{"name":"web","unknown":true}`, want: `unknown field "unknown"`},
		{name: "policy field", body: `{"name":"web","policy":{"unknown":true}}`, want: `unknown field "unknown"`},
		{name: "preheat field", body: `{"name":"web","policy":{"preheat":{"unknown":true}}}`, want: `unknown field "unknown"`},
		{name: "recycle field", body: `{"name":"web","policy":{"recycle":{"unknown":true}}}`, want: `unknown field "unknown"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec PoolSpec
			err := json.Unmarshal([]byte(tt.body), &spec)
			if err == nil {
				t.Fatalf("json unmarshal error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json unmarshal error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestPoolSpecUnmarshalYAMLRejectsBadShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "pool scalar", body: `not-a-map`, want: "pool spec must be a mapping"},
		{name: "policy scalar", body: "name: web\npolicy: nope\n", want: "pool policy must be a mapping"},
		{name: "preheat scalar", body: "name: web\npolicy:\n  preheat: nope\n", want: "preheat policy must be a mapping"},
		{name: "recycle scalar", body: "name: web\npolicy:\n  recycle: nope\n", want: "recycle policy must be a mapping"},
		{name: "pool unknown", body: "name: web\nunknown: true\n", want: "field unknown not found"},
		{name: "policy unknown", body: "name: web\npolicy:\n  unknown: true\n", want: "field unknown not found"},
		{name: "preheat unknown", body: "name: web\npolicy:\n  preheat:\n    unknown: true\n", want: "field unknown not found"},
		{name: "recycle unknown", body: "name: web\npolicy:\n  recycle:\n    unknown: true\n", want: "field unknown not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec PoolSpec
			err := yaml.Unmarshal([]byte(tt.body), &spec)
			if err == nil {
				t.Fatalf("yaml unmarshal error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("yaml unmarshal error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}
