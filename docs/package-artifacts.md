# Resource package artifacts

Packages are immutable, versioned configuration artifacts. A package declares
its lifecycle scope and event, then supplies an execution method and inputs.
Build one explicitly with:

```sh
boxy package build --manifest package.yaml --output package.json
boxy package publish --artifact package.json --registry .boxy/registry
```

## Built-in package-manager recipe

For common guest setup, use the built-in recipe instead of maintaining a
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
first recipe does not pin versions.

The generated operation fails if the manager is absent or returns a nonzero
exit code. The resource is not recorded as having applied the package after a
failure. Successful applications use the existing applied-package reference
and input digest for idempotency; a changed package list produces a new digest
and is applied as a new package input.

The recipe is compiled during configuration validation, in-config registry
construction, `boxy package build`, and package planning. Agents receive only
the resulting provider-neutral execution operation and credential; they do not
receive package policy or resolve package references.

Manager bootstrapping is deliberately deferred. Any future opt-in bootstrap
feature must specify installer provenance, checksums or signatures, privilege,
network-failure handling, and rollback first.

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
