# Resource templates, resource packages, artifact sources, and promotion

Status: Implementation-ready
Date: 2026-08-28
Tracks: #234, #268
Vocabulary: [Boxy domain language](../../domain-language.md)

## Purpose

Extend Boxy from a pool-only resource shape to a reusable resource-template
model. A template describes the desired state of a resource independently of
the pool policy that preheats it. A resource package is an immutable,
parameterized artifact that can be applied to a resource at a lifecycle event.
Templates let a derived pool promote a ready resource from an ancestor pool
and apply only the package delta instead of building the final image from
scratch.

This release also includes #268, a small independent CLI improvement that lets
OIDC loopback login use a fixed callback port for providers that require an
exact redirect URI.

## Decisions

### User-facing vocabulary and configuration

The existing `pools:` list remains valid and keeps its current `name`, `type`,
`provider`, `config`, `agent`, and policy fields. It gains an optional
`template:` reference. Reusable definitions are user-facing `templates:`;
the internal domain type is `model.ResourceTemplate`.

Templates have a single optional `extends:` parent. A child inherits the
parent's provider/resource shape and package references, then appends its own
package references. There is no synthetic root. A cycle or missing parent is
a configuration error before the daemon starts.

Example daemon configuration:

```yaml
artifact_stores:
  images:
    type: s3
    endpoint: https://s3.example.test
    bucket: boxy-images
    access_key: env:BOXY_IMAGES_ACCESS_KEY
    secret_key: env:BOXY_IMAGES_SECRET_KEY

sources:
  windows-2022:
    store: images
    path: base/windows-2022.vhdx
    digest: sha256:0123456789abcdef...
    format: hyperv-vhdx

templates:
  windows-base:
    type: vm
    provider: hyperv-local
    source: windows-2022

  windows-apps12:
    extends: windows-base
    packages:
      - app1@1.0.0
      - app2@1.0.0

  windows-apps123:
    extends: windows-apps12
    packages:
      - app3@1.0.0

pools:
  - name: apps12
    template: windows-apps12
    policy:
      preheat:
        min_ready: 2
```

Existing inline pool configuration remains supported. A pool with no
`template:` behaves exactly as it does today. A template may be used by more
than one pool; each pool still owns its own inventory and policy.

Sandbox files keep their existing top-level `resources:` list. Each resource
request may add allocation package references:

```yaml
name: test-lab

resources:
  - pool: apps123
    count: 1
    packages:
      - debugging-tools@1.0.0
```

Package manifests use the following stable terms:

```yaml
name: app3
version: 1.0.0
method: powershell
scopes: [resource]
events: [provision, promotion]

inputs:
  script: install-app3.ps1
  parameters:
    Foo: bar
    Boo: baz
```

`method` describes the executor, not a pool provisioner. This avoids the
existing `internal/pool.Provisioner` naming collision. `scopes` is always a
list and declares where the package is legal: `resource`, `allocation`, or
both. `events` declares when it is applied: `provision`, `promotion`, or
`allocation`. A resource template can reference only packages that include
`resource`; a sandbox resource request can reference only packages that
include `allocation`. An event outside the package's declared scope is a
validation error.

This release implements `shell` and `powershell`. DSC and Ansible are
recognized as reserved future methods but fail with a clear unsupported-method
error until their bootstrap requirements have a separate design.

### Resource package engine

`pkg/resourcepack` is a public, provider-neutral package. It owns package
manifest validation, canonical identity, parameter resolution, desired-set
planning, and applied-package records. It does not own guest transport,
provider selection, credentials, or pool policy.

The public seam is:

- `Engine.Plan` is side-effect free. It resolves package references, validates
  scope/event/method, applies defaults and overrides, canonicalizes inputs,
  and returns a deterministic plan and package identities.
- `Engine.Apply` executes a previously planned set through an injected
  executor and returns applied identities. The executor is the only side
  effecting dependency.
- Parameter precedence is package defaults, then resource/template overrides,
  then lifecycle-hook overrides, then sandbox-request overrides. Secret
  values are accepted only as secret references and are never persisted in
  plans, applied records, or logs.
- Applying an already recorded package identity with the same inputs is a
  no-op. The engine does not attempt guest drift detection.
- Package removal and inverse operations are explicitly out of scope. Failed
  application does not attempt an automatic rollback script.

The engine emits provider-neutral operation data containing package identity,
method, materialized input references, and resolved parameters. It never emits
package registry policy to an agent.

An operation that names `inputs.script` must have matching package content
materialized as a blob, unless the operation also supplies `inputs.inline`.
The executor must not treat an unmaterialized script name as a path that already
exists in the guest.

### Artifact registry and sources

`pkg/artifact` is the single public `Registry` facade for typed artifacts. It
may use separate internal stores/caches, but callers do not choose between
multiple registry interfaces.

Sources are catalog records for externally owned immutable bytes. A source
contains a named store, path/key, digest, format, and optional provider/OS
metadata. Boxy does not require copying the source into a Boxy-managed store.
The configured store may provide a direct pull descriptor to the provider or
agent. If it cannot, the server streams the verified bytes to the executor.

The first store adapters are local filesystem and S3-compatible object stores.
Credentials are configuration references such as `env:NAME`; raw credentials
must not appear in persisted configuration, package records, plans, or logs.

Published packages use one immutable artifact identity containing a manifest
and content-addressed blobs. `boxy package build` creates the artifact,
`boxy package publish` writes it to an artifact store, and `boxy package
inspect` verifies and displays its manifest. Inline package content is a local
build input and does not implicitly publish.

### Agents and providers

Agents remain dumb pipes. The server resolves the template lineage, package
set, artifact references, credentials, lifecycle event, and policy. Embedded
and remote agents carry opaque provider operations and execute them through
their local provider driver. They do not load package manifests, decide which
packages apply, or access the package registry as a policy engine.

Hyper-V uses the existing Windows guest transport for PowerShell and the
existing Linux guest transport for shell. Devfactory supplies deterministic
simulation of those two methods for tests and local integration checks; it is
not a Hyper-V emulator.

### Provision, promotion, and allocation

Resource-scoped packages run after a resource is created and admitted, at the
`provision` event. A derived template may promote an eligible ready resource
from an ancestor pool and apply only the package identities absent from the
resource's applied set at the `promotion` event. Allocation-scoped packages
run for each selected sandbox resource at the `allocation` event.

Promotion is explicit and one-directional. A resource is marked
`promoting`, receives a pending destination ownership record, and is removed
from ordinary source selection while work is in progress. The source pool's
minimum-ready protection is respected; if no eligible surplus resource exists,
the destination provisions from scratch.

`OriginPool` remains immutable provenance. A separate current/pending pool
ownership field controls inventory membership and capacity accounting, so a
promoted resource is no longer counted against the source pool's
`max_total` while its origin remains available for provenance and credential
authorization.

Promotion rotates the destination bootstrap credential first. The resulting
resource credential is then used for package application. Only after all
required packages succeed does Boxy commit current ownership and mark the
resource ready in the destination pool. Any failure quarantines or destroys
the resource through the existing fail-closed cleanup path; it never returns
the resource to the source pool with ambiguous ownership or credentials.

The existing states and failure behavior remain unchanged for ordinary
provisioning, allocation, recycling, and destruction. The new `promoting`
state is observable through the model, REST resource output, and persisted
state. Configurable circuit breakers, failure thresholds, and forensic
retention are deferred to a separate issue.

### OIDC loopback callback port (#268)

`boxy login --oidc --web` gains `--oidc-loopback-port`, an integer flag whose
default is `0`. Zero preserves kernel-selected dynamic ports. A nonzero value
must be in the TCP port range and is bound only as `127.0.0.1:<port>`.

The selected listener port is used in the OAuth redirect URI. The change does
not alter device-code login, PKCE/S256, public-client behavior, browser
launching, callback state validation, or token exchange. Documentation must
show that an exact-match issuer registers
`http://127.0.0.1:<port>/callback` exactly.

## Public interfaces and persisted data

- Add `model.ResourceTemplate`, template references to `config.PoolSpec`, and
  typed template/package/source configuration models.
- Add `ResourceStatePromoting` and current/pending ownership fields to
  `model.Resource`. Existing JSON without those fields remains readable.
- Add `pkg/resourcepack` and `pkg/artifact` public contracts. Do not add
  package semantics to `providersdk.Driver` or `agentsdk.Agent`.
- Extend the remote operation envelope only enough to carry opaque generic
  package execution/materialization data; remote decoding must remain
  provider-operation based.
- Extend `SandboxResource` parsing and the sandbox request model with
  allocation package references. Existing requests without packages are
  unchanged.
- Update the generated configuration schema and all user-facing examples.

## Failure and compatibility rules

- Unknown configuration fields, duplicate names, missing references, cycles,
  invalid digests, invalid scopes/events, and unsupported methods fail during
  configuration validation.
- Old pool-only configurations and old persisted resources continue to work.
- No raw secrets are serialized or logged.
- Package application is idempotent by immutable package identity plus
  canonical resolved inputs.
- Existing quarantine, cleanup retry, and capped provisioning backoff remain
  the only automated failure controls in this release.

## Implementation order and acceptance

1. This document, the glossary, and the related ADRs are updated and reviewed
   first. No production code or test implementation begins before the
   contract is complete in the repository.
2. Add failing tests for config/template resolution, graph traversal, artifact
   registry behavior, package planning/application, and #268.
3. Implement the public packages and configuration models, then integrate
   provisioning, promotion, allocation, and remote opaque operations.
4. Implement shell/PowerShell Hyper-V adapters and devfactory simulation.
5. Add CLI/package workflows, generated schema, examples, lifecycle/API docs,
   and the OIDC fixed-port documentation.
6. Acceptance requires focused tests, `go test ./...`, `task lint`, `task
   build`, devfactory smoke coverage, and `task ci:validate` with the race
   portion run through WSL on Windows ARM64.

## Explicit non-goals

- DSC or Ansible execution.
- Package removal, inverse configuration, or guest drift detection.
- Multi-parent template composition or package dependency graphs.
- Provider-specific package policy inside agents.
- Managed copying of every external source into Boxy storage.
- Generalized pool circuit breakers, failure thresholds, or forensic retention.
