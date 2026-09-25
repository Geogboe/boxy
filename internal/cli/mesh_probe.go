package cli

import (
	"log/slog"

	"github.com/Geogboe/boxy/pkg/meshnet"
)

// meshnetProbe is meshnet.Probe behind a package-level var, following the
// same injectable-factory pattern as svcmgrNewManager and isElevatedFn, so
// tests can exercise probeMeshCapability's enabled/disabled/failure branches
// without needing a real CAP_NET_ADMIN/wintun.dll environment.
var meshnetProbe = meshnet.Probe

// probeMeshCapability reports whether this process can create a WireGuard
// device, for a caller about to build an AgentInfo (embedded or remote) --
// see agentsdk.AgentInfo.MeshCapable's doc comment for the full contract.
//
// enabled is the operator's own explicit opt-in (ServerSpec.MeshOverlayEnabled
// for the embedded agent, --enable-mesh-overlay for a remote one). When
// false, the probe is not run at all and this returns false unconditionally
// -- on Windows, creating the first WireGuard device is what installs the
// Wintun kernel driver, so probing unconditionally would install it on
// every agent whether or not the operator ever intends to use cross-host
// mesh (see docs/adr/0022's 2026-09-25 changelog entry).
//
// A probe failure is logged, not returned as an error: the operator asked
// for mesh, the environment isn't ready for it, and the daemon should keep
// serving everything else exactly the way an unset MeshCapable already
// degrades (single-host isolation still works, only cross-host mesh setup
// is skipped) rather than blocking startup on it.
func probeMeshCapability(enabled bool) bool {
	if !enabled {
		return false
	}
	if err := meshnetProbe(); err != nil {
		slog.Default().Warn("mesh overlay enabled but this host cannot create a WireGuard device; cross-host mesh peering will be skipped",
			"error", err, "operation", "mesh_capability_probe")
		return false
	}
	return true
}
