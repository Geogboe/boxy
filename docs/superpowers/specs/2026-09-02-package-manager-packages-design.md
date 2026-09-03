# Built-in package-manager packages

## Status

Implemented for the v0.1.61 prerelease slice.

## Goal

Allow a package manifest to declare a small, portable package-manager package
without embedding a shell script:

```yaml
packages:
  developer-tools:
    builtin: package-manager
    version: 1.0.0
    scopes: [resource]
    events: [provision]
    inputs:
      parameters:
        manager: apk
        packages: [curl, git]
```

The package compiler turns this declaration into the existing immutable inline
script package shape. It selects `shell` for `apt` and `apk`, and `powershell`
for `winget` and `chocolatey`. The compiled manifest has no `builtin` marker
and contains its generated script in `inputs.inline`.

## Supported managers and commands

Only managers already present in the guest are supported:

| Manager | Guest method | Behavior |
| --- | --- | --- |
| `apt` | `shell` | Check for `apt-get`, refresh indexes, then install packages non-interactively with `--no-install-recommends`. |
| `apk` | `shell` | Check for `apk`, then install packages with `--no-cache`. |
| `winget` | `powershell` | Check for `winget`, then install each exact ID silently while accepting the source and package agreements. |
| `chocolatey` | `powershell` | Check for `choco`, then install each package with `--yes` and `--no-progress`. |

The generated script fails when the selected executable is missing or a
manager invocation returns a nonzero exit code. It does not detect an
alternative manager, elevate, bootstrap, upgrade, or otherwise install a
manager. The existing package executor supplies the configured guest
credential to the provider; no package-manager policy crosses the agent
boundary.

## Validation and determinism

`manager` and `packages` are the only accepted package parameters. The manager
name is case-insensitive and is normalized to the canonical lower-case name.
At least one package is required. Package IDs must be non-empty, contain no
whitespace or shell/PowerShell metacharacters, and use only the portable
identifier characters `A-Z a-z 0-9 . _ + - @ : / =`. Duplicate IDs are
rejected. IDs are sorted before script generation, so equivalent declarations
produce the same immutable package input digest.

Package versions are intentionally not pinned in this slice. The selected
guest manager resolves the current version available from its configured
repositories. Network access is therefore required at application time for
the manager's normal index/source operations. Package installation runs with
the supplied guest credential and may require administrative/root privilege;
Boxy does not silently change that privilege. A failed command leaves the
resource lifecycle operation failed and does not record the package as
applied.

## Compilation points

Compilation is performed while validating configuration, while building the
in-memory package registry, while running `boxy package build`, and again when
planning a package resolved from an external registry. The repeated compile
is a defensive normalization step; compiled manifests are already unchanged.

## Out of scope

Manager bootstrapping is deferred. A follow-up design must cover installer
provenance, checksums or signatures, privilege transitions, network failures,
and rollback before any bootstrap behavior is added.

Package dependency graphs are also out of scope for this increment. The
ordered package-reference behavior is tracked in issue #310.
