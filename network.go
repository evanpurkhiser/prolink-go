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

// playerIDrange is the normal set of player IDs that may exist on one prolink
// network.
var prolinkIDRange = []DeviceID{0x01, 0x02, 0x03, 0x04}

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
		Name:    string(bytes.TrimRight(packet[0x0C:0x0C+20], "\x00")),
		ID:      DeviceID(packet[0x24]),
		Type:    DeviceType(packet[0x34]),
		MacAddr: net.HardwareAddr(packet[0x26 : 0x26+6]),
		IP:      net.IP(packet[0x2C : 0x2C+4]),
	}

	dev.LastActive = time.Now()

	return dev, nil
}

// getMatchingInterface determines the interface that routes the given address
// by comparing the masked addresses.
func getMatchingInterface(ip net.IP) (*net.Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, possibleIface := range ifaces {
		addrs, err := possibleIface.Addrs()
		if err != nil {
			return nil, err
		}

		for _, addr := range addrs {
			ifaceIP, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			if ifaceIP.IP.Mask(ifaceIP.Mask).Equal(ip.Mask(ifaceIP.Mask)) {
				return &possibleIface, nil
			}
		}
	}

	return nil, fmt.Errorf("Failed to find matching interface for %s", ip)
}

// getBroadcastAddress determines the broadcast address to use for
// communicating with the device.
func getBroadcastAddress(dev *Device) *net.UDPAddr {
	mask := dev.IP.DefaultMask()
	bcastIPAddr := make(net.IP, net.IPv4len)

	for i, b := range dev.IP.To4() {
		bcastIPAddr[i] = b | ^mask[i]
	}

	broadcastAddr := net.UDPAddr{
		IP:   bcastIPAddr,
		Port: announceAddr.Port,
	}

	return &broadcastAddr
}

// newVirtualCDJDevice constructs a Device that can be bound to the network
// interface provided.
func newVirtualCDJDevice(iface *net.Interface, id DeviceID) (*Device, error) {
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
		Name:    VirtualCDJName,
		ID:      id,
		Type:    DeviceTypeCDJ,
		MacAddr: iface.HardwareAddr,
		IP:      *ipAddress,
	}

	return virtualCDJ, nil
}

// cdjAnnouncer manages announcing a CDJ device on the network. This is usually
// used to announce a "virtual CDJ" which allows the prolink library to recieve
// more details from real CDJs on the network.
type cdjAnnouncer struct {
	cancel  chan bool
	running bool
}

// start creates a goroutine that will continually announce a virtual CDJ
// device on the host network.
func (a *cdjAnnouncer) activate(vCDJ *Device, announceConn *net.UDPConn) {
	if a.running == true {
		return
	}

	broadcastAddrs := getBroadcastAddress(vCDJ)
	announcePacket := getAnnouncePacket(vCDJ)
	announceTicker := time.NewTicker(keepAliveInterval)

	go func() {
		for {
			select {
			case <-a.cancel:
				return
			case <-announceTicker.C:
				announceConn.WriteToUDP(announcePacket, broadcastAddrs)
			}
		}
	}()

	a.running = true
}

// stop stops the running announcer
func (a *cdjAnnouncer) deactivate() {
	if a.running == true {
		a.cancel <- true
	}
}

func newCDJAnnouncer() *cdjAnnouncer {
	return &cdjAnnouncer{
		cancel: make(chan bool),
	}
}

// Network is the priamry API to the PRO DJ LINK network.
type Network struct {
	announceConn *net.UDPConn
	listenerConn *net.UDPConn

	announcer  *cdjAnnouncer
	cdjMonitor *CDJStatusMonitor
	devManager *DeviceManager
	remoteDB   *RemoteDB

	// TargetInterface specifies what network interface to broadcast announce
	// packets for the virtual CDJ on.
	//
	// This field should not be reconfigured, use SetInterface instead to
	// ensure the announce is correctly restarted on the new interface.
	TargetInterface *net.Interface

	// VirtualCDJID specifies the CDJ Device ID (Player ID) that should be used
	// when announcing the device.
	//
	// This field should not be reconfigured, use SetVirtualCDJID instead to
	// ensure the announce is correctly restarted on the new interface.
	VirtualCDJID DeviceID
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

// SetVirtualCDJID configures the CDJ ID (Player ID) that the prolink library
// should use to identify itself on the network. To correctly access metadata
// on the network this *must* be in the range from 1-4, and should *not* be a
// player ID that is already in use by a CDJ, otherwise the CDJ simply will not
// respond. This is a known issue [1]
//
// [1]: https://github.com/EvanPurkhiser/prolink-go/issues/6
func (n *Network) SetVirtualCDJID(id DeviceID) error {
	n.VirtualCDJID = id
	n.remoteDB.setRequestingDeviceID(id)

	return n.reloadAnnouncer()
}

// SetInterface configures what network interface should be used when
// announcing the Virtual CDJ.
func (n *Network) SetInterface(iface *net.Interface) error {
	n.TargetInterface = iface

	return n.reloadAnnouncer()
}

func (n *Network) reloadAnnouncer() error {
	if n.TargetInterface == nil || n.VirtualCDJID == 0x0 {
		return nil
	}

	vCDJ, err := newVirtualCDJDevice(n.TargetInterface, n.VirtualCDJID)
	if err != nil {
		return fmt.Errorf("Failed to construct virtual CDJ: %s", err)
	}

	n.announcer.deactivate()
	n.announcer.activate(vCDJ, n.announceConn)

	return nil
}

// openUDPConnection connects to the minimum required UDP sockets needed to
// communicate with the Prolink network.
func (n *Network) openUDPConnections() error {
	listenerConn, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		return fmt.Errorf("Failed to open listener conection: %s", err)
	}

	n.listenerConn = listenerConn

	announceConn, err := net.ListenUDP("udp", announceAddr)
	if err != nil {
		return fmt.Errorf("Cannot open UDP announce connection: %s", err)
	}

	n.announceConn = announceConn

	return nil
}

// activeNetwork keeps a reference to the currently connected network.
var activeNetwork *Network

// Connect connects to the Pioneer PRO DJ LINK network, returning the singleton
// Network object to interact with the connection.
//
// Note that after connecting you must configure the virtual CDJ ID and network
// interface to announce the virtual CDJ on before all functionality of the
// prolink network will be available, specifically:
//
// - CDJs will not broadcast detailed payer information until they recieve the
//   announce packet and recognize the libraries virtual CDJ as being on the
//   network.
//
// - Any remote DB devices will not respond to metadata queries.
//
// Both values may be autodetected or manually configured.
func Connect() (*Network, error) {
	if activeNetwork != nil {
		return activeNetwork, nil
	}

	n := &Network{
		announcer:  newCDJAnnouncer(),
		remoteDB:   newRemoteDB(),
		devManager: newDeviceManager(),
		cdjMonitor: newCDJStatusMonitor(),
	}

	activeNetwork = n

	n.openUDPConnections()

	// We can start the device manager and CDJ monitor immediately as neither
	// of these have any type of reconfiguration options other than then
	// network connection. (unlike the remote DB and announcer)
	n.devManager.activate(n.announceConn)
	n.cdjMonitor.activate(n.listenerConn)
	n.remoteDB.activate(n.devManager)

	return n, nil
}
