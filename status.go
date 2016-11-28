package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
	playStateEmpty     PlayState = 0x00
	playStateLoading   PlayState = 0x02
	playStatePlaying   PlayState = 0x03
	playStateLooping   PlayState = 0x04
	playStatePaused    PlayState = 0x05
	playStateCued      PlayState = 0x06
	playStateCuing     PlayState = 0x07
	playStateSearching PlayState = 0x09
	playStateEnded     PlayState = 0x11
)

// Labels associated to the PlayState flags
var playStateLabels = map[PlayState]string{
	playStateEmpty:     "empty",
	playStateLoading:   "loading",
	playStatePlaying:   "playing",
	playStateLooping:   "looping",
	playStatePaused:    "paused",
	playStateCued:      "cued",
	playStateCuing:     "cuing",
	playStateSearching: "searching",
	playStateEnded:     "ended",
}

// PlayState represents the play state of the CDJ.
type PlayState byte

// String returns the string representation of the play state.
func (s PlayState) String() string {
	return playStateLabels[s]
}

// Track load slot flags
const (
	trackSlotEmpty TrackSlot = 0x00
	trackSlotCD    TrackSlot = 0x01
	trackSlotSD    TrackSlot = 0x02
	trackSlotUSB   TrackSlot = 0x03
	trackSlotRB    TrackSlot = 0x04
)

// Labels associated to the track load slot flags
var trackSlotLabels = map[TrackSlot]string{
	trackSlotEmpty: "empy",
	trackSlotCD:    "cd",
	trackSlotSD:    "sd",
	trackSlotUSB:   "usb",
	trackSlotRB:    "rekordbox",
}

// TrackSlot represents the slot that a track is loaded from on the CDJ.
type TrackSlot byte

// String returns the string representation of the track slot.
func (s TrackSlot) String() string {
	return trackSlotLabels[s]
}

// CDJStatus represents various details about the current state of the CDJ.
type CDJStatus struct {
	PlayerID       PlayerID
	TrackID        uint32
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
	TrackSlot      TrackSlot
	PacketNum      uint32
}

func packetToStatus(p []byte) (*CDJStatus, error) {
	b := binary.BigEndian

	if !bytes.HasPrefix(p, header) {
		return nil, fmt.Errorf("CDJ status packet does not start with the expected header")
	}

	if len(p) < 0xFF {
		return nil, nil
	}

	status := &CDJStatus{
		PlayerID:       PlayerID(p[0x21]),
		TrackID:        b.Uint32(p[0x2C : 0x2C+4]),
		PlayState:      PlayState(p[0x7B]),
		TrackSlot:      TrackSlot(p[0x29]),
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
