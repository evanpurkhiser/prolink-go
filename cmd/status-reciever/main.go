package main

import (
	"fmt"
	"time"

	"go.evanpurkhiser.com/prolink"
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

	dj.OnStatusUpdate(prolink.StatusHandlerFunc(func(s *prolink.CDJStatus) {
		fmt.Println(s)
	}))

	<-make(chan bool)
}
