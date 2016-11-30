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
var header = []byte{
	0x51, 0x73, 0x70, 0x74, 0x31,
	0x57, 0x6d, 0x4a, 0x4f, 0x4c,
}

// Defined device types.
const (
	DeviceTypeRB    DeviceType = 0x01
	DeviceTypeCDJ   DeviceType = 0x02
	DeviceTypeMixer DeviceType = 0x03

	// DeviceTypeVCDJ is a custom device type that we will use to identify our
	// Virtual CDJ.
	DeviceTypeVCDJ DeviceType = 0xff
)

// DeviceType represents the types of devices on the network.
type DeviceType byte

// DeviceID represents the ID of the device. For CDJs this is the number
// displayed on screen.
type DeviceID byte

// Device represents a device on the network.
type Device struct {
	Name       string
	ID         DeviceID
	Type       DeviceType
	MacAddr    net.HardwareAddr
	IP         net.IP
	LastActive time.Time
}

// String returns a string representation of a device.
func (d *Device) String() string {
	return fmt.Sprintf("%s %02d @ %s [%s]", d.Name, d.ID, d.IP, d.MacAddr)
}

// getAnnouncePacket constructs the announce packet that is sent on the PRO DJ
// LINK network to announce a devices existence.
func getAnnouncePacket(dev *Device) []byte {
	// The name is a 20 byte string
	name := make([]byte, 20)
	copy(name[:], []byte(dev.Name))

	// unknown padding bytes
	unknown1 := []byte{0x01, 0x02, 0x00, 0x36}
	unknown2 := []byte{0x01, 0x00, 0x00, 0x00, 0x01, 0x00}

	parts := [][]byte{
		header,                 // 0x00: 10 byte header
		[]byte{0x06, 0x00},     // 0x0A: 02 byte announce packet type
		name,                   // 0x0c: 20 byte device name
		unknown1,               // 0x20: 04 byte unknown
		[]byte{byte(dev.ID)},   // 0x24: 01 byte for the player ID
		[]byte{byte(dev.Type)}, // 0x25: 01 byte for device type
		dev.MacAddr[:6],        // 0x26: 06 byte mac address
		dev.IP.To4(),           // 0x2C: 04 byte IP address
		unknown2,               // 0x30: 06 byte unknown
	}

	return bytes.Join(parts, nil)
}

// deviceFromAnnouncePacket constructs a device object given a device
// announcement packet.
func deviceFromAnnouncePacket(packet []byte) (*Device, error) {
	if !bytes.HasPrefix(packet, header) {
		return nil, fmt.Errorf("Announce packet does not start with expected header")
	}

	if packet[0x0A] != 0x06 {
		return nil, fmt.Errorf("Packet is not an announce packet")
	}

	dev := &Device{
		Name:    string(packet[0x0C : 0x0c+20]),
		ID:      DeviceID(packet[0x24]),
		Type:    DeviceType(packet[0x25]),
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

	virtualCDJ := &Device{
		Name:    "Virtual CDJ",
		ID:      DeviceID(0x05),
		Type:    DeviceTypeVCDJ,
		MacAddr: iface.HardwareAddr,
		IP:      addrs[0].(*net.IPNet).IP,
	}

	return virtualCDJ, nil
}

// startVCDJAnnouncer creates a goroutine that will continually announce a
// virtual CDJ device on the host network.
func startVCDJAnnouncer(announceConn *net.UDPConn) error {
	bcastIface, err := getBroadcastInterface()
	if err != nil {
		return err
	}

	virtualCDJ, err := newVirtualCDJDevice(bcastIface)
	if err != nil {
		return err
	}

	announcePacket := getAnnouncePacket(virtualCDJ)
	announceTicker := time.NewTicker(keepAliveInterval)

	go func() {
		for range announceTicker.C {
			announceConn.WriteToUDP(announcePacket, broadcastAddr)
		}
	}()

	return nil
}

// DeviceListener is a function that will be called when a change is made to a
// device. Currently this includes the device being added or removed.
type DeviceListener func(*Device)

// DeviceManager provides functionality for watching the connection status of
// PRO DJ LINK devices on the network.
type DeviceManager struct {
	delHandlers []DeviceListener
	addHandlers []DeviceListener
	devices     map[DeviceID]*Device
}

// OnDeviceAdded registers a listener that will be called when any PRO DJ LINK
// devices are added to the network.
func (m *DeviceManager) OnDeviceAdded(fn DeviceListener) {
	m.addHandlers = append(m.addHandlers, fn)
}

// OnDeviceRemoved registers a listener that will be called when any PRO DJ
// LINK devices are removed from the network.
func (m *DeviceManager) OnDeviceRemoved(fn DeviceListener) {
	m.delHandlers = append(m.delHandlers, fn)
}

// ActiveDeviceMap returns a mapping of device IDs to their associated devices.
func (m *DeviceManager) ActiveDeviceMap() map[DeviceID]*Device {
	return m.devices
}

// ActiveDevices returns a list of active devices on the PRO DJ LINK network.
func (m *DeviceManager) ActiveDevices() []*Device {
	devices := make([]*Device, 0, len(m.devices))

	for _, dev := range m.devices {
		devices = append(devices, dev)
	}

	return devices
}

// activate triggers the DeviceManager to begin watching for device changes on
// the PRO DJ LINK network.
func (m *DeviceManager) activate(announceConn *net.UDPConn) {
	packet := make([]byte, announcePacketLen)

	timeouts := map[DeviceID]*time.Timer{}

	timeoutTimer := func(dev *Device) {
		timeouts[dev.ID] = time.NewTimer(deviceTimeout)
		<-timeouts[dev.ID].C

		// Device timeout expired. No longer active
		delete(timeouts, dev.ID)
		delete(m.devices, dev.ID)

		for _, fn := range m.delHandlers {
			go fn(dev)
		}
	}

	announceHandler := func() {
		announceConn.Read(packet)
		dev, err := deviceFromAnnouncePacket(packet)
		if err != nil {
			return
		}

		if dev.Type == DeviceTypeVCDJ {
			return
		}

		// Update device keepalive
		if dev, ok := m.devices[dev.ID]; ok {
			timeouts[dev.ID].Stop()
			timeouts[dev.ID].Reset(deviceTimeout)
			dev.LastActive = time.Now()
			return
		}

		// New device
		m.devices[dev.ID] = dev

		for _, fn := range m.addHandlers {
			go fn(dev)
		}

		go timeoutTimer(dev)
	}

	// Begin listening for announce packets
	go func() {
		for {
			announceHandler()
		}
	}()
}

func newDeviceManager() *DeviceManager {
	return &DeviceManager{
		addHandlers: []DeviceListener{},
		delHandlers: []DeviceListener{},
		devices:     map[DeviceID]*Device{},
	}
}

// StatusHandler is a function that will be called when the status of a CDJ
// device has changed.
type StatusHandler func(status *CDJStatus)

// CDJStatusMonitor provides an interface for watching for status updates to
// CDJ devices on the PRO DJ LINK network.
type CDJStatusMonitor struct {
	handlers []StatusHandler
}

// OnStatusUpdate registers a StatusHandler to be called when any CDJ on the
// PRO DJ LINK network reports its status.
func (sm *CDJStatusMonitor) OnStatusUpdate(fn StatusHandler) {
	sm.handlers = append(sm.handlers, fn)
}

// activate triggers the CDJStatusMonitor to begin listening for status packets
// given a UDP connection to listen on.
func (sm *CDJStatusMonitor) activate(listenConn *net.UDPConn) {
	packet := make([]byte, 512)

	statusUpdateHandler := func() {
		n, _ := listenConn.Read(packet)
		status, err := packetToStatus(packet[:n])
		if err != nil {
			return
		}

		if status == nil {
			return
		}

		for _, fn := range sm.handlers {
			go fn(status)
		}
	}

	go func() {
		for {
			statusUpdateHandler()
		}
	}()
}

func newCDJStatusMonitor() *CDJStatusMonitor {
	return &CDJStatusMonitor{handlers: []StatusHandler{}}
}

// Network is the priamry API to the PRO DJ LINK network.
type Network struct {
	cdjMonitor *CDJStatusMonitor
	devManager *DeviceManager
}

// CDJStatusMonitor obtains the CDJStatusMonitor for the network.
func (n *Network) CDJStatusMonitor() *CDJStatusMonitor {
	return n.cdjMonitor
}

// DeviceManager returns the DeviceManager for the network.
func (n *Network) DeviceManager() *DeviceManager {
	return n.devManager
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

	err = startVCDJAnnouncer(announceConn)
	if err != nil {
		return nil, fmt.Errorf("Failed to start Virtual CDJ announcer: %s", err)
	}

	network := &Network{
		cdjMonitor: newCDJStatusMonitor(),
		devManager: newDeviceManager(),
	}

	network.cdjMonitor.activate(listenerConn)
	network.devManager.activate(announceConn)

	activeNetwork = network

	return network, nil
}
