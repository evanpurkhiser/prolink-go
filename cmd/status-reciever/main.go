package main

import (
	"fmt"
	"time"

	"go.evanpurkhiser.com/prolink"
	"go.evanpurkhiser.com/prolink/mixstatus"
)

func main() {
	network, err := prolink.Connect()
	if err != nil {
		panic(err)
	}

	if err := network.AutoConfigure(3 * time.Second); err != nil {
		fmt.Println(err)
	}

	dj := network.CDJStatusMonitor()
	rb := network.RemoteDB()

	config := mixstatus.Config{
		AllowedInterruptBeats: 8,
		BeatsUntilReported:    128,
		TimeBetweenSets:       10 * time.Second,
	}

	handler := mixstatus.NewHandler(config, func(event mixstatus.Event, status *prolink.CDJStatus) {
		fmt.Printf("Event: %s\n", event)
		fmt.Println(status)

		if status.TrackID != 0 {
			track, err := rb.GetTrack(status.TrackQuery())
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println(track)
		}

		fmt.Println("---")
	})

	dj.OnStatusUpdate(handler)

	<-make(chan bool)
}
