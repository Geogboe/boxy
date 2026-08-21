# Secret backend deployment guidance

Boxy keeps the lifecycle event log and secret values separate. Lifecycle
events contain resource and pool identifiers only. Guest credentials are
stored through the explicitly selected server-owned backend.

Configure one of these backends under `server.secrets`:

- `dpapi` on Windows Server. Values are encrypted with machine-scope Windows
  DPAPI and stored in the configured file. The service account and filesystem
  ACL still control who can use the Boxy process and read the file.
- `keyring` for local development or a host with an available OS keychain.
  This is not assumed to exist in a Windows service, container, OKD pod, or
  K3s node.
- `file` for portable Linux/container deployments. Boxy creates the parent
  directory with mode `0700` and the file with mode `0600` where the platform
  supports Unix mode bits. Windows deployments should use filesystem ACLs;
  the file backend does not pretend that Unix mode bits enforce ACLs there.

There is no implicit backend and no fallback chain. A pool that uses a guest
personalization capability must have a usable backend before the server starts.
This makes a missing keychain, unwritable PVC, or incorrect Windows service
ACL a startup/configuration failure instead of a silent plaintext fallback.

## Containers and Kubernetes

Mount a writable, durable directory for the file backend and restrict it to
the Boxy service identity. Do not mount a read-only Kubernetes Secret directly
as the backend: pool admission and allocation update and delete values. A
Kubernetes Secret may hold operator input for a deliberate migration or
bootstrap command, but the runtime backend should be a protected writable
volume (or a future external secret integration).

The reference examples show the volume and path shape without embedding a
credential. Replace `${BOXY_TEST_PASSWORD}` only in a controlled test fixture
or provide real input through your deployment's secret-management workflow.
