package meshnet

import (
	"fmt"
	"net"
	"strconv"
)

// ListenPortFromEndpoint extracts the UDP port a WireGuard interface must
// bind to advertise itself at endpoint ("host:port", the same value returned
// as MeshPeerer's endpoint and configured as a provider's mesh_endpoint).
//
// Creating an interface with listen port 0 (letting the OS choose an
// ephemeral port) while advertising a fixed configured endpoint means a
// remote peer's handshake dials a port nothing is actually listening on --
// neither side can ever complete a handshake. The interface must bind the
// exact port it advertises.
func ListenPortFromEndpoint(endpoint string) (int, error) {
	_, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return 0, fmt.Errorf("parse mesh endpoint %q: %w", endpoint, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("parse mesh endpoint %q port: %w", endpoint, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("mesh endpoint %q port %d out of range", endpoint, port)
	}
	return port, nil
}
