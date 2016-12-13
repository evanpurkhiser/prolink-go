package main

import (
	"fmt"

	"go.evanpurkhiser.com/prolink"
)

func main() {
	fmt.Println("-> Connecting to pro DJ Link network")
	network, _ := prolink.Connect()

	dm := network.DeviceManager()
	dj := network.CDJStatusMonitor()

	dm.OnDeviceAdded(func(dev *prolink.Device) {
		fmt.Printf("[+]: %s\n", dev)
	})

	dm.OnDeviceRemoved(func(dev *prolink.Device) {
		fmt.Printf("[-]: %s\n", dev)
	})

	dj.OnStatusUpdate(func(s *prolink.CDJStatus) {
		fmt.Printf("%+v\n", s)
	})

	<-make(chan bool)
}
