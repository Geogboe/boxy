# devfactory-containers

This example uses Boxy's simulated container provider, with both a Linux
container profile and Windows-friendly `serve.ps1` startup. It is useful for
trying the lifecycle and package boundaries without a Docker daemon, but the
simulator does not validate real `apk`, `apt`, `winget`, or Chocolatey
installation behavior.

For a real Windows guest, a package manifest can use either supported Windows
manager:

```yaml
packages:
  windows-tools:
    builtin: package-manager
    version: 1.0.0
    scopes: [resource]
    events: [provision]
    inputs:
      parameters:
        manager: winget # or chocolatey
        packages: [Git.Git]
```

The selected executable (`winget` or `choco`) must already be installed and
available to the configured guest credential. Its sources require network
access, and installed versions follow those sources because this package does
not pin versions. Missing managers and nonzero installer exits fail the
package operation; Boxy does not elevate, bootstrap, upgrade, or auto-detect a
manager.

Use a real Windows guest smoke test before relying on these packages in a
production pool. See [`docs/package-artifacts.md`](../../docs/package-artifacts.md)
for the shared contract.
