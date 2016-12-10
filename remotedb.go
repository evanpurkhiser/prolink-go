package prolink

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"time"
	"unicode/utf16"
)

// ErrNoLinkedRekordbox is returned by Rekordbox if no rekordbox is on the network
var ErrNoLinkedRekordbox = fmt.Errorf("No linked Rekordbox on network")

// rbSeparator is a 6 byte marker used in TCP packets sent sent and received
// from the rekordbox db server. It's not particular known exactly what this
// value is for, but in some packets it seems to be used as a field separator.
var rbSeparator = []byte{0x11, 0x87, 0x23, 0x49, 0xae, 0x11}

// buildPacket constructs a packet to be sent to rekordbox.
func buildPacket(messageID uint32, part []byte) []byte {
	count := make([]byte, 4)
	binary.BigEndian.PutUint32(count, messageID)

	header := bytes.Join([][]byte{rbSeparator, count}, nil)

	return append(header, part...)
}

// Given a byte array where the first 4 bytes contain the uint32 length of the
// string (number of runes) followed by a UTF-16 representation of the string,
// convert it to a string.
func stringFromUTF16(s []byte) string {
	size := binary.BigEndian.Uint32(s[:4])
	s = s[4:][:size*2]

	str16Bit := make([]uint16, 0, size)
	for ; len(s) > 0; s = s[2:] {
		str16Bit = append(str16Bit, binary.BigEndian.Uint16(s[:2]))
	}

	return string(utf16.Decode(str16Bit))[:size-1]
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
	ID      uint32
	Path    string
	Title   string
	Artist  string
	Album   string
	Label   string
	Genre   string
	Comment string
	Key     string
	Length  time.Duration
	Artwork []byte
}

// Rekordbox provides an interface to talking to the rekordbox remote database.
type Rekordbox struct {
	conn     net.Conn
	deviceID DeviceID
	msgCount uint32
}

// OnLink adds a handler to be triggered when the Rekordbox DB server becomes
// available on the network.
func (rb *Rekordbox) OnLink() {
	// TODO
}

// IsLinked reports weather the Rekordbox DB server is available on the network.
func (rb *Rekordbox) IsLinked() bool {
	return rb.conn != nil
}

// GetTrack queries Rekordbox for track details given a Rekordbox track ID.
func (rb *Rekordbox) GetTrack(id uint32) (*Track, error) {
	if rb.conn == nil {
		return nil, ErrNoLinkedRekordbox
	}

	track, err := rb.queryTrackMetadata(id)
	if err != nil {
		return nil, err
	}

	path, err := rb.queryTrackPath(id)
	if err != nil {
		return nil, err
	}

	track.Path = path

	// No artwork, nothing left to do
	if binary.BigEndian.Uint32(track.Artwork) == 0 {
		return track, nil
	}

	artID := binary.BigEndian.Uint32(track.Artwork)

	artwork, err := rb.queryArtwork(artID)
	if err != nil {
		return nil, err
	}

	track.Artwork = artwork

	return track, nil
}

// queryTrackMetadata queries rekordbox for various metadata about a track,
// returing a sparse Track object. The track Path and Artwork must be looked up
// as separate queries.
//
// Note that the Artwork ID is populated in the Artwork field, as this value is
// returned with the track metadata and is needed to lookup the artwork.
func (rb *Rekordbox) queryTrackMetadata(id uint32) (*Track, error) {
	trackID := make([]byte, 4)
	binary.BigEndian.PutUint32(trackID, id)

	dvID := byte(rb.deviceID)
	slot := byte(TrackSlotRB)

	part1 := []byte{
		0x10, 0x20, 0x02, 0x0f, 0x02, 0x14, 0x00, 0x00,
		0x00, 0x0c, 0x06, 0x06, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, dvID,
		0x01, slot, 0x01, 0x11,
	}
	part1 = append(part1, trackID...)

	part2 := []byte{
		0x10, 0x30, 0x00, 0x0f, 0x06, 0x14, 0x00, 0x00,
		0x00, 0x0c, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, dvID,
		0x01, slot, 0x01, 0x11, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00, 0x0b, 0x11, 0x00, 0x00,
		0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x0b, 0x11,
		0x00, 0x00, 0x00, 0x00,
	}

	items, err := rb.getMultimessageResp(part1, part2)
	if err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(items[3][28:32])

	track := &Track{
		ID:      id,
		Title:   stringFromUTF16(items[0][38:]),
		Artist:  stringFromUTF16(items[1][38:]),
		Album:   stringFromUTF16(items[2][38:]),
		Comment: stringFromUTF16(items[5][38:]),
		Key:     stringFromUTF16(items[6][38:]),
		Genre:   stringFromUTF16(items[9][38:]),
		Label:   stringFromUTF16(items[10][38:]),
		Length:  time.Duration(length) * time.Second,

		// Artwork will be given in with the artwork ID
		Artwork: items[0][len(items[0])-19:][:4],
	}

	return track, nil

}

// queryTrackPath looks up the file path of a track in rekordbox.
func (rb *Rekordbox) queryTrackPath(id uint32) (string, error) {
	trackID := make([]byte, 4)
	binary.BigEndian.PutUint32(trackID, id)

	dvID := byte(rb.deviceID)

	part1 := []byte{
		0x10, 0x21, 0x02, 0x0f, 0x02, 0x14, 0x00, 0x00,
		0x00, 0x0c, 0x06, 0x06, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, dvID,
		0x08, 0x04, 0x01, 0x11,
	}
	part1 = append(part1, trackID...)

	part2 := []byte{
		0x10, 0x30, 0x00, 0x0f, 0x06, 0x14, 0x00, 0x00,
		0x00, 0x0c, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, dvID,
		0x08, 0x04, 0x01, 0x11, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00, 0x06, 0x11, 0x00, 0x00,
		0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x06, 0x11,
		0x00, 0x00, 0x00, 0x00,
	}

	items, err := rb.getMultimessageResp(part1, part2)
	if err != nil {
		return "", err
	}

	return stringFromUTF16(items[4][38:]), nil
}

// getMultimessageResp is used for queries that that multiple packets to setup
// and respond with mult-section bodies that can be split on the rbSection
// delimiter.
func (rb *Rekordbox) getMultimessageResp(p1, p2 []byte) ([][]byte, error) {
	// Part one of query
	packet := buildPacket(rb.msgCount, p1)

	if err := rb.sendMessage(packet); err != nil {
		return nil, fmt.Errorf("Multipart query failed: %s", err)
	}

	// This data doesn't seem useful, there *should* be 42 bytes of it
	io.CopyN(ioutil.Discard, rb.conn, 42)

	// Part two of query
	packet = buildPacket(rb.msgCount, p2)

	// As far as I can tell, these multi-section packets *do not* have a length
	// marker for bytes in the message, or even how many sections they will
	// have. So for now, look for the 'final section' which seems to always be
	// empty. We can reuse buildPacket here even though this is not a packet.
	finalSection := buildPacket(rb.msgCount, []byte{
		0x10, 0x42, 0x01, 0x0f, 0x00, 0x14, 0x00, 0x00, 0x00,
		0x0c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	})

	if err := rb.sendMessage(packet); err != nil {
		return nil, fmt.Errorf("Multipart query failed: %s", err)
	}

	part := make([]byte, 1024)
	full := []byte{}

	for !bytes.HasSuffix(full, finalSection) {
		n, err := rb.conn.Read(part)
		if err != nil {
			return nil, fmt.Errorf("Could not read multipart response: %s", err)
		}

		full = append(full, part[:n]...)
	}

	// Break into sections (keep only interesting ones
	sections := bytes.Split(full, rbSeparator)[2:]
	sections = sections[:len(sections)-1]

	// Remove uint32 message counter from each section
	for i := range sections {
		sections[i] = sections[i][4:]
	}

	return sections, nil
}

// queryArtwork requests artwork of a specific ID from rekordbox.
func (rb *Rekordbox) queryArtwork(id uint32) ([]byte, error) {
	artID := make([]byte, 4)
	binary.BigEndian.PutUint32(artID, id)

	dvID := byte(rb.deviceID)

	part := []byte{
		0x10, 0x20, 0x03, 0x0f, 0x02, 0x14, 0x00, 0x00,
		0x00, 0x0c, 0x06, 0x06, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, dvID,
		0x08, 0x04, 0x01, 0x11,
	}
	part = append(part, artID...)

	packet := buildPacket(rb.msgCount, part)

	if err := rb.sendMessage(packet); err != nil {
		return nil, fmt.Errorf("Artwork query failed: %s", err)
	}

	// there is a uint32 at byte 48 containing the size of the image, simply
	// read up until this value so we know how much more to read after.
	data := make([]byte, 52)

	_, err := rb.conn.Read(data)
	if err != nil {
		return nil, err
	}

	imgLen := binary.BigEndian.Uint32(data[48:52])
	img := make([]byte, int(imgLen))

	_, err = io.ReadFull(rb.conn, img)
	if err != nil {
		return nil, fmt.Errorf("Failed to read artwork data stream: %s", err)
	}

	return img, nil
}

// sendMessage writes to the open connection and increments the message
// counter.
func (rb *Rekordbox) sendMessage(m []byte) error {
	if _, err := rb.conn.Write(m); err != nil {
		return err
	}

	rb.msgCount++

	return nil
}

// openConnection initializes the TCP connection to the specified address on
// which the rekordbox remote DB is presumed to be running. This sends the
// appropriate packets to initialize the communication between a fake device
// (this host) and rekordbox.
func (rb *Rekordbox) openConnection(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	// Begin connection to rekordbox database
	if _, err = conn.Write([]byte{0x11, 0x00, 0x00, 0x00, 0x01}); err != nil {
		return fmt.Errorf("Failed to connect to rekordbox: %s", err)
	}

	// No need to keep this response, but it *should* be 5 bytes
	io.CopyN(ioutil.Discard, conn, 5)

	// Send identification to rekordbox
	identifyParts := [][]byte{
		rbSeparator,

		// Possibly resets the
		[]byte{0xff, 0xff, 0xff, 0xfe},

		// Currently don't know what these bytes do, but they're needed to get
		// the connection into a state where we can make queries
		[]byte{
			0x10, 0x00, 0x00, 0x0f, 0x01, 0x14, 0x00, 0x00,
			0x00, 0x0c, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, 0x00,
			0x00, 0x00,
		},

		// The last byte of the identifier is the device ID that we are assuming
		// to use to communicate with rekordbox
		[]byte{byte(rb.deviceID)},
	}

	if _, err = conn.Write(bytes.Join(identifyParts, nil)); err != nil {
		return fmt.Errorf("Failed to connect to rekordbox: %s", err)
	}

	// No need to keep this response, but it *should be 42 bytes
	io.CopyN(ioutil.Discard, conn, 42)

	rb.conn = conn
	rb.msgCount = 1

	return nil
}

// activate begins actively listening for a rekordbox device to be added to the
// PRO DJ LINK network, at which time a we will poll to connect to rekordbox,
// as it must be linked first. When a rekordbox device is removed the
// connection will be removed.
func (rb *Rekordbox) activate(dm *DeviceManager, deviceID DeviceID) {
	rb.deviceID = deviceID

	// TODO: This isn't robust at all, handle polling for the DB server, since
	// it won't always be available OR figure out how to tell when it does
	// become available.
	dm.OnDeviceAdded(func(dev *Device) {
		if dev.Type != DeviceTypeRB {
			return
		}

		addr, err := getRemoteDBServerAddr(net.ParseIP("192.168.1.3"))
		if err != nil {
			fmt.Println(err)
			return
		}

		rb.openConnection(addr)
	})
}

func newRekordbox() *Rekordbox {
	return &Rekordbox{}
}
