package prolink

import (
	"fmt"
	"io"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"
)

// captureListener implements io.Reader, providing the ability to capture
// status packets using pcap, instead of binding to the interface itself.
//
// This allows the software to run along side other programs that listen for
// status packets (such as rekordbox).
type captureListener struct {
	source *gopacket.PacketSource
}

// Read implements the io.Reader interface. This method will read a status
// packet directly off the interface using packet capturing.
func (cl *captureListener) Read(p []byte) (int, error) {
	for packet := range cl.source.Packets() {
		appLayer := packet.ApplicationLayer()
		if appLayer == nil {
			continue
		}

		data := appLayer.Payload()
		copy(p, data)

		return len(data), nil
	}

	return 0, nil
}

// newCaptureListener attempts to create a captureListener. In the case where
// we cannot sniff the network interface this may fail due to privileges.
func newCaptureListener(iface *net.Interface, addr *net.UDPAddr) (*captureListener, error) {
	handle, err := pcap.OpenLive(iface.Name, 1600, false, pcap.BlockForever)
	if err != nil {
		return nil, err
	}

	// Compile a BPF filter to listen for UDP packets on the given port
	handle.SetBPFFilter(fmt.Sprintf("udp port %d", addr.Port))

	// We're listening for incoming packets only
	handle.SetDirection(pcap.DirectionIn)

	src := gopacket.NewPacketSource(handle, handle.LinkType())

	listener := captureListener{
		source: src,
	}

	return &listener, nil
}

// openListener attempts to open a listener. If we're unable to listen
// promiscuously on the interface (allowing other software to also listen for
// status packets) it will fall back to directly binding to the interface.
func openListener(iface *net.Interface, addr *net.UDPAddr) (io.Reader, error) {
	captureListener, err := newCaptureListener(iface, addr)
	if err == nil {
		return captureListener, nil
	}

	listenerConn, err := net.ListenUDP("udp", listenerAddr)
	if err == nil {
		return listenerConn, nil
	}

	return nil, fmt.Errorf("Cannot capture or bind to interface to listen")
}
