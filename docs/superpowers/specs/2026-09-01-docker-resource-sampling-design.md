# Docker host resource sampling (#283)

The Docker driver implements the optional `providersdk.AvailabilityReporter`
capability by calling Docker Engine `Info`. It reports `MemTotal` converted to
MiB through the existing agent heartbeat and Agents UI capacity sample.

Docker exposes total host memory here, not a reliable free-memory signal. The
sample is therefore a capacity estimate for operator visibility only. Boxy
must not reject allocations based on it; allocation admission continues to
use the provider's normal create result until Docker supplies a trustworthy
free-memory value.

The driver seam includes only the `Info` method needed by this capability, and
tests inject a mock Docker client so sampling remains deterministic without a
running Engine.
