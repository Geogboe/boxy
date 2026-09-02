# Boxy domain language

This glossary is the shared vocabulary for Boxy's resource orchestration and
configuration features. Use these terms consistently in code, configuration,
API responses, documentation, issues, and tests.

## Core objects

### Resource

A concrete provisioned instance that Boxy can track and allocate: for example,
a VM, container, share, network, or database. A resource has a provider handle,
a lifecycle state, and immutable provisioning provenance. Resources are
single-use after sandbox allocation; they are not returned to a pool.

### Pool

A named inventory of homogeneous, unused resources. A pool owns readiness and
capacity policy such as `min_ready`, `max_total`, draining, and recycling. A
pool is runtime inventory, not the reusable desired resource definition.

### Template

A named, reusable desired shape for a resource. A template describes the
resource type/provider/source and the resource packages needed to reach the
desired state. Templates may extend one parent template. In Go and the domain
model, the object is `ResourceTemplate`.

### Sandbox

A user-facing composition of one or more allocated resources. Sandbox resource
requests may add packages that apply only during allocation.

### Package

An immutable, parameterized configuration artifact that changes a resource
toward a desired state. A package has a method, allowed scopes, lifecycle
events, inputs, and a content identity. In this release methods are `shell`
and `powershell`.

The `package-manager` built-in is a declarative package recipe. Its only
parameters are a supported manager (`apt`, `apk`, `winget`, or `chocolatey`)
and a non-empty list of safe package IDs. Compilation derives the method and
inline script; the stored artifact no longer needs the built-in marker. The
manager must already exist in the guest and uses the configured guest
credential. Network access, repository-provided versions, and sufficient
guest privilege remain the operator's responsibility.

Do not call a package a script. A script may be one input inside a package, but
the package also carries identity, parameters, scope, event, and artifact
metadata.

### Source

A catalog record for immutable bytes used to create a resource, such as a VHDX
image. A source contains a store reference, path/key, digest, format, and
optional OS/provider metadata. Boxy records the source and verifies its digest;
the source bytes may remain in an externally owned store.

### Artifact

An immutable, addressable payload with a digest. Sources and published resource
packages are different typed artifact types that share retrieval and identity
infrastructure.

### Store

A physical or protocol-specific location that reads and writes artifacts, such
as a local filesystem or S3-compatible object store. Store configuration owns
endpoint and credential-reference details; it does not define resource or
package policy.

### Registry

The logical `pkg/artifact.Registry` facade used to resolve, verify, publish,
and retrieve typed artifacts. The registry may use multiple stores and caches
internally. Callers use one registry abstraction rather than separate source
and package registries.

## Execution and lifecycle

### Agent

The execution transport and host-local runtime. An agent may be embedded in the
daemon or connected remotely. Agents are dumb pipes: they carry opaque
provider operations and materialization requests but do not resolve package
policy or template lineage.

### Driver

Provider-specific code that talks to an external resource system such as
Hyper-V, Docker, or devfactory. A driver creates, reads, updates, allocates,
and deletes resources and may execute the provider-neutral operations sent by
the agent.

### Executor

The injected side-effecting seam used by `pkg/resourcepack` after planning.
An executor materializes inputs and runs a package operation against a target;
it is not responsible for deciding which packages should run.

### Provision

The lifecycle event that applies resource-scoped packages to a newly created
resource before it becomes ready in its pool.

### Promotion

A one-directional transition that moves an eligible ready resource from an
ancestor pool toward a derived template. Promotion applies only package
identities not already recorded on the resource. It is not recycling and is
not returning an allocated resource to a pool.

### Allocation

The transition that takes a ready resource from a pool into a sandbox. It may
apply allocation-scoped packages and sandbox-specific parameters. Allocated
resources are not returned to pools.

### Ownership

The pool currently responsible for inventory membership and capacity
accounting. During promotion, pending ownership records the intended
destination. Ownership is distinct from `OriginPool`, which is immutable
provenance.

### OriginPool

The immutable pool that originally provisioned a resource. It remains useful
for provenance and credential authorization even after current ownership
changes through promotion.

### Applied package record

The durable identity and canonical input digest recorded after a package
successfully applies. Desired-set comparison uses this record for idempotency;
it is not guest drift detection.

## Configuration terms

### Method

The execution mechanism named by a package, such as `shell` or `powershell`.
Use `method`, not `provisioner`, for this field. `Provisioner` already means
resource creation in `internal/pool`.

For a `package-manager` built-in, Boxy derives `shell` for `apt`/`apk` and
`powershell` for `winget`/`chocolatey`.

### Scope

Where a package is legal: `resource` and/or `allocation`. The value is always a
list, for example `scopes: [resource, allocation]`.

### Event

When a package is applied: `provision`, `promotion`, or `allocation`. Scope
and event are separate: scope authorizes the target type, while event selects
the lifecycle moment.

### Input

A named package value or supporting file reference. Inputs are not limited to
command-line arguments because PowerShell scripts and future configuration
methods need structured values and files. Use `parameters` for named values,
not `args`, unless a method specifically requires command-line argument
semantics.

### Secret reference

A non-secret locator such as `env:NAME` that lets an execution boundary obtain
a secret. Raw secret values must never be stored in configuration, plans,
applied records, or logs.

## Naming rules

- Use **template** in user-facing configuration; use `ResourceTemplate` in the
  domain model when a type name is needed.
- Use **package** for the complete immutable configuration artifact; use
  **script** only for a script file or inline script input.
- Use **method** for shell/PowerShell/DSC/Ansible-style execution mechanisms.
- Use **store** for a physical artifact backend and **registry** for the single
  logical artifact facade.
- Use **provision** for creating/configuring pool inventory, **promotion** for
  derived-pool advancement, and **allocation** for sandbox consumption.
- Do not introduce another `Provisioner` meaning for guest configuration.
