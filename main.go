package main

import (
	"fmt"
)

func main() {
	manager := NetworkManager{}

	fmt.Println("Looking for PRO DJ LINK devices...\n")
	manager.Activate()

	<-make(chan bool)
}
