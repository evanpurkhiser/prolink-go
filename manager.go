package main

import (
	"bytes"
	"fmt"
	"net"
	"time"
)

// We wait a second and a half to send keep alive packets for the virtual CDJ
// we create on the PRO DJ LINK network.
const keepAliveInterval = 1500 * time.Millisecond

// Length of device announce packets
const announcePacketLen = 54

// The UDP broadcast address on which device annoucments should be made
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
	DeviceTypeCDJ   DeviceType = 0x01
	DeviceTypeMixer DeviceType = 0x02

	// Custom device type that we will use to identify our Virtual CDJ
	DeviceTypeVCDJ DeviceType = 0xff
)

// DeviceType represents the types of devices on the network.
type DeviceType byte

// PlayerID represents the ID of the player. For CDJs this is the number
// displayed on screen.
type PlayerID byte

// Device represents a device on the network.
type Device struct {
	Name    string
	ID      PlayerID
	Type    DeviceType
	MacAddr net.HardwareAddr
	IP      net.IP
}

// String returns a string representation of a device.
func (d *Device) String() string {
	return fmt.Sprintf("%s %02d @ %s [%s]", d.Name, d.ID, d.IP.String(), d.MacAddr.String())
}

// getAnnouncePacket constructs the announce packet that is sent on the PRO DJ
// LINK network to announce a devices existence.
func getAnnouncePacket(dev *Device) []byte {
	p := make([]byte, 0, announcePacketLen)

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

	for _, part := range parts {
		p = append(p, part...)
	}

	return p
}

// deviceFromAnnouncePacket constructs a device object given a device
// announcement packet.
func deviceFromAnnouncePacket(packet []byte) (*Device, error) {
	if !bytes.HasPrefix(packet, header) {
		return nil, fmt.Errorf("Announce packet does not start with expected header")
	}

	dev := &Device{
		Name:    string(packet[0x0C : 0x0c+20]),
		ID:      PlayerID(packet[0x24]),
		Type:    DeviceType(packet[0x25]),
		MacAddr: net.HardwareAddr(packet[0x26 : 0x26+6]),
		IP:      net.IP(packet[0x2C : 0x2C+4]),
	}

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
		ID:      PlayerID(0x05),
		Type:    DeviceTypeVCDJ,
		MacAddr: iface.HardwareAddr,
		IP:      addrs[0].(*net.IPNet).IP,
	}

	return virtualCDJ, nil
}

type NetworkManager struct {
	announceConn *net.UDPConn
	listenerConn *net.UDPConn
	knownDevices map[PlayerID]*Device
}

func (m *NetworkManager) announceVirtualCDJ() error {
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

	doAnnoucments := func() {
		for range announceTicker.C {
			m.announceConn.WriteToUDP(announcePacket, broadcastAddr)
		}
	}

	go doAnnoucments()

	return nil
}

func (m *NetworkManager) watchDevices() error {
	packet := make([]byte, announcePacketLen)

	if m.knownDevices == nil {
		m.knownDevices = map[PlayerID]*Device{}
	}

	announceListener := func() {
		for {
			m.announceConn.Read(packet)
			dev, _ := deviceFromAnnouncePacket(packet)

			// Ignore the virtual CDJ device
			if dev.Type == DeviceTypeVCDJ {
				continue
			}

			// Ignore already accounted for devices
			if _, ok := m.knownDevices[dev.ID]; ok {
				continue
			}

			m.knownDevices[dev.ID] = dev

			fmt.Printf("New Device: %s\n", dev)
		}
	}

	go announceListener()

	return nil
}

func (m *NetworkManager) listenForStatus() error {
	packet := make([]byte, 512)

	listener := func() {
		for {
			len, _ := m.listenerConn.Read(packet)
			data := packet[:len]

			// Do something with the packet
		}
	}

	go listener()

	return nil
}

func (m *NetworkManager) Activate() error {
	announceConn, err := net.ListenUDP("udp", announceAddr)
	if err != nil {
		return fmt.Errorf("Cannot open UDP connection to announce: %s", err)
	}

	listenerConn, err := net.ListenUDP("udp", listenerAddr)
	if err != nil {
		return fmt.Errorf("Cannot open UDP connection to listen: %s", err)
	}

	m.announceConn = announceConn
	m.listenerConn = listenerConn

	m.watchDevices()
	m.announceVirtualCDJ()
	m.listenForStatus()

	return nil
}
