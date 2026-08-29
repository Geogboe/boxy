package resourcepack

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/artifact"
)

func packageArtifact(manifest string) artifact.Artifact {
	return artifact.Artifact{
		Kind:     artifact.KindPackage,
		Ref:      artifact.Ref{Kind: artifact.KindPackage, Name: "app3", Version: "1.0.0"},
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
			Kind:     artifact.KindPackage,
			Ref:      artifact.Ref{Kind: artifact.KindPackage, Name: name, Version: "1.0.0"},
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

type recordingExecutor struct {
	operations []Operation
}

func (e *recordingExecutor) Execute(_ context.Context, _ Target, op Operation) error {
	e.operations = append(e.operations, op)
	return nil
}
