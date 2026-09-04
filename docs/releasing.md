# Releasing Boxy

Boxy releases are assembled as batches on an integration branch. Keep each
issue's work in conventional commits and merge the aggregate pull request with
a merge commit whose body is empty. Squashing the batch turns many useful
entries into one opaque release-note line, while a conventional merge preserves
the individual Features, Bug Fixes, Refactoring, Performance, UI, Testing,
Build, CI, and Documentation sections.

Release Please owns `CHANGELOG.md` and the GitHub release notes. The repository
configuration intentionally does not skip changelog generation. Do not hand
edit the generated changelog in a feature pull request.

Before opening the aggregate pull request:

```powershell
task agent:ci:validate
```

When merging the aggregate pull request, use the repository's merge strategy
with an empty merge body so the merge commit is not parsed as a second
Conventional Commit. Merge the generated Release Please pull request only
after the batch is substantial and its notes have been reviewed.
