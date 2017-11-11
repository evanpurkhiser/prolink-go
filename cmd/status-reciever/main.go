package main

import (
	"fmt"
	"net"

	"go.evanpurkhiser.com/prolink"
)

func main() {
	fmt.Println("-> Connecting to pro DJ Link network")

	network, err := prolink.Connect()
	if err != nil {
		panic(err)
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Name == "en0" {
			network.SetInterface(&iface)
			break
		}
	}

	network.SetVirtualCDJID(prolink.DeviceID(0x04))

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
