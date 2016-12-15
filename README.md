## Pioneer PRO DJ LINK client

This go library provides an API to the Pioneer PRO DJ LINK network. This allows
you to listen in on and interact with the network in various ways.

Massive thank you to [@brunchboy](https://github.com/brunchboy) for his work on
[dysentery](https://github.com/brunchboy/dysentery).

[![GoDoc](https://godoc.org/go.evanpurkhiser.com/prolink?status.svg)](https://godoc.org/go.evanpurkhiser.com/prolink)

```go
import "go.evanpurkhiser.com/prolink"
```

### Basic usage

```go
network, err := prolink.Connect()

dm := network.DeviceManager()
st := network.CDJStatusMonitor()

added := func(dev *prolink.Device) {
    fmt.Printf("Connected: %s\n", dev)
}

removed := func(dev *prolink.Device) {
    fmt.Printf("Disconected: %s\n", dev)
}

dm.OnDeviceAdded(prolink.DeviceListenerFunc(added))
dm.OnDeviceRemoved(prolink.DeviceListenerFunc(removed))

statusChange := func(status *prolink.CDJStatus) {
    // Status packets come every 300ms, or faster depending on what is
    // happening on the CDJ. Do something with them.
}

st.OnStatusUpdate(prolink.StatusHandlerFunc(statusChange));
```
