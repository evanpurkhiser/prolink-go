package prolink

import (
	"bytes"
	"fmt"
	"net"
	"time"
)

// We wait a second and a half to send keep alive packets for the virtual CDJ
// we create on the PRO DJ LINK network.
const keepAliveInterval = 1500 * time.Millisecond

// How long to wait after before considering a device off the network.
const deviceTimeout = 10 * time.Second

// Length of device announce packets
const announcePacketLen = 54

// The UDP broadcast address on which device annoucments should be made.
var broadcastAddr = &net.UDPAddr{
	IP:   net.IPv4bcast,
	Port: 50000,
}

// The UDP address on which device announcements are recieved.
var announceAddr = &net.UDPAddr{
	IP:   net.IPv4zero,
	Port: 50000,
}

// The UDP address on which device information is received.
var listenerAddr = &net.UDPAddr{
	IP:   net.IPv4zero,
	Port: 50002,
}

// All UDP packets on the PRO DJ LINK network start with this header.
var prolinkHeader = []byte{
	0x51, 0x73, 0x70, 0x74, 0x31,
	0x57, 0x6d, 0x4a, 0x4f, 0x4c,
}

// getAnnouncePacket constructs the announce packet that is sent on the PRO DJ
// LINK network to announce a devices existence.
func getAnnouncePacket(dev *Device) []byte {
	// The name is a 20 byte string
	name := make([]byte, 20)
	copy(name[:], []byte(dev.Name))

	// unknown padding bytes
	unknown1 := []byte{0x01, 0x02, 0x00, 0x36}
	unknown2 := []byte{0x01, 0x00, 0x00, 0x00}

	parts := [][]byte{
		prolinkHeader,          // 0x00: 10 byte header
		[]byte{0x06, 0x00},     // 0x0A: 02 byte announce packet type
		name,                   // 0x0c: 20 byte device name
		unknown1,               // 0x20: 04 byte unknown
		[]byte{byte(dev.ID)},   // 0x24: 01 byte for the player ID
		[]byte{0x00},           // 0x25: 01 byte unknown
		dev.MacAddr[:6],        // 0x26: 06 byte mac address
		dev.IP.To4(),           // 0x2C: 04 byte IP address
		unknown2,               // 0x30: 04 byte unknown
		[]byte{byte(dev.Type)}, // 0x34: 01 byte for the player type
		[]byte{0x00},           // 0x35: 01 byte final padding

	}

	return bytes.Join(parts, nil)
}

// deviceFromAnnouncePacket constructs a device object given a device
// announcement packet.
func deviceFromAnnouncePacket(packet []byte) (*Device, error) {
	if !bytes.HasPrefix(packet, prolinkHeader) {
		return nil, fmt.Errorf("Announce packet does not start with expected header")
	}

	if packet[0x0A] != 0x06 {
		return nil, fmt.Errorf("Packet is not an announce packet")
	}

	dev := &Device{
		Name:    string(packet[0x0C : 0x0c+20]),
		ID:      DeviceID(packet[0x24]),
		Type:    DeviceType(packet[0x34]),
		MacAddr: net.HardwareAddr(packet[0x26 : 0x26+6]),
		IP:      net.IP(packet[0x2C : 0x2C+4]),
	}

	dev.LastActive = time.Now()

	return dev, nil
}

// getBroadcastInterface returns the network interface that may be used to
// broadcast UDP packets.
func getBroadcastInterface() (*net.Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var iface *net.Interface

	// Find the interface that supports network broadcast
	for _, possibleIface := range ifaces {
		if possibleIface.Flags&net.FlagBroadcast != 0 {
			iface = &possibleIface
			break
		}
	}

	if iface == nil {
		return nil, fmt.Errorf("No network interface available to broadcast over")
	}

	return iface, nil
}

// newVirtualCDJDevice constructs a Device that can be bound to the network
// interface provided.
func newVirtualCDJDevice(iface *net.Interface) (*Device, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	var ipAddress *net.IP
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
			ipAddress = &ipNet.IP
			break
		}
	}
	if ipAddress == nil {
		return nil, fmt.Errorf("No IPv4 broadcast interface available")
	}

	virtualCDJ := &Device{
		Name:    "Virtual CDJ",
		ID:      DeviceID(0x04),
		Type:    DeviceTypeVCDJ,
		MacAddr: iface.HardwareAddr,
		IP:      *ipAddress,
	}

	return virtualCDJ, nil
}

// startVCDJAnnouncer creates a goroutine that will continually announce a
// virtual CDJ device on the host network. Returns the Virtual CDJ being
// announced.
func startVCDJAnnouncer(announceConn *net.UDPConn) (*Device, error) {
	bcastIface, err := getBroadcastInterface()
	if err != nil {
		return nil, err
	}

	virtualCDJ, err := newVirtualCDJDevice(bcastIface)
	if err != nil {
		return nil, err
	}

	announcePacket := getAnnouncePacket(virtualCDJ)
	announceTicker := time.NewTicker(keepAliveInterval)

	go func() {
		for range announceTicker.C {
			announceConn.WriteToUDP(announcePacket, broadcastAddr)
		}
	}()

	return virtualCDJ, nil
}

// Network is the priamry API to the PRO DJ LINK network.
type Network struct {
	cdjMonitor *CDJStatusMonitor
	devManager *DeviceManager
	remoteDB   *RemoteDB
}

// CDJStatusMonitor obtains the CDJStatusMonitor for the network.
func (n *Network) CDJStatusMonitor() *CDJStatusMonitor {
	return n.cdjMonitor
}

// DeviceManager returns the DeviceManager for the network.
func (n *Network) DeviceManager() *DeviceManager {
	return n.devManager
}

// RemoteDB returns the remote database client for the network.
func (n *Network) RemoteDB() *RemoteDB {
	return n.remoteDB
}

// activeNetwork keeps
var activeNetwork *Network

// Connect connects to the Pioneer PRO DJ LINK network, returning a Network
// object to interact with the connection.
func Connect() (*Network, error) {
	if activeNetwork != nil {
		return activeNetwork, nil
	}

	announceConn, err := net.ListenUDP("udp", announceAddr)
	if err != nil {
		return nil, fmt.Errorf("Cannot open UDP announce connection: %s", err)
	}

	listenerConn, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		return nil, fmt.Errorf("Cannot open UDP listening connection: %s", err)
	}

	vcdj, err := startVCDJAnnouncer(announceConn)
	if err != nil {
		return nil, fmt.Errorf("Failed to start Virtual CDJ announcer: %s", err)
	}

	network := &Network{
		remoteDB:   newRemoteDB(),
		cdjMonitor: newCDJStatusMonitor(),
		devManager: newDeviceManager(),
	}

	network.remoteDB.activate(network.devManager, vcdj.ID)
	network.cdjMonitor.activate(listenerConn)
	network.devManager.activate(announceConn)

	activeNetwork = network

	return network, nil
}
