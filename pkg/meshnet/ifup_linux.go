//go:build linux

package meshnet

import "github.com/vishvananda/netlink"

// bringLinkUp sets a TUN device's kernel link state to up. wireguard-go's
// device.Device.Up only starts the WireGuard packet-processing goroutines --
// it never touches the interface's own administrative state, the same way
// wg-quick's own setup script runs a separate "ip link set up dev ..." step
// after configuring the device. Without this, the interface is created but
// stays down (confirmed running this end to end on a real Linux host: every
// route through it failed with "RTNETLINK answers: Network is down").
func bringLinkUp(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return netlink.LinkSetUp(link)
}
