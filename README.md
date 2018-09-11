## Pioneer PRO DJ LINK client

[![Build Status](https://www.travis-ci.com/EvanPurkhiser/prolink-go.svg?branch=master)](https://www.travis-ci.com/EvanPurkhiser/prolink-go)
[![GoDoc](https://godoc.org/go.evanpurkhiser.com/prolink?status.svg)](https://godoc.org/go.evanpurkhiser.com/prolink)

This go library provides an API to the Pioneer PRO DJ LINK network. Providing
various interactions and event subscribing.

Massive thank you to [@brunchboy](https://github.com/brunchboy) for his work on
[dysentery](https://github.com/brunchboy/dysentery).

```go
import "go.evanpurkhiser.com/prolink"
```

### Basic usage

```go
network, err := prolink.Connect()
network.AutoConfigure(5 * time.Second)

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

### Features

- Listen for Pioneer PRO DJ LINK devices to connect and disconnect from the
  network using the
  [`DeviceManager`](https://godoc.org/go.evanpurkhiser.com/prolink#DeviceManager).
  Currently active devices may also be queried.

- Receive Player status details for each CDJ on the network. The status is
  reported as
  [`CDJStatus`](https://godoc.org/go.evanpurkhiser.com/prolink#CDJStatus)
  structs.

- Query the Rekordbox remoteDB server present on both CDJs themselves and on
  the Rekordbox (PC / OSX / Android / iOS) software for track metadata using
  [`RemoteDB`](https://godoc.org/go.evanpurkhiser.com/prolink#RemoteDB). This
  includes most metadata fields as well as (low quality) album artwork.

- View the status of a DJ setup as a whole using the
  [`mixstatus.Handler`](https://godoc.org/github.com/EvanPurkhiser/prolink-go/mixstatus#Handler).
  This allows you to determine the status of tracks in a mixing situation. Has
  the track been playing long enough to be considered 'now playing'?

### Limitations, bugs, and missing functionality

- [[GH-1](https://github.com/EvanPurkhiser/prolink-go/issues/1)] Currently the
  software cannot be run on the same machine that is running Rekordbox.
  Rekordbox takes exclusive access to the socket used to communicate to the
  CDJs making it impossible to receive track status information

- [[GH-6](https://github.com/EvanPurkhiser/prolink-go/issues/6)] To read track
  metadata from the CDJs USB drives you may have no more than 3 CDJs. Having 4
  CDJs on the network will only allow you to read track metadata through
  linked Rekordbox.
