package prolink

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/inconshreveable/log15"
)

var be = binary.BigEndian

// Logger specifies the logger that should be used for capturing information.
// May be disabled by replacing with the logrus.test.NullLogger.
var Log = log15.New("module", "prolink")

func init() {
	Log.SetHandler(log15.LvlFilterHandler(
		log15.LvlInfo,
		log15.StdoutHandler,
	))
}

// We wait a second and a half to send keep alive packets for the virtual CDJ
// we create on the PRO DJ LINK network.
const keepAliveInterval = 1500 * time.Millisecond

// How long to wait after before considering a device off the network.
const deviceTimeout = 10 * time.Second

// Length of device announce packets
const announcePacketLen = 54

const announceDeadline = 5 * time.Second

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

func makeNameBytes(dev *Device) []byte {
	// The name is a 20 byte string
	name := make([]byte, 20)
	copy(name[:], []byte(dev.Name))

	return name
}

// getAnnouncePacket constructs the announce packet that is sent on the PRO DJ
// LINK network to announce a devices existence.
func getAnnouncePacket(dev *Device) []byte {
	// unknown padding bytes
	unknown1 := []byte{0x01, 0x02, 0x00, 0x36}
	unknown2 := []byte{0x01, 0x00, 0x00, 0x00}

	parts := [][]byte{
		prolinkHeader,          // 0x00: 10 byte header
		[]byte{0x06, 0x00},     // 0x0A: 02 byte announce packet type
		makeNameBytes(dev),     // 0x0c: 20 byte device name
		unknown1,               // 0x20: 04 byte unknown
		[]byte{byte(dev.ID)},   // 0x24: 01 byte for the player ID
		[]byte{byte(dev.Type)}, // 0x25: 01 byte for the player type
		dev.MacAddr[:6],        // 0x26: 06 byte mac address
		dev.IP.To4(),           // 0x2C: 04 byte IP address
		unknown2,               // 0x30: 04 byte unknown
		[]byte{byte(dev.Type)}, // 0x34: 01 byte for the player type
		[]byte{0x00},           // 0x35: 01 byte final padding
	}

	return bytes.Join(parts, nil)
}

// getStatusPacket returns a mostly empty-state status packet. This is
// currently used to report the virtual CDJs status, which *seems* to be
// required for the CDJ to send metadata about some unanalyzed mp3 files.
func getStatusPacket(dev *Device) []byte {
	// NOTE: It seems that byte 0x68 and 0x75 MUST be 1 in order for the CDJ to
	//       correctly report mp3 metadata (again, only for some files).
	//       See https://github.com/brunchboy/dysentery/issues/15
	b := []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0a, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x03, 0x00, 0x00, 0xf8, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x04, 0x04, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x9c, 0xff, 0xfe, 0x00, 0x10, 0x00, 0x00,
		0x7f, 0xff, 0xff, 0xff, 0x7f, 0xff, 0xff, 0xff, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff,
		0xff, 0xff, 0xff, 0xff, 0x01, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x10, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}

	// Replace key values into template packet
	copy(b, prolinkHeader)             // 0x00: 10 byte header
	copy(b[0x0B:], makeNameBytes(dev)) // 0x0B: 20 byte device name
	copy(b[0x21:], string(dev.ID))     // 0x21: 01 byte device ID
	copy(b[0x24:], string(dev.ID))     // 0x24: 01 byte device ID
	copy(b[0x7C:], VirtualCDJFirmware) // 0x7C: 04 byte firmware string

	return b
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
// by comparing the masked addresses. This type of information is generally
// determined through the kernels routing table, but for sake of cross-platform
// compatibility, we do some rudimentary lookup.
func getMatchingInterface(ip net.IP) (*net.Interface, error) {
	Log.Debug("Matching IP route interface", "ip", ip)

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, possibleIface := range ifaces {
		if possibleIface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := possibleIface.Addrs()
		if err != nil {
			return nil, err
		}

		var matchedIface *net.Interface
		matchedSubnetLen := 0x00

		for _, addr := range addrs {
			ifaceIP, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			subnetLen, _ := ifaceIP.Mask.Size()

			if ifaceIP.Contains(ip) && subnetLen > matchedSubnetLen {
				matchedIface = &possibleIface
				matchedSubnetLen = subnetLen
			}

			Log.Debug("Checking iface addr", "iface", possibleIface.Name, "addr", ifaceIP)
		}

		if matchedIface != nil {
			return matchedIface, nil
		}
	}

	return nil, fmt.Errorf("Failed to find matching interface for %s", ip)
}

// getV4IPNetOfInterface finds the first Ipv4 address on an interface that is
// not a loopback address.
func getV4IPNetOfInterface(iface *net.Interface) (*net.IPNet, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
			return ipNet, nil
		}
	}

	return nil, nil
}

// getBroadcastAddress determines the broadcast address to use for
// communicating with the device.
func getBroadcastAddress(dev *Device) *net.UDPAddr {
	iface, _ := getMatchingInterface(dev.IP)
	ipNet, _ := getV4IPNetOfInterface(iface)

	mask := ipNet.Mask
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
	ipNet, err := getV4IPNetOfInterface(iface)
	if err != nil {
		return nil, err
	}

	if ipNet == nil {
		return nil, fmt.Errorf("No IPv4 broadcast interface available")
	}

	virtualCDJ := &Device{
		Name:    VirtualCDJName,
		ID:      id,
		Type:    DeviceTypeCDJ,
		MacAddr: iface.HardwareAddr,
		IP:      ipNet.IP,
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
func (a *cdjAnnouncer) activate(vCDJ *Device, dm *DeviceManager, announceConn *net.UDPConn) {
	if a.running == true {
		return
	}

	Log.Info("Announcing vCDJ on network", "vCDJ", vCDJ)

	announceTicker := time.NewTicker(keepAliveInterval)
	broadcastAddrs := getBroadcastAddress(vCDJ)
	announcePacket := getAnnouncePacket(vCDJ)
	statusPacket := getStatusPacket(vCDJ)

	Log.Info("Broadcast address detected", "addr", broadcastAddrs)

	announceStatus := func() {
		for _, device := range dm.ActiveDevices() {
			addr := &net.UDPAddr{
				IP:   device.IP,
				Port: listenerAddr.Port,
			}

			announceConn.SetWriteDeadline(time.Now().Add(announceDeadline))
			announceConn.WriteToUDP(statusPacket, addr)
		}
	}

	go func() {
		for {
			select {
			case <-a.cancel:
				a.running = false
				return
			case <-announceTicker.C:
				announceConn.SetWriteDeadline(time.Now().Add(announceDeadline))
				announceConn.WriteToUDP(announcePacket, broadcastAddrs)
				announceStatus()
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

	Log.Info("Announcer stopped")
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
	Log.Info("VirtualCDJ ID updated", "ID", id)

	return n.reloadAnnouncer()
}

// SetInterface configures what network interface should be used when
// announcing the Virtual CDJ.
func (n *Network) SetInterface(iface *net.Interface) error {
	lastInterface := n.TargetInterface

	n.TargetInterface = iface
	Log.Info("PROLINK interface updated", "iface", iface.Name)

	err := n.reloadAnnouncer()
	if err != nil {
		Log.Warn("Bad interface, restoring previous interface", "err", err)
		n.TargetInterface = lastInterface
		n.reloadAnnouncer()
	}

	return err
}

// AutoConfigure attempts to configure the two confgiuration parameters of the
// network.
//
// - Determine which interface to announce the Virtual CDJ over by finding
//   the interface which has a matching net mask to the first CDJ detected on the
//   network.
//
// - Determine the Virtual CDJ ID to assume by looking for the first unused CDJ
//   ID on the network.
//
// wait specifies how long to wait before checking what devices have appeared
// on the network to determine auto configuration values from.
func (n *Network) AutoConfigure(wait time.Duration) error {
	Log.Debug("Waiting to autoconfigure", "wait", wait)
	time.Sleep(wait)

	Log.Debug("Starting autoconfigure")

	playerIDs := []DeviceID{}
	var CDJAddr net.IP

	for _, device := range n.devManager.ActiveDevices() {
		if device.Type != DeviceTypeCDJ {
			continue
		}

		playerIDs = append(playerIDs, device.ID)
		CDJAddr = device.IP
	}

	if len(playerIDs) == 0 {
		return fmt.Errorf("Could not autoconfigure network: no CDJs on network")
	}

	var unusedDeviceID DeviceID

	// Choose an unused ID from the 4 available CDJ slots
	for _, id := range prolinkIDRange {
		isUnused := true

		for _, usedID := range playerIDs {
			if id == usedID {
				isUnused = false
			}
		}

		if isUnused {
			unusedDeviceID = id
			break
		}
	}

	if unusedDeviceID == 0x0 {
		return fmt.Errorf("Could not autoconfigure network: No available Virtual CDJ slots")
	}

	n.SetVirtualCDJID(unusedDeviceID)

	// Determine the matching interface for the CDJ
	iface, err := getMatchingInterface(CDJAddr)
	if err != nil {
		return fmt.Errorf("Could not autoconfigure network: %s", err)
	}

	n.SetInterface(iface)

	return nil
}

func (n *Network) reloadAnnouncer() error {
	if n.TargetInterface == nil || n.VirtualCDJID == 0x0 {
		return nil
	}

	vCDJ, err := newVirtualCDJDevice(n.TargetInterface, n.VirtualCDJID)
	if err != nil {
		return fmt.Errorf("Failed to construct virtual CDJ: %s", err)
	}

	Log.Info("Reloading announcer")

	n.announcer.deactivate()
	n.announcer.activate(vCDJ, n.devManager, n.announceConn)

	// Reload the remote remote DB service since we may now be announcing as a
	// different device, we need to re-associate ourselves with the devices
	// serving the remote database.
	n.remoteDB.deactivate(n.devManager)
	n.remoteDB.activate(n.devManager)

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
	Log.Debug("UDP socket open -> packet listening", "addr", listenerAddr)

	announceConn, err := net.ListenUDP("udp", announceAddr)
	if err != nil {
		return fmt.Errorf("Cannot open UDP announce connection: %s", err)
	}

	n.announceConn = announceConn
	Log.Debug("UDP socket open -> announce listening", "addr", announceAddr)

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

	Log.Info("Connecting to Prolink network")

	n := &Network{
		announcer:  newCDJAnnouncer(),
		remoteDB:   newRemoteDB(),
		devManager: newDeviceManager(),
		cdjMonitor: newCDJStatusMonitor(),
	}

	activeNetwork = n

	err := n.openUDPConnections()
	if err != nil {
		return nil, err
	}

	// We can start the device manager and CDJ monitor immediately as neither
	// of these have any type of reconfiguration options other than then
	// network connection.
	n.devManager.activate(n.announceConn)
	n.cdjMonitor.activate(n.listenerConn)

	// NOTE: We cannot start the remoteDB service until the Virtual CDJ has
	// been announced on the network.

	return n, nil
}
