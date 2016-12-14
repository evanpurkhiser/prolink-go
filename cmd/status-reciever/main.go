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
	rb := network.RemoteDB()

	dm.OnDeviceAdded(func(dev *prolink.Device) {
		fmt.Printf("[+]: %s\n", dev)
	})

	dm.OnDeviceRemoved(func(dev *prolink.Device) {
		fmt.Printf("[-]: %s\n", dev)
	})

	lastTrack := map[prolink.DeviceID]uint32{}

	dj.OnStatusUpdate(func(s *prolink.CDJStatus) {
		query := s.TrackQuery()

		if query == nil || !rb.IsLinked(query.DeviceID) {
			return
		}

		if s.TrackID == lastTrack[s.PlayerID] {
			return
		}

		fmt.Printf("%+v\n", s)

		lastTrack[s.PlayerID] = s.TrackID

		track, err := rb.GetTrack(query)
		fmt.Println(track, err)
	})

	<-make(chan bool)
}
