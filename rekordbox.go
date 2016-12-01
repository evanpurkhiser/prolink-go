package prolink

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"unicode/utf16"
)

// ErrNoLinkedRekordbox is returned by Rekordbox if no rekordbox is on the network
var ErrNoLinkedRekordbox = fmt.Errorf("No linked Rekordbox on network")

// rbSeparator is a 6 byte marker used in TCP packets sent sent and recieved
// from the rekordbox db server. It's not particular known exactly what this
// value is for, but in some larger packets it's used as a separator.
var rbSeparator = []byte{0x11, 0x87, 0x23, 0x49, 0xae, 0x11}

// trackQuerySequence is the sequence of bytes that should be sent to Rekordbox
// to obtain track details. Currently not a lot is known about this sequence:
//
//  - The query returns details primarily including the file path for now, the
//    sequence of packets seems to be slightly different to get a different
//    metadata.
//
//  - This sequence can currently only be played once per open TCP connection
//    to rekordbox. It's likely that more packets need to be sent to put
//    rekordbox in a state where it will respond again.
//
//  - The 3rd packet includes the 4byte track ID appened to the end
//
//  - Note that the first packet does not include the rbSeperator, however the
//    next 3 do as the first six bytes.
var trackQuerySequence = [][]byte{
	[]byte{
		0x11, 0x00, 0x00, 0x00, 0x01,
	},
	[]byte{
		0x11, 0x87, 0x23, 0x49, 0xae, 0x11, 0xff, 0xff,
		0xff, 0xfe, 0x10, 0x00, 0x00, 0x0f, 0x01, 0x14,
		0x00, 0x00, 0x00, 0x0c, 0x06, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00, 0x02,
	},

	[]byte{
		0x11, 0x87, 0x23, 0x49, 0xae, 0x11, 0xff, 0xff,
		0xff, 0xfd, 0x10, 0x21, 0x02, 0x0f, 0x02, 0x14,
		0x00, 0x00, 0x00, 0x0c, 0x06, 0x06, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x02, 0x08, 0x04, 0x01, 0x11,
		// 4 byte track ID should be appened here
	},
	[]byte{
		0x11, 0x87, 0x23, 0x49, 0xae, 0x11, 0xff, 0xff,
		0xff, 0xfc, 0x10, 0x30, 0x00, 0x0f, 0x06, 0x14,
		0x00, 0x00, 0x00, 0x0c, 0x06, 0x06, 0x06, 0x06,
		0x06, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x02, 0x08, 0x04, 0x01, 0x11, 0x00, 0x00,
		0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x06, 0x11,
		0x00, 0x00, 0x00, 0x00, 0x11, 0x00, 0x00, 0x00,
		0x06, 0x11, 0x00, 0x00, 0x00, 0x00,
	},
}

// getTrackQuerySequence incorperates the track ID into the query sequence.
func getTrackQuerySequence(trackID uint32) [][]byte {
	sequence := trackQuerySequence

	byteID := make([]byte, 4)
	binary.BigEndian.PutUint32(byteID, trackID)

	sequence[2] = append(sequence[2], byteID...)

	return sequence
}

// decodeFilePathFromPacket decodes the UTF-16-BE file path from a file path
// query packet.
func decodeFilePathFromPacket(p []byte) string {
	if len(p) < 46 {
		return ""
	}

	// The track path is 46 bytes into the 6th separated portion of the packet
	p = bytes.Split(p, rbSeparator)[6][46:]
	p = bytes.Split(p, []byte{0x00, 0x00, 0x11})[0]

	data16Bit := make([]uint16, 0, len(p)/2)
	for ; len(p) > 0; p = p[2:] {
		data16Bit = append(data16Bit, binary.BigEndian.Uint16(p[:2]))
	}

	return string(utf16.Decode(data16Bit))
}

// rbDBServerQueryPort is the consistent port on which we can query rekordbox
// for the port to connect to to communicate with the database server.
const rbDBServerQueryPort = 12523

// getRemoteDBServerAddr queries rekordbox for the port that the rekordbox
// remote database server is listening on for requests.
func getRemoteDBServerAddr(rekordboxIP net.IP) (string, error) {
	addr := fmt.Sprintf("%s:%d", rekordboxIP, rbDBServerQueryPort)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return "", err
	}

	defer conn.Close()

	parts := [][]byte{
		[]byte{0x00, 0x00, 0x00, 0x0f},
		[]byte("RemoteDBServer"),
		[]byte{0x00},
	}

	queryPacket := bytes.Join(parts, nil)

	// Request for the port
	_, err = conn.Write(queryPacket)
	if err != nil {
		return "", fmt.Errorf("Failed to query remote DB Server port: %s", err)
	}

	// Read request response, should be a two byte uint16
	data := make([]byte, 2)

	_, err = conn.Read(data)
	if err != nil {
		return "", fmt.Errorf("Failed to retrieve remote DB Server port: %s", err)
	}

	port := binary.BigEndian.Uint16(data)

	return fmt.Sprintf("%s:%d", rekordboxIP, port), nil
}

// Track contains track information retrieved from rekordbox.
type Track struct {
	ID   uint32
	Path string
}

// Rekordbox provides an interface to talking to the rekordbox remote database.
type Rekordbox struct {
	linked  bool
	address string
}

// GetTrack queries Rekordbox for track details given a Rekordbox track ID.
func (rb *Rekordbox) GetTrack(id uint32) (*Track, error) {
	if !rb.linked {
		return nil, ErrNoLinkedRekordbox
	}

	conn, err := net.Dial("tcp", rb.address)
	if err != nil {
		return nil, err
	}

	packetSequence := getTrackQuerySequence(id)

	data := make([]byte, 1024)

	for _, packet := range packetSequence {
		_, err = conn.Write(packet)
		if err != nil {
			return nil, err
		}

		_, err := conn.Read(data)
		if err != nil {
			return nil, err
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("Could not load track details from Rekordbox DB server")
	}

	track := &Track{
		ID:   id,
		Path: decodeFilePathFromPacket(data),
	}

	return track, nil
}

// OnLink adds a handler to be triggered when the Rekordbox DB server becomes
// available on the network.
func (rb *Rekordbox) OnLink() {
	// TODO
}

// IsLinked reports weather the Rekordbox DB server is available on the network.
func (rb *Rekordbox) IsLinked() bool {
	return rb.linked
}

func (rb *Rekordbox) activate(dm *DeviceManager) {
	// TODO: This isn't robust at all, handle polling for the DB server, since
	// it won't always be avaialbel OR figure out how to tell when it does
	// become available.
	dm.OnDeviceAdded(func(dev *Device) {
		if dev.Type != DeviceTypeRB {
			return
		}

		addr, err := getRemoteDBServerAddr(dev.IP)
		if err != nil {
			fmt.Println(err)
			return
		}

		rb.address = addr
		rb.linked = true
	})
}

func newRekordbox() *Rekordbox {
	return &Rekordbox{}
}
