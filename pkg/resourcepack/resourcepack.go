// Package resourcepack plans and applies immutable, parameterized resource
// configuration packages. It deliberately has no provider or guest-transport
// dependency: those are supplied by the executor at the application boundary.
// Compile turns the supported package-manager package into the same inline
// shell or PowerShell package format used by ordinary manifests.
package resourcepack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/Geogboe/boxy/pkg/artifact"
	"gopkg.in/yaml.v3"
)

type Method string

const (
	MethodShell      Method = "shell"
	MethodPowerShell Method = "powershell"
	MethodDSC        Method = "dsc"
	MethodAnsible    Method = "ansible"
)

// Supported reports whether Boxy currently has an executor for the method.
// DSC and Ansible remain valid manifest vocabulary for future implementations,
// but cannot be used by the current lifecycle executor.
func (m Method) Supported() bool {
	return m == MethodShell || m == MethodPowerShell
}

type Scope string

const (
	ScopeResource   Scope = "resource"
	ScopeAllocation Scope = "allocation"
)

type Event string

const (
	EventProvision  Event = "provision"
	EventPromotion  Event = "promotion"
	EventAllocation Event = "allocation"
)

var (
	ErrRegistryRequired    = errors.New("resource package registry is required")
	ErrExecutorRequired    = errors.New("resource package executor is required")
	ErrUnsupportedMethod   = errors.New("resource package method is not supported")
	ErrInvalidManifest     = errors.New("resource package manifest is invalid")
	ErrInvalidPackageScope = errors.New("resource package scope is invalid for event")
)

// Manifest is the typed content of a published resource package.
type Manifest struct {
	Name     string         `json:"name" yaml:"name"`
	Version  string         `json:"version" yaml:"version"`
	Builtin  string         `json:"builtin,omitempty" yaml:"builtin,omitempty"`
	Method   Method         `json:"method" yaml:"method"`
	Scopes   []Scope        `json:"scopes" yaml:"scopes"`
	Events   []Event        `json:"events" yaml:"events"`
	Defaults map[string]any `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("%w: name and version are required", ErrInvalidManifest)
	}
	if m.Builtin != "" {
		if m.Builtin != BuiltinPackageManager {
			return fmt.Errorf("%w: unsupported builtin %q", ErrInvalidManifest, m.Builtin)
		}
		if m.Method != "" {
			return fmt.Errorf("%w: builtin %q must not declare method", ErrInvalidManifest, m.Builtin)
		}
		if len(m.Defaults) != 0 {
			return fmt.Errorf("%w: builtin %q does not support defaults", ErrInvalidManifest, m.Builtin)
		}
		if err := validatePackageManagerInputs(m.Inputs); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidManifest, err)
		}
	} else {
		switch m.Method {
		case MethodShell, MethodPowerShell, MethodDSC, MethodAnsible:
		default:
			return fmt.Errorf("%w: unsupported method %q", ErrInvalidManifest, m.Method)
		}
	}
	if len(m.Scopes) == 0 {
		return fmt.Errorf("%w: at least one scope is required", ErrInvalidManifest)
	}
	for _, scope := range m.Scopes {
		if scope != ScopeResource && scope != ScopeAllocation {
			return fmt.Errorf("%w: unknown scope %q", ErrInvalidManifest, scope)
		}
	}
	if len(m.Events) == 0 {
		return fmt.Errorf("%w: at least one event is required", ErrInvalidManifest)
	}
	for _, event := range m.Events {
		if event != EventProvision && event != EventPromotion && event != EventAllocation {
			return fmt.Errorf("%w: unknown event %q", ErrInvalidManifest, event)
		}
	}
	return nil
}

// Request describes one lifecycle package application.
type Request struct {
	Target     Target
	Event      Event
	Scope      Scope
	References []string
	Overrides  map[string]any
	Applied    []AppliedPackage
}

// Target identifies the concrete resource receiving an operation. Provider
// and credential details remain opaque to this package and may be carried by
// the executor's adapter.
type Target struct {
	ResourceID string
	Provider   string
	AgentID    string
}

type AppliedPackage struct {
	Reference   string `json:"reference" yaml:"reference"`
	InputDigest string `json:"input_digest" yaml:"input_digest"`
}

type PlannedPackage struct {
	Reference   string
	Manifest    Manifest
	Inputs      map[string]any
	Parameters  map[string]any
	InputDigest string
	Content     []byte
}

type Plan struct {
	Target   Target
	Event    Event
	Packages []PlannedPackage
}

type Operation struct {
	Reference   string
	Method      Method
	Inputs      map[string]any
	Parameters  map[string]any
	InputDigest string
	Content     []byte
}

// Executor is the only side-effecting dependency of Engine.Apply.
type Executor interface {
	Execute(ctx context.Context, target Target, operation Operation) error
}

type Engine struct {
	Registry artifact.Registry
}

func (e Engine) Plan(ctx context.Context, request Request) (Plan, error) {
	if e.Registry == nil {
		return Plan{}, ErrRegistryRequired
	}
	if request.Event == "" {
		return Plan{}, fmt.Errorf("%w: event is required", ErrInvalidManifest)
	}
	if request.Scope == "" {
		request.Scope = scopeForEvent(request.Event)
	}
	if request.Scope != ScopeResource && request.Scope != ScopeAllocation {
		return Plan{}, fmt.Errorf("%w: unknown scope %q", ErrInvalidManifest, request.Scope)
	}
	plan := Plan{Target: request.Target, Event: request.Event}
	for _, rawRef := range request.References {
		ref, err := artifact.ParseRef(rawRef)
		if err != nil {
			return Plan{}, err
		}
		ref.Type = artifact.ArtifactTypePackage
		value, err := e.Registry.Resolve(ctx, ref)
		if err != nil {
			return Plan{}, fmt.Errorf("resolve package %q: %w", rawRef, err)
		}
		manifest, err := decodeManifest(value.Manifest)
		if err != nil {
			return Plan{}, fmt.Errorf("decode package %q: %w", rawRef, err)
		}
		manifest, err = Compile(manifest)
		if err != nil {
			return Plan{}, fmt.Errorf("package %q: %w", rawRef, err)
		}
		if manifest.Name != ref.Name || manifest.Version != ref.Version {
			return Plan{}, fmt.Errorf("%w: package %q manifest identity is %s@%s", ErrInvalidManifest, rawRef, manifest.Name, manifest.Version)
		}
		if !containsScope(manifest.Scopes, request.Scope) {
			return Plan{}, fmt.Errorf("%w: package %q does not declare scope %q", ErrInvalidPackageScope, rawRef, request.Scope)
		}
		if !containsEvent(manifest.Events, request.Event) {
			return Plan{}, fmt.Errorf("%w: package %q does not declare event %q", ErrInvalidPackageScope, rawRef, request.Event)
		}
		if !manifest.Method.Supported() {
			return Plan{}, fmt.Errorf("%w: package %q uses method %q", ErrUnsupportedMethod, rawRef, manifest.Method)
		}
		parameters := mergeMaps(manifest.Defaults, inputParameters(manifest.Inputs))
		maps.Copy(parameters, request.Overrides)
		digest, err := digestInputs(manifest.Inputs, parameters)
		if err != nil {
			return Plan{}, fmt.Errorf("canonicalize package %q inputs: %w", rawRef, err)
		}
		if alreadyApplied(request.Applied, ref.String(), digest) {
			continue
		}
		plan.Packages = append(plan.Packages, PlannedPackage{
			Reference:   ref.String(),
			Manifest:    manifest,
			Inputs:      cloneMap(manifest.Inputs),
			Parameters:  parameters,
			InputDigest: digest,
			Content:     packageContent(manifest, value),
		})
	}
	return plan, nil
}

func (e Engine) Apply(ctx context.Context, plan Plan, executor Executor) ([]AppliedPackage, error) {
	if executor == nil {
		return nil, ErrExecutorRequired
	}
	applied := make([]AppliedPackage, 0, len(plan.Packages))
	for _, planned := range plan.Packages {
		err := executor.Execute(ctx, plan.Target, Operation{
			Reference:   planned.Reference,
			Method:      planned.Manifest.Method,
			Inputs:      cloneMap(planned.Inputs),
			Parameters:  cloneMap(planned.Parameters),
			InputDigest: planned.InputDigest,
			Content:     append([]byte(nil), planned.Content...),
		})
		if err != nil {
			return applied, fmt.Errorf("apply package %q: %w", planned.Reference, err)
		}
		applied = append(applied, AppliedPackage{Reference: planned.Reference, InputDigest: planned.InputDigest})
	}
	return applied, nil
}

func decodeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func packageContent(manifest Manifest, value artifact.Artifact) []byte {
	if manifest.Method != MethodShell && manifest.Method != MethodPowerShell {
		return nil
	}
	script, _ := manifest.Inputs["script"].(string)
	if script == "" || len(value.Blobs) == 0 {
		return nil
	}
	content, ok := value.Blobs[script]
	if !ok {
		content, ok = value.Blobs["script"]
	}
	if !ok {
		return nil
	}
	return append([]byte(nil), content...)
}

func scopeForEvent(event Event) Scope {
	if event == EventAllocation {
		return ScopeAllocation
	}
	return ScopeResource
}

func containsScope(scopes []Scope, want Scope) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func containsEvent(events []Event, want Event) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func inputParameters(inputs map[string]any) map[string]any {
	if inputs == nil {
		return nil
	}
	parameters, ok := inputs["parameters"].(map[string]any)
	if !ok {
		return nil
	}
	return parameters
}

func mergeMaps(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	maps.Copy(merged, base)
	maps.Copy(merged, override)
	return merged
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	clone := make(map[string]any, len(values))
	maps.Copy(clone, values)
	return clone
}

func digestInputs(inputs, parameters map[string]any) (string, error) {
	canonical := struct {
		Inputs     map[string]any `json:"inputs,omitempty"`
		Parameters map[string]any `json:"parameters,omitempty"`
	}{Inputs: inputs, Parameters: parameters}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func alreadyApplied(applied []AppliedPackage, reference, digest string) bool {
	for _, record := range applied {
		if record.Reference == reference && record.InputDigest == digest {
			return true
		}
	}
	return false
}
