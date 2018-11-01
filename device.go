package prolink

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Defined device types.
const (
	DeviceTypeCDJ   DeviceType = 0x01
	DeviceTypeMixer DeviceType = 0x03
	DeviceTypeRB    DeviceType = 0x04
)

var deviceTypeLabels = map[DeviceType]string{
	DeviceTypeCDJ:   "cdj",
	DeviceTypeMixer: "djm",
	DeviceTypeRB:    "rekordbox",
}

// VirtualCDJName is the name given to the Virtual CDJ device.
const VirtualCDJName = "prolink-go"

// VirtualCDJFirmware is a string indicating the firmware version reported with
// status packets.
const VirtualCDJFirmware = "1.43"

// DeviceType represents the types of devices on the network.
type DeviceType byte

// String returns a string representation of a device.
func (d DeviceType) String() string {
	return deviceTypeLabels[d]
}

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

// A DeviceListener responds to devices being added and removed from the PRO DJ
// LINK network.
type DeviceListener interface {
	OnChange(*Device)
}

// The DeviceListenerFunc is an adapter to allow a function to be used as a
// listener for device changes.
type DeviceListenerFunc func(*Device)

// OnChange implements the DeviceListener interface.
func (f DeviceListenerFunc) OnChange(d *Device) { f(d) }

// DeviceManager provides functionality for watching the connection status of
// PRO DJ LINK devices on the network.
type DeviceManager struct {
	delHandlers map[string]DeviceListener
	addHandlers map[string]DeviceListener
	devices     map[DeviceID]*Device
}

// OnDeviceAdded registers a listener that will be called when any PRO DJ LINK
// devices are added to the network. Provide a key if you wish to remove the
// handler later with RemoveListener by specifying the same key.
func (m *DeviceManager) OnDeviceAdded(key string, fn DeviceListener) {
	m.addHandlers[key] = fn
}

// OnDeviceRemoved registers a listener that will be called when any PRO DJ
// LINK devices are removed from the network. Provide a key if you wish to
// remove the handler later with RemoveListener by specifying the same key.
func (m *DeviceManager) OnDeviceRemoved(key string, fn DeviceListener) {
	m.delHandlers[key] = fn
}

// RemoveListener removes a DeviceListener that may have been added by
// OnDeviceAdded or OnDeviceRemoved. Use the key you provided when adding the
// handler.
func (m *DeviceManager) RemoveListener(key string, fn DeviceListener) {
	delete(m.addHandlers, key)
	delete(m.delHandlers, key)
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
	timeoutsLock := sync.Mutex{}

	Log.Info("Now monitoring for PROLINK devices")

	timeoutTimer := func(dev *Device) {
		timeoutsLock.Lock()
		defer timeoutsLock.Unlock()

		timeouts[dev.ID] = time.NewTimer(deviceTimeout)
		<-timeouts[dev.ID].C

		// Device timeout expired. No longer active
		delete(timeouts, dev.ID)
		delete(m.devices, dev.ID)

		Log.Info("Device timeout", "device", dev)

		for _, h := range m.delHandlers {
			go h.OnChange(dev)
		}
	}

	announceLock := sync.Mutex{}

	announceHandler := func() {
		packet := make([]byte, announcePacketLen)

		announceConn.Read(packet)
		dev, err := deviceFromAnnouncePacket(packet)
		if err != nil {
			return
		}

		if dev.Name == VirtualCDJName {
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

		announceLock.Lock()
		defer announceLock.Unlock()

		// New device
		m.devices[dev.ID] = dev

		Log.Info("New device tracked", "device", dev)

		for _, h := range m.addHandlers {
			go h.OnChange(dev)
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
		addHandlers: map[string]DeviceListener{},
		delHandlers: map[string]DeviceListener{},
		devices:     map[DeviceID]*Device{},
	}
}
