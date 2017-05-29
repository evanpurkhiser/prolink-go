package main

import (
	"fmt"

	"go.evanpurkhiser.com/prolink"
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

	dj.OnStatusUpdate(prolink.StatusHandlerFunc(func(s *prolink.CDJStatus) {
		fmt.Println(s)
	}))

	<-make(chan bool)
}
