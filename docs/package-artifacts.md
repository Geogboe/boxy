# Resource package artifacts

Packages are immutable, versioned configuration artifacts. A package declares
its lifecycle scope and event, then supplies an execution method and inputs.
Build one explicitly with:

```sh
boxy package build --manifest package.yaml --output package.json
boxy package publish --artifact package.json --registry .boxy/registry
# Publish to a configured store (the config must name the store)
boxy package publish --artifact package.json --config boxy.yaml --store guest-artifacts
```

## Built-in package-manager package

For common guest setup, use the built-in package instead of maintaining a
script file:

```yaml
name: developer-tools
version: 1.0.0
builtin: package-manager
scopes: [resource]
events: [provision]
inputs:
  parameters:
    manager: apt # apt, apk, winget, or chocolatey
    packages: [curl, git]
```

To install Chocolatey explicitly through Winget, copy this package declaration
and retain the package-reference order shown below. The official Winget ID is
[`Chocolatey.Chocolatey`](https://github.com/microsoft/winget-pkgs/tree/master/manifests/c/Chocolatey/Chocolatey).

```yaml
packages:
  chocolatey:
    builtin: package-manager
    version: 1.0.0
    scopes: [resource]
    events: [provision]
    inputs:
      parameters:
        manager: winget
        packages: [Chocolatey.Chocolatey]

  windows-tools:
    builtin: package-manager
    version: 1.0.0
    scopes: [resource]
    events: [provision]
    inputs:
      parameters:
        manager: chocolatey
        packages: [git]

templates:
  windows-base:
    type: vm
    provider: hyperv-local
    packages: [chocolatey@1.0.0, windows-tools@1.0.0]
```

The first package installs Chocolatey through Winget; the second uses
Chocolatey. Dependencies are exact immutable references and are resolved
before their dependents:

```yaml
packages:
  windows-tools:
    version: 1.0.0
    dependencies: [chocolatey@1.0.0]
    method: powershell
    scopes: [resource]
    events: [provision]
```

Discovery is stable: inherited/template package declarations retain their
order, each dependency list retains its order, and an exact reference runs
once at its first discovery. Missing references and cycles fail validation
with deterministic messages containing the complete cycle chain. Validation
runs at startup and planning re-checks the graph defensively. With no
dependencies, the historical ordered-reference behavior is unchanged. Apply
still stops at the first failure: successful applications remain recorded,
while the failed package, its dependents, and later packages do not run.

Boxy compiles this declaration into the normal immutable inline-script
package. Linux managers use `shell`; Windows managers use `powershell`.
Package IDs are sorted before compilation; duplicates and shell/PowerShell
metacharacters are rejected. The supported package ID characters are
`A-Z a-z 0-9 . _ + - @ : / =`.

The manager must already be installed in the guest and is run using the
configured guest credential. Boxy does not auto-detect another manager,
elevate, bootstrap, upgrade, or silently install one. The selected manager's
normal repositories or sources must be reachable, so package application may
require network access. Versions are resolved by those repositories; this
first package does not pin versions.

The generated operation fails if the manager is absent or returns a nonzero
exit code. The resource is not recorded as having applied the package after a
failure. Successful applications use the existing applied-package reference
and input digest for idempotency; a changed package list produces a new digest
and is applied as a new package input.

The package is compiled during configuration validation, in-config registry
construction, `boxy package build`, and package planning. Agents receive only
the resulting provider-neutral execution operation and credential; they do not
receive package policy or resolve package references.

Manager bootstrapping is deliberately deferred. Any future opt-in bootstrap
feature must specify installer provenance, checksums or signatures, privilege,
network-failure handling, and rollback first.

## Artifact stores and source delivery

`artifact_stores` supports `local`, `filesystem`, and S3-compatible stores.
S3 endpoints may set `bucket`, `path`, `region`, and `path_style`. Credentials
must be references such as `env:BOXY_S3_ACCESS_KEY`, never literal secrets:

```yaml
artifact_stores:
  guest-artifacts:
    type: s3
    endpoint: https://s3.example.invalid
    bucket: boxy-artifacts
    path: production
    region: us-east-1
    path_style: true
    access_key: env:BOXY_S3_ACCESS_KEY
    secret_key: env:BOXY_S3_SECRET_KEY
sources:
  windows-2022:
    store: guest-artifacts
    path: images/windows-2022.vhdx
    digest: sha256:<64 hex characters>
    format: vhdx
    provider: hyperv
```

Source registration remains declarative. Upload source bytes with the
provider's storage tooling, add the store/source/template entries to
`boxy.yaml`, and restart or reload Boxy. At provisioning time Boxy gives the
provider a provider-neutral descriptor with a 15-minute, object-specific
signed pull URL. Source bytes do not transit or persist in the control plane.
Providers download directly, verify SHA-256, and fail closed on expiry,
missing objects, download errors, cancellation, or digest mismatch. Hyper-V
accepts local paths and VHD/VHDX descriptors; Docker continues to use image
references and custom registries and rejects raw source descriptors.

## Deployment examples

The repository includes complete or intentionally marked reference examples:

- [`examples/compose-secrets`](../examples/compose-secrets/README.md) shows a
  Docker Compose deployment with a file-backed secret store.
- [`examples/k3s-secrets`](../examples/k3s-secrets/README.md) shows the ConfigMap,
  PVC, and non-root deployment shape for K3s/OKD.
- [`examples/oidc-keycloak`](../examples/oidc-keycloak/README.md) runs local
  OIDC login tests against Keycloak and documents the development credentials.

These examples cover deployment wiring and secret handling; they do not
bootstrap guest package managers.
