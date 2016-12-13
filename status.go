package prolink

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Status flag bitmasks
const (
	statusFlagLive    byte = 1 << 3
	statusFlagSync    byte = 1 << 4
	statusFlagMaster  byte = 1 << 5
	statusFlagPlaying byte = 1 << 6
)

// Play state flags
const (
	PlayStateEmpty     PlayState = 0x00
	PlayStateLoading   PlayState = 0x02
	PlayStatePlaying   PlayState = 0x03
	PlayStateLooping   PlayState = 0x04
	PlayStatePaused    PlayState = 0x05
	PlayStateCued      PlayState = 0x06
	PlayStateCuing     PlayState = 0x07
	PlayStateSearching PlayState = 0x09
	PlayStateEnded     PlayState = 0x11
)

// Labels associated to the PlayState flags
var playStateLabels = map[PlayState]string{
	PlayStateEmpty:     "empty",
	PlayStateLoading:   "loading",
	PlayStatePlaying:   "playing",
	PlayStateLooping:   "looping",
	PlayStatePaused:    "paused",
	PlayStateCued:      "cued",
	PlayStateCuing:     "cuing",
	PlayStateSearching: "searching",
	PlayStateEnded:     "ended",
}

// PlayState represents the play state of the CDJ.
type PlayState byte

// String returns the string representation of the play state.
func (s PlayState) String() string {
	return playStateLabels[s]
}

// Track load slot flags
const (
	TrackSlotEmpty TrackSlot = 0x00
	TrackSlotCD    TrackSlot = 0x01
	TrackSlotSD    TrackSlot = 0x02
	TrackSlotUSB   TrackSlot = 0x03
	TrackSlotRB    TrackSlot = 0x04
)

// Labels associated to the track load slot flags
var trackSlotLabels = map[TrackSlot]string{
	TrackSlotEmpty: "empy",
	TrackSlotCD:    "cd",
	TrackSlotSD:    "sd",
	TrackSlotUSB:   "usb",
	TrackSlotRB:    "rekordbox",
}

// TrackSlot represents the slot that a track is loaded from on the CDJ.
type TrackSlot byte

// String returns the string representation of the track slot.
func (s TrackSlot) String() string {
	return trackSlotLabels[s]
}

// CDJStatus represents various details about the current state of the CDJ.
type CDJStatus struct {
	PlayerID       DeviceID
	TrackID        uint32
	TrackDevice    DeviceID
	TrackSlot      TrackSlot
	PlayState      PlayState
	IsLive         bool
	IsSync         bool
	IsMaster       bool
	TrackBPM       float32
	EffectivePitch float32
	SliderPitch    float32
	BeatInMeasure  uint8
	BeatsUntilCue  uint16
	Beat           uint32
	PacketNum      uint32
}

// TrackQuery constructs a track query object from the CDJStatus. If no track
// is currently provided in the CDJStatus nil will be returned.
func (s *CDJStatus) TrackQuery() *TrackQuery {
	if s.TrackID == 0 {
		return nil
	}

	return &TrackQuery{
		DeviceID: s.TrackDevice,
		Slot:     s.TrackSlot,
		TrackID:  s.TrackID,
	}
}

func packetToStatus(p []byte) (*CDJStatus, error) {
	b := binary.BigEndian

	if !bytes.HasPrefix(p, prolinkHeader) {
		return nil, fmt.Errorf("CDJ status packet does not start with the expected header")
	}

	if len(p) < 0xFF {
		return nil, nil
	}

	status := &CDJStatus{
		PlayerID:       DeviceID(p[0x21]),
		TrackID:        b.Uint32(p[0x2C : 0x2C+4]),
		TrackDevice:    DeviceID(p[0x28]),
		TrackSlot:      TrackSlot(p[0x29]),
		PlayState:      PlayState(p[0x7B]),
		IsLive:         p[0x89]&statusFlagLive != 0,
		IsSync:         p[0x89]&statusFlagSync != 0,
		IsMaster:       p[0x89]&statusFlagMaster != 0,
		TrackBPM:       calcBPM(p[0x92 : 0x92+2]),
		SliderPitch:    calcPitch(p[0x8D : 0x8D+3]),
		EffectivePitch: calcPitch(p[0x99 : 0x99+3]),
		BeatInMeasure:  uint8(p[0xA6]),
		BeatsUntilCue:  b.Uint16(p[0xA4 : 0xA4+2]),
		Beat:           b.Uint32(p[0xA0 : 0xA0+4]),
		PacketNum:      b.Uint32(p[0xC8 : 0xC8+4]),
	}

	return status, nil
}

// calcPitch converts a uint24 byte value into a flaot32 pitch.
//
// The pitch information ranges from 0x000000 (meaning -100%, complete stop) to
// 0x200000 (+100%).
func calcPitch(p []byte) float32 {
	p = append([]byte{0x00}, p[:]...)

	v := float32(binary.BigEndian.Uint32(p))
	d := float32(0x100000)

	return (v - d) / d * 100
}

// calcBPM converts a uint16 byte value into a float32 bpm.
func calcBPM(p []byte) float32 {
	return float32(binary.BigEndian.Uint16(p)) / 100
}

// StatusHandler is a function that will be called when the status of a CDJ
// device has changed.
type StatusHandler func(status *CDJStatus)

// CDJStatusMonitor provides an interface for watching for status updates to
// CDJ devices on the PRO DJ LINK network.
type CDJStatusMonitor struct {
	handlers []StatusHandler
}

// OnStatusUpdate registers a StatusHandler to be called when any CDJ on the
// PRO DJ LINK network reports its status.
func (sm *CDJStatusMonitor) OnStatusUpdate(fn StatusHandler) {
	sm.handlers = append(sm.handlers, fn)
}

// activate triggers the CDJStatusMonitor to begin listening for status packets
// given a UDP connection to listen on.
func (sm *CDJStatusMonitor) activate(listenConn io.Reader) {
	packet := make([]byte, 512)

	statusUpdateHandler := func() {
		n, _ := listenConn.Read(packet)
		status, err := packetToStatus(packet[:n])
		if err != nil {
			return
		}

		if status == nil {
			return
		}

		for _, fn := range sm.handlers {
			go fn(status)
		}
	}

	go func() {
		for {
			statusUpdateHandler()
		}
	}()
}

func newCDJStatusMonitor() *CDJStatusMonitor {
	return &CDJStatusMonitor{handlers: []StatusHandler{}}
}
