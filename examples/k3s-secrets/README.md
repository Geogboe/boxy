# K3s/OKD secret-backend reference

Use a writable PVC for the Boxy `file` backend and restrict the mount to the
Boxy service account. The backend file is mutable: admission stores rotated
resource credentials and allocation deletes consumed values, so a read-only
Kubernetes Secret volume is not the runtime backend.

The manifest is intentionally a reference fragment rather than a complete
cluster deployment. Supply the Boxy image, Service, TLS ingress, NetworkPolicy,
and storage class required by your cluster. Seed bootstrap input through an
authenticated Boxy command or admin API, then keep the source Kubernetes
Secret out of the runtime backend mount.
