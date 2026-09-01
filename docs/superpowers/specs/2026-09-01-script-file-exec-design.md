# Script-file sandbox execution (#259)

## Contract

`ExecOperation` keeps the existing `Command []string` form and gains an
optional `Script`. A request contains either a command or a script, never
both. A script carries the raw UTF-8/byte payload, its lowercase SHA-256
digest, an interpreter (`auto`, `powershell`, or `sh`), and trailing arguments
as a JSON array. The server limits the payload to 4 MiB and recomputes the
digest before dispatching it to an agent.

The CLI supports both `--script-file path -- args...` and `-- @path args...`.
The file is read once, hashed locally, and encoded as the request's byte
payload. Script bytes are not logged or persisted in control-plane state;
provider-side records may retain only the digest and interpreter metadata.

## Interpreter selection

`auto` is resolved at the provider boundary. Hyper-V reads the guest metadata
written during provisioning, Docker uses its inspected image/platform hint,
and Devfactory uses its configured profile. An ambiguous provider result is an
error that names `--interpreter` as the remedy. Explicit interpreters are
validated against the guest platform.

## Guest cache

Each guest has a private cache directory and a digest-only filename. Execution
first probes for the digest. A miss stages bytes to a unique temporary file,
sets private permissions, and atomically renames it into place. The cache is
bounded to 64 files and 32 MiB; oldest files are removed after installation.
The cache is guest-local and therefore disappears with the provider resource.
Concurrent misses use different temporary names and cannot expose a partial
final file.

Docker stages through an attached stdin stream so the script is not placed in
a command-line argument. SSH and PowerShell Direct use the same cache
protocol through their guest command channels. The existing streaming and
buffered execution paths then invoke the cached file and preserve exit codes.
