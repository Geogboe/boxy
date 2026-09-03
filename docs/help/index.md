# Boxy package help

Boxy packages are immutable lifecycle operations applied to existing guests.
They are not a global software catalog, and Boxy does not implicitly install a
package manager. A package manager can be used only when the package declares
it explicitly and the selected executable is available in the guest.

## Package order

Package references run in the order they appear in a template or pool. The
same order is retained through template inheritance: parent references run
before references added by the child. Until Boxy has an explicit dependency
graph, this ordered list is the dependency mechanism.

For example, this Windows template installs Chocolatey through Winget before
running the package that uses Chocolatey:

```yaml
packages: [chocolatey@1.0.0, windows-tools@1.0.0]
```

If an earlier package fails, later packages in that provisioning operation are
skipped. A future dependency-graph design still needs to define missing
dependencies, cycles, deterministic ordering, inheritance, and failure
semantics; see [issue #310](https://github.com/Geogboe/boxy/issues/310).

## Supported managers

The built-in `package-manager` package supports the manager named in its
parameters:

| Manager | Guest executable | Method |
| --- | --- | --- |
| `apt` | `apt-get` | POSIX shell |
| `apk` | `apk` | POSIX shell |
| `winget` | `winget` | PowerShell |
| `chocolatey` | `choco` | PowerShell |

Managers are not interchangeable. Boxy does not detect an alternative,
elevate, retry, bootstrap, upgrade, or silently install a missing manager. An
explicitly declared Winget package may install Chocolatey; that is an ordinary
package operation, not implicit manager bootstrapping.

## Chocolatey through Winget

Copy this declaration into the `packages` section of your configuration. The
official Winget package identifier is
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
```

Reference them in this order:

```yaml
templates:
  windows-base:
    type: vm
    provider: hyperv-local
    packages: [chocolatey@1.0.0, windows-tools@1.0.0]
```

Winget selects the current Chocolatey version from its configured source;
Boxy does not pin that version. The second package then resolves `git` through
Chocolatey's configured source.

## Guest requirements

Package commands run with the configured guest credential. The credential
must have the privilege required by the guest manager: typically root or an
equivalent administrative account for `apt`/`apk`, and an account permitted to
install software for `winget`/`choco`. Boxy does not change the credential or
elevate it for a package.

The guest must have Internet access to the manager's normal repositories or
sources. Package versions follow those sources because these declarations do
not pin versions. An offline guest fails through the selected manager and can
retry on a later provisioning pass.

## Failure and idempotency

Missing manager executables and nonzero manager exits fail the package
operation. A package is recorded as applied only after its command succeeds;
if installation fails, the package can be retried and later ordered packages
are not run in that pass.

Successful applications use the package reference and input digest for normal
idempotency. Repeating provisioning with the same package inputs skips the
already-applied package. Changing the package list creates a new input digest
and runs the package again.
