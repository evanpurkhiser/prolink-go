package main

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
	"go.evanpurkhiser.com/prolink"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var conn *websocket.Conn

func trackLoads(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Failed to upgrade", err)
		return
	}
	defer c.Close()

	conn = c

	fmt.Println("connected")

	<-make(chan bool)
}

type trackJSON struct {
	DeviceID int     `json:"deck_id"`
	Title    *string `json:"title"`
	Artist   *string `json:"artist"`
	Album    *string `json:"album"`
	Label    *string `json:"label"`
	Release  *string `json:"release"`
	Artwork  *string `json:"artwork"`
}

func main() {
	fmt.Println("-> Connecting to pro DJ Link network")
	network, _ := prolink.Connect()

	dm := network.DeviceManager()
	dj := network.CDJStatusMonitor()
	rb := network.Rekordbox()

	dm.OnDeviceAdded(func(dev *prolink.Device) {
		fmt.Printf("[+]: %s\n", dev)
	})

	dm.OnDeviceRemoved(func(dev *prolink.Device) {
		fmt.Printf("[-]: %s\n", dev)
	})

	lastTrack := map[prolink.DeviceID]uint32{}

	dj.OnStatusUpdate(func(s *prolink.CDJStatus) {
		if s.TrackID == lastTrack[s.PlayerID] {
			return
		}

		if !rb.IsLinked() {
			return
		}

		lastTrack[s.PlayerID] = s.TrackID

		if s.TrackSlot != prolink.TrackSlotRB {
			return
		}

		if conn == nil {
			return
		}

		t, err := rb.GetTrack(s.TrackID)
		if err != nil {
			fmt.Printf("Failed to get track of id %d: %s", s.TrackID, err)
			return
		}

		track := trackJSON{
			DeviceID: int(s.PlayerID),
			Title:    &t.Title,
			Artist:   &t.Artist,
		}

		if t.Comment != "" {
			track.Release = &t.Comment
		} else {
			track.Release = nil
		}

		if t.Album != "" {
			track.Album = &t.Album
		} else {
			track.Album = nil
		}

		if t.Label != "" {
			track.Label = &t.Label
		} else {
			track.Label = nil
		}

		// TODO: Artwork should be empty when empty
		if len(t.Artwork) > 4 {
			art := base64.StdEncoding.EncodeToString(t.Artwork)
			art = fmt.Sprintf("data:%s;base64,%s", "image/jpeg", art)

			track.Artwork = &art
		}

		conn.WriteJSON(track)

		t.Artwork = nil

		fmt.Printf("LOADED [%d] %+v\n", s.PlayerID, t)
	})

	http.HandleFunc("/track-loads", trackLoads)
	http.ListenAndServe("localhost:8008", nil)

	<-make(chan bool)
}
