package main

import (
	"fmt"

	"go.evanpurkhiser.com/prolink"
	"go.evanpurkhiser.com/prolink/trackstatus"
)

func main() {
	fmt.Println("-> Connecting to pro DJ Link network")

	config := prolink.Config{
		NetIface:     "",
		VirtualCDJID: 0x04,
		UseSniffing:  false,
	}

	network, err := prolink.Connect(config)
	if err != nil {
		panic(err)
	}

	dm := network.DeviceManager()
	dj := network.CDJStatusMonitor()

	added := func(dev *prolink.Device) {
		fmt.Printf("[+]: %s\n", dev)
	}

	removed := func(dev *prolink.Device) {
		fmt.Printf("[-]: %s\n", dev)
	}

	dm.OnDeviceAdded(prolink.DeviceListenerFunc(added))
	dm.OnDeviceRemoved(prolink.DeviceListenerFunc(removed))

	trackChangeConfig := trackstatus.Config{
		AllowedInterruptBeats: 8,
		BeatsUntilReported:    128,
	}

	changed := func(devID prolink.DeviceID, track uint32, status trackstatus.Status) {
		fmt.Printf("Track ID %d on device %d is now in %s\n", track, devID, status)
	}

	trackStatusHandler := trackstatus.NewHandler(trackChangeConfig, changed)
	dj.OnStatusUpdate(trackStatusHandler)

	<-make(chan bool)
}
