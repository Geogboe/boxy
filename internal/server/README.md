# internal/server

Purpose: compose and run the Boxy service (storage, providers, pool/sandbox managers, API) so CLI commands remain thin.

Key parts:

- `service.go`: Start/Stop lifecycle. Opens storage, builds encryptor, builds provider registry (local + remote agents), starts pool managers, sandbox manager, and API.
- `providers.go`: Availability-aware provider registry assembly (docker health-check, scratch/hyperv only if requested).
- `remote_agents.go`: Registers remote providers from config into the registry.

Non-goals:

- Flag parsing or CLI concerns.
- Core domain logic (lives under internal/core).
- UI/printing.
