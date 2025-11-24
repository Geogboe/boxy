# scratch/shell provider

Lightweight filesystem-backed scratch workspace provider. Resources are directories with generated connection scripts; no processes or network isolation are managed.

Key behaviors:

- Provision: create workspace directories and resource metadata files.
- Allocate: write sandbox metadata, connect script, and env file.
- Health: check directory existence, metadata files, and free space.
- Destroy: remove the resource directory tree.

This provider is intended for fast, local scratch use cases and is not a security boundary.
