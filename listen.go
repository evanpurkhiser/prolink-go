package prolink

import (
	"fmt"
	"io"
	"net"
)

// openListener crates a status listener connection (returned as an io.Reader).
// If sniff is enabled we will attempt to listen on the interface using packet
// capturing, otherwise, we will simply directly bind to the interface.
func openListener(iface *net.Interface, addr *net.UDPAddr, sniff bool) (io.Reader, error) {
	listenerConn, err := net.ListenUDP("udp", listenerAddr)
	if err == nil {
		return listenerConn, nil
	}

	return nil, fmt.Errorf("Cannot capture or bind to interface to listen")
}
