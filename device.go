package prolink

import (
	"fmt"
	"net"
	"time"
)

// Defined device types.
const (
	DeviceTypeCDJ   DeviceType = 0x01
	DeviceTypeMixer DeviceType = 0x03
	DeviceTypeRB    DeviceType = 0x04

	// DeviceTypeVCDJ is a custom device type that we will use to identify our
	// Virtual CDJ. Hopefully this isn't used by other pioneer equipment (?)
	DeviceTypeVCDJ DeviceType = 0x08
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
		packet := make([]byte, announcePacketLen)

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
			timeout, ok := timeouts[dev.ID]
			if !ok {
				return
			}

			timeout.Stop()
			timeout.Reset(deviceTimeout)
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
