package resourcepack

import (
	"context"
	"errors"
	"testing"

	"github.com/Geogboe/boxy/pkg/artifact"
	"gopkg.in/yaml.v3"
)

func packageArtifact(manifest string) artifact.Artifact {
	return artifact.Artifact{
		Type:     artifact.ArtifactTypePackage,
		Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: "app3", Version: "1.0.0"},
		Manifest: []byte(manifest),
	}
}

func TestEnginePlanMergesParametersAndSkipsAppliedIdentity(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	if err := reg.Publish(context.Background(), packageArtifact(`
name: app3
version: 1.0.0
method: powershell
scopes: [resource]
events: [provision, promotion]
defaults:
  channel: stable
inputs:
  script: install.ps1
  parameters:
    Foo: package
`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	engine := Engine{Registry: reg}
	plan, err := engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"app3@1.0.0"},
		Overrides:  map[string]any{"Foo": "hook", "Boo": "sandbox"},
		Applied:    []AppliedPackage{{Reference: "old@1.0.0", InputDigest: "different"}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Packages) != 1 {
		t.Fatalf("planned package count = %d, want 1", len(plan.Packages))
	}
	params := plan.Packages[0].Parameters
	if params["channel"] != "stable" || params["Foo"] != "hook" || params["Boo"] != "sandbox" {
		t.Fatalf("parameters = %#v, want merged defaults and overrides", params)
	}

	plan, err = engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"app3@1.0.0"},
		Overrides:  map[string]any{"Foo": "hook", "Boo": "sandbox"},
		Applied:    []AppliedPackage{{Reference: "app3@1.0.0", InputDigest: plan.Packages[0].InputDigest}},
	})
	if err != nil {
		t.Fatalf("Plan already applied: %v", err)
	}
	if len(plan.Packages) != 0 {
		t.Fatalf("planned already-applied packages = %d, want 0", len(plan.Packages))
	}
}

func TestEnginePlanRejectsScopeAndUnsupportedMethod(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	for name, manifest := range map[string]string{
		"wrong-scope": `name: wrong-scope
version: 1.0.0
method: shell
scopes: [allocation]
events: [allocation]`,
		"future": `name: future
version: 1.0.0
method: ansible
scopes: [resource]
events: [provision]`,
	} {
		if err := reg.Publish(context.Background(), artifact.Artifact{
			Type:     artifact.ArtifactTypePackage,
			Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: name, Version: "1.0.0"},
			Manifest: []byte(manifest),
		}); err != nil {
			t.Fatalf("Publish %s: %v", name, err)
		}
	}
	engine := Engine{Registry: reg}
	if _, err := engine.Plan(context.Background(), Request{Event: EventProvision, Scope: ScopeResource, References: []string{"wrong-scope@1.0.0"}}); err == nil {
		t.Fatal("wrong-scope package planned successfully")
	}
	if _, err := engine.Plan(context.Background(), Request{Event: EventProvision, Scope: ScopeResource, References: []string{"future@1.0.0"}}); err == nil {
		t.Fatal("future method planned successfully")
	}
}

func TestEngineApplyUsesInjectedExecutor(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	if err := reg.Publish(context.Background(), packageArtifact(`name: app3
version: 1.0.0
method: shell
scopes: [resource]
events: [provision]
inputs:
  inline: echo ready`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	engine := Engine{Registry: reg}
	plan, err := engine.Plan(context.Background(), Request{Event: EventProvision, Scope: ScopeResource, References: []string{"app3@1.0.0"}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	exec := &recordingExecutor{}
	applied, err := engine.Apply(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 || len(exec.operations) != 1 {
		t.Fatalf("applied=%#v operations=%d, want one each", applied, len(exec.operations))
	}
}

func TestEngineApplyPreservesPackageOrderAndStopsAfterFailure(t *testing.T) {
	t.Parallel()

	reg := artifact.NewMemoryRegistry()
	for _, spec := range []struct {
		name    string
		manager string
		id      string
	}{
		{name: "chocolatey", manager: "winget", id: "Chocolatey.Chocolatey"},
		{name: "windows-tools", manager: "chocolatey", id: "git"},
	} {
		manifest := packageManagerManifest(spec.manager, spec.id)
		manifest.Name = spec.name
		payload, err := yaml.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal %s: %v", spec.name, err)
		}
		if err := reg.Publish(context.Background(), artifact.Artifact{
			Type:     artifact.ArtifactTypePackage,
			Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: spec.name, Version: "1.0.0"},
			Manifest: payload,
		}); err != nil {
			t.Fatalf("Publish %s: %v", spec.name, err)
		}
	}

	engine := Engine{Registry: reg}
	plan, err := engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"chocolatey@1.0.0", "windows-tools@1.0.0"},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := []string{plan.Packages[0].Reference, plan.Packages[1].Reference}; got[0] != "chocolatey@1.0.0" || got[1] != "windows-tools@1.0.0" {
		t.Fatalf("planned references = %v, want declaration order", got)
	}

	failing := &recordingExecutor{failAt: 1}
	applied, err := engine.Apply(context.Background(), plan, failing)
	if err == nil {
		t.Fatal("Apply succeeded, want first package failure")
	}
	if len(failing.operations) != 1 || failing.operations[0].Reference != "chocolatey@1.0.0" {
		t.Fatalf("operations after failure = %+v, want only Chocolatey bootstrap", failing.operations)
	}
	if len(applied) != 0 {
		t.Fatalf("applied after failure = %+v, want none", applied)
	}
	retryPlan, err := engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"chocolatey@1.0.0", "windows-tools@1.0.0"},
		Applied:    applied,
	})
	if err != nil {
		t.Fatalf("Plan after failure: %v", err)
	}
	if len(retryPlan.Packages) != 2 {
		t.Fatalf("packages planned after failure = %d, want 2 for retry", len(retryPlan.Packages))
	}

	success := &recordingExecutor{}
	applied, err = engine.Apply(context.Background(), plan, success)
	if err != nil {
		t.Fatalf("Apply retry: %v", err)
	}
	if len(success.operations) != 2 || success.operations[0].Reference != "chocolatey@1.0.0" || success.operations[1].Reference != "windows-tools@1.0.0" {
		t.Fatalf("successful operation order = %+v, want declaration order", success.operations)
	}
	if len(applied) != 2 {
		t.Fatalf("applied on retry = %+v, want both packages", applied)
	}
	finalPlan, err := engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"chocolatey@1.0.0", "windows-tools@1.0.0"},
		Applied:    applied,
	})
	if err != nil {
		t.Fatalf("Plan after success: %v", err)
	}
	if len(finalPlan.Packages) != 0 {
		t.Fatalf("packages planned after success = %d, want 0", len(finalPlan.Packages))
	}
}

type recordingExecutor struct {
	operations []Operation
	failAt     int
}

func (e *recordingExecutor) Execute(_ context.Context, _ Target, op Operation) error {
	e.operations = append(e.operations, op)
	if e.failAt > 0 && len(e.operations) == e.failAt {
		return errors.New("simulated package failure")
	}
	return nil
}
