//go:build !linux

package meshnet

// bringLinkUp is a no-op on non-Linux platforms. Windows' Wintun adapter
// (the tun.Device wireguard-go uses there) is reported to come up as part
// of its own creation, unlike a Linux TUN device -- but that is unverified
// from this development host, which cannot run live Windows networking
// tests (see AGENTS.md). Revisit with real evidence before adding
// Windows-specific link-state code here.
func bringLinkUp(_ string) error {
	return nil
}
