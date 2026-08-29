package resourcepack

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func TestEnginePlanSkipsAppliedPackageBeforeEventValidation(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	if err := reg.Publish(context.Background(), packageArtifact(`
name: app3
version: 1.0.0
method: powershell
scopes: [resource]
events: [provision]
inputs:
  inline: Write-Output baseline
`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	engine := Engine{Registry: reg}
	first, err := engine.Plan(context.Background(), Request{
		Event:      EventProvision,
		Scope:      ScopeResource,
		References: []string{"app3@1.0.0"},
	})
	if err != nil {
		t.Fatalf("initial Plan: %v", err)
	}
	if len(first.Packages) != 1 {
		t.Fatalf("initial planned package count = %d, want 1", len(first.Packages))
	}

	second, err := engine.Plan(context.Background(), Request{
		Event:      EventPromotion,
		Scope:      ScopeResource,
		References: []string{"app3@1.0.0"},
		Applied:    []AppliedPackage{{Reference: "app3@1.0.0", InputDigest: first.Packages[0].InputDigest}},
	})
	if err != nil {
		t.Fatalf("already-applied Plan: %v", err)
	}
	if len(second.Packages) != 0 {
		t.Fatalf("already-applied planned package count = %d, want 0", len(second.Packages))
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

func TestEnginePlanResolvesDependenciesBeforeDependentsInStableOrder(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	for _, spec := range []struct {
		name string
		deps []string
	}{
		{name: "root", deps: []string{"shared@1.0.0", "leaf@1.0.0"}},
		{name: "shared", deps: []string{"leaf@1.0.0"}},
		{name: "leaf"},
		{name: "second"},
	} {
		manifest := fmt.Sprintf("name: %s\nversion: 1.0.0\nmethod: shell\nscopes: [resource]\nevents: [provision]\n", spec.name)
		if len(spec.deps) > 0 {
			manifest += "dependencies:\n"
			for _, dep := range spec.deps {
				manifest += "  - " + dep + "\n"
			}
		}
		if err := reg.Publish(context.Background(), artifact.Artifact{
			Type:     artifact.ArtifactTypePackage,
			Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: spec.name, Version: "1.0.0"},
			Manifest: []byte(manifest),
		}); err != nil {
			t.Fatalf("Publish %s: %v", spec.name, err)
		}
	}

	plan, err := (Engine{Registry: reg}).Plan(context.Background(), Request{
		Event: EventProvision, Scope: ScopeResource,
		References: []string{"root@1.0.0", "shared@1.0.0", "second@1.0.0", "root@1.0.0"},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := make([]string, 0, len(plan.Packages))
	for _, planned := range plan.Packages {
		got = append(got, planned.Reference)
	}
	want := []string{"leaf@1.0.0", "shared@1.0.0", "root@1.0.0", "second@1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planned references = %v, want %v", got, want)
	}
}

func TestEnginePlanReportsMissingDependencyAndFullCycle(t *testing.T) {
	reg := artifact.NewMemoryRegistry()
	publish := func(name, deps string) {
		manifest := fmt.Sprintf("name: %s\nversion: 1.0.0\nmethod: shell\nscopes: [resource]\nevents: [provision]\ndependencies:\n%s", name, deps)
		if err := reg.Publish(context.Background(), artifact.Artifact{
			Type:     artifact.ArtifactTypePackage,
			Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: name, Version: "1.0.0"},
			Manifest: []byte(manifest),
		}); err != nil {
			t.Fatalf("Publish %s: %v", name, err)
		}
	}
	publish("missing-root", "  - missing@1.0.0\n")
	if _, err := (Engine{Registry: reg}).Plan(context.Background(), Request{Event: EventProvision, Scope: ScopeResource, References: []string{"missing-root@1.0.0"}}); err == nil || !strings.Contains(err.Error(), "missing@1.0.0") {
		t.Fatalf("missing dependency error = %v, want missing reference", err)
	}

	publish("cycle-a", "  - cycle-b@1.0.0\n")
	publish("cycle-b", "  - cycle-c@1.0.0\n")
	publish("cycle-c", "  - cycle-a@1.0.0\n")
	_, err := (Engine{Registry: reg}).Plan(context.Background(), Request{Event: EventProvision, Scope: ScopeResource, References: []string{"cycle-a@1.0.0"}})
	if err == nil || !strings.Contains(err.Error(), "cycle-a@1.0.0 -> cycle-b@1.0.0 -> cycle-c@1.0.0 -> cycle-a@1.0.0") {
		t.Fatalf("cycle error = %v, want full deterministic chain", err)
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
	if len(plan.Packages) != 2 {
		t.Fatalf("planned package count = %d, want 2", len(plan.Packages))
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
