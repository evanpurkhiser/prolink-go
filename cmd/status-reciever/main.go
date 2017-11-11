package main

import (
	"fmt"
	"time"

	"go.evanpurkhiser.com/prolink"
)

func main() {
	fmt.Println("-> Connecting to pro DJ Link network")

	network, err := prolink.Connect()
	if err != nil {
		panic(err)
	}

	if err := network.AutoConfigure(3 * time.Second); err != nil {
		fmt.Println("Unable to autoconfigure: %s", err)
	} else {
		fmt.Println("-> Autoconfigured with values:")
		fmt.Printf("   Interface: %s\n", network.TargetInterface.Name)
		fmt.Printf("   Virtual CDJ ID: %d\n", network.VirtualCDJID)
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
