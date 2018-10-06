package prolink

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"
)

// ErrDeviceNotLinked is returned by RemoteDB if the device being queried is
// not currently 'linked' on the network.
var ErrDeviceNotLinked = fmt.Errorf("The device is not linked on the network")

// allowedDevices specify what device types act as a remote DB server
var allowedDevices = map[DeviceType]bool{
	DeviceTypeRB:  true,
	DeviceTypeCDJ: true,
}

// rbDBServerQueryPort is the consistent port on which we can query the remote
// db server for the port to connect to to communicate with it.
const rbDBServerQueryPort = 12523

// getRemoteDBServerAddr queries the remote device for the port that the remote
// database server is listening on for requests.
func getRemoteDBServerAddr(deviceIP net.IP) (string, error) {
	addr := fmt.Sprintf("%s:%d", deviceIP, rbDBServerQueryPort)

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

	return fmt.Sprintf("%s:%d", deviceIP, port), nil
}

type deviceConnection struct {
	remoteDB *RemoteDB
	device   *Device
	lock     *sync.Mutex
	conn     net.Conn
	txCount  uint32
	timeout  time.Duration

	retryEvery time.Duration
	disconnect chan bool
}

// connect attempts to open a TCP socket connection to the device. This will
// send the necessary packet sequence in order start communicating with the
// database server once connected.
func (dc *deviceConnection) connect() error {
	addr, err := getRemoteDBServerAddr(dc.device.IP)
	if err != nil {
		return err
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	if err := conn.SetDeadline(time.Now().Add(dc.timeout)); err != nil {
		return err
	}

	// Begin connection to the remote database
	preamble := fieldNumber04(0x01)
	if _, err = conn.Write(preamble.bytes()); err != nil {
		return fmt.Errorf("Failed to connect to remote database: %s", err)
	}

	// No need to keep this response, but it should be a uin32 field, which is
	// 5 bytes in length. Discard it.
	io.CopyN(ioutil.Discard, conn, 5)

	introPacket := &introducePacket{
		deviceID: dc.remoteDB.deviceID,
	}

	if _, err = conn.Write(introPacket.bytes()); err != nil {
		return fmt.Errorf("Failed to connect to remote database: %s", err)
	}

	if _, err := readMessagePacket(conn); err != nil {
		return err
	}

	dc.conn = conn

	return nil
}

func (dc *deviceConnection) tryConnect(ticker *time.Ticker) bool {
	select {
	case <-dc.disconnect:
		return true
	case <-ticker.C:
		return dc.connect() == nil
	}
}

func (dc *deviceConnection) ensureConnect() {
	dc.disconnect = make(chan bool, 1)
	ticker := time.NewTicker(dc.retryEvery)

	// Attempt to immediately connect
	dc.connect()

	for dc.conn == nil && !dc.tryConnect(ticker) {
	}

	ticker.Stop()
}

// Open begins attempting to connect to the device. If we're unable to connect
// to the device we will retry until the deviceConnection is closed.
func (dc *deviceConnection) Open() {
	go dc.ensureConnect()
}

// Close stops any attempts to connect to the device or closes any open socket
// connections with the device.
func (dc *deviceConnection) Close() {
	if dc.disconnect != nil {
		dc.disconnect <- true
		close(dc.disconnect)
	}

	if dc.conn != nil {
		dc.conn.Close()
		dc.conn = nil
	}
}

// Read implenets the Reader interface and will read from the device. This
// updates the read deadline of the connection.
func (dc *deviceConnection) Read(p []byte) (n int, err error) {
	dc.conn.SetReadDeadline(time.Now().Add(dc.timeout))
	return dc.conn.Read(p)
}

// Write implenets the Writer interface and will write to the device. This
// updates the write deadline of the connection.
func (dc *deviceConnection) Write(p []byte) (n int, err error) {
	dc.conn.SetWriteDeadline(time.Now().Add(dc.timeout))
	return dc.conn.Write(p)
}

// Track contains track information retrieved from the remote database.
type Track struct {
	ID        uint32
	Path      string
	Title     string
	Artist    string
	Album     string
	Label     string
	Genre     string
	Comment   string
	Key       string
	Length    time.Duration
	DateAdded time.Time
	Artwork   []byte
}

func (t Track) String() string {
	format := `Track %d
  Path:    %s
  Title:   %s
  Artist:  %s
  Album:   %s
  Label:   %s
  Genre:   %s
  Comment: %s
  Key:     %s
  Length:  %s
  Added:   %s
  HasArt:  %t`

	return fmt.Sprintf(format,
		t.ID,
		t.Path,
		t.Title,
		t.Artist,
		t.Album,
		t.Label,
		t.Genre,
		t.Comment,
		t.Key,
		t.Length,
		t.DateAdded,
		len(t.Artwork) > 0,
	)
}

// TrackKey is used to make queries for track metadata.
type TrackKey struct {
	TrackID  uint32
	Slot     TrackSlot
	Type     TrackType
	DeviceID DeviceID

	// artworkID will be filled in after the track metadata is queried, this
	// feild will be needed to lookup the track artwork.
	artworkID uint32
}

// NewTrackKey constructs a TrackKey from humany friendly representation of
// track keys. Slot and Type are strings, device and trackID are ints.
func NewTrackKey(id int, slot, trackType string, deviceID int) *TrackKey {
	tk := TrackKey{
		TrackID:  uint32(id),
		Slot:     labelsTrackSlot[slot],
		Type:     labelsTrackType[trackType],
		DeviceID: DeviceID(deviceID),
	}

	return &tk
}

// RemoteDB provides an interface to talking to the remote database.
type RemoteDB struct {
	deviceID  DeviceID
	conns     map[DeviceID]*deviceConnection
	connsLock *sync.Mutex
}

// IsLinked reports weather the DB server is available for the given device.
func (rd *RemoteDB) IsLinked(devID DeviceID) bool {
	devConn, ok := rd.conns[devID]

	return ok && devConn.conn != nil
}

// GetTrack queries the remote db for track details given a track ID.
func (rd *RemoteDB) GetTrack(q *TrackKey) (*Track, error) {
	if !rd.IsLinked(q.DeviceID) {
		return nil, ErrDeviceNotLinked
	}

	track, err := rd.executeQuery(q)

	// Refresh the connection if we EOF while querying the server
	if err != nil && err == io.EOF {
		Log.Warn("Got EOF while querying server. Reconnecting", "device", q.DeviceID)
		rd.refreshConnection(rd.conns[q.DeviceID].device)
	}

	return track, err
}

func (rd *RemoteDB) executeQuery(q *TrackKey) (*Track, error) {
	// Synchroize queries as not to distrupt the query flow. We could probably
	// be a little more precice about where the locks are, but for now the
	// entire query is "pretty fast", just lock the whole thing.
	lock := rd.conns[q.DeviceID].lock

	lock.Lock()
	defer lock.Unlock()

	if _, ok := rd.conns[q.DeviceID]; !ok {
		return nil, fmt.Errorf("Device disconnected during query")
	}

	track, err := rd.queryTrackMetadata(q)
	if err != nil {
		return nil, err
	}

	path, err := rd.queryTrackPath(q)
	if err != nil {
		return nil, err
	}

	track.Path = path

	artwork, err := rd.getArtwork(q)
	if err != nil {
		return nil, err
	}

	track.Artwork = artwork

	return track, nil
}

// queryTrackMetadata queries the rmote database for various metadata about a
// track, returing a sparse Track object. The track Path and Artwork must be
// looked up as separate queries.
//
// Note that the Artwork ID is populated into the passed TrackKey after this
// call completes.
func (rd *RemoteDB) queryTrackMetadata(q *TrackKey) (*Track, error) {
	trackID := make([]byte, 4)
	binary.BigEndian.PutUint32(trackID, q.TrackID)

	getMetadata := &metadataRequestPacket{
		deviceID:  rd.deviceID,
		slot:      q.Slot,
		trackType: q.Type,
		trackID:   q.TrackID,
	}

	renderRequest := &renderRequestPacket{
		deviceID:  rd.deviceID,
		slot:      q.Slot,
		trackType: q.Type,
		offset:    0,
		limit:     32,
	}

	items, err := rd.getMenuItems(q.DeviceID, getMetadata, renderRequest)
	if err != nil {
		return nil, err
	}

	q.artworkID = items[itemTypeTitle].artworkID

	duration := time.Duration(items.getNum(itemTypeDuration)) * time.Second

	track := &Track{
		ID:      q.TrackID,
		Title:   items.getText(itemTypeTitle),
		Artist:  items.getText(itemTypeArtist),
		Album:   items.getText(itemTypeAlbum),
		Comment: items.getText(itemTypeComment),
		Key:     items.getText(itemTypeKey),
		Genre:   items.getText(itemTypeGenre),
		Label:   items.getText(itemTypeLabel),
		Length:  duration,
	}

	return track, nil
}

// queryTrackPath looks up the file path of a track in rekordbox.
func (rd *RemoteDB) queryTrackPath(q *TrackKey) (string, error) {
	trackID := make([]byte, 4)
	binary.BigEndian.PutUint32(trackID, q.TrackID)

	trackInfoRequest := &trackInfoRequestPacket{
		deviceID:  rd.deviceID,
		slot:      q.Slot,
		trackType: q.Type,
		trackID:   q.TrackID,
	}

	renderRequest := &renderRequestPacket{
		renderType: renderSystem,
		deviceID:   rd.deviceID,
		slot:       q.Slot,
		trackType:  q.Type,
		offset:     0,
		limit:      32,
	}

	items, err := rd.getMenuItems(q.DeviceID, trackInfoRequest, renderRequest)
	if err != nil {
		return "", err
	}

	return items.getText(itemTypePath), nil
}

// getMenuItems is used to query a list of menu items. It returns a mapping of
// the menu itemType byte to the menu item packet object.
func (rd *RemoteDB) getMenuItems(devID DeviceID, p1, p2 messagePacket) (menuItems, error) {
	if err := rd.sendMessage(devID, p1); err != nil {
		return nil, err
	}

	resp, err := readMessagePacket(rd.conns[devID])
	if err != nil {
		return nil, err
	}

	if resp.messageType != msgTypeResponse {
		return nil, fmt.Errorf("Invalid menu items request, got response type %#x", resp.messageType)
	}

	if err := rd.sendMessage(devID, p2); err != nil {
		return nil, err
	}

	// Add 2 for the menu header / footer
	entryCount := int(resp.arguments[1].(fieldNumber04)) + 2

	items := map[byte]*menuItem{}

	for i := 0; i < entryCount; i++ {
		entry, err := readMessagePacket(rd.conns[devID])
		if err != nil {
			return nil, err
		}

		if entry.messageType != msgTypeMenuItem {
			continue
		}

		item := makeMenuItem(entry)
		items[item.itemType] = item
	}

	return menuItems(items), nil
}

// getArtwork requests artwork of a specific ID from the remote database.
func (rd *RemoteDB) getArtwork(q *TrackKey) ([]byte, error) {
	artworkRequest := &requestArtwork{
		deviceID:  rd.deviceID,
		slot:      q.Slot,
		trackType: q.Type,
		artworkID: q.artworkID,
	}

	if err := rd.sendMessage(q.DeviceID, artworkRequest); err != nil {
		return nil, err
	}

	resp, err := readMessagePacket(rd.conns[q.DeviceID])
	if err != nil {
		return nil, err
	}

	if resp.messageType != msgTypeArtwork {
		return nil, fmt.Errorf("Invalid artwork request, got response type %#x", resp.messageType)
	}

	return []byte(resp.arguments[3].(fieldBinary)), nil
}

// sendMessage writes a message packet to the open connection and increments
// the transaction counter.
func (rd *RemoteDB) sendMessage(devID DeviceID, m messagePacket) error {
	devConn := rd.conns[devID]

	Log.Debug("Sending packet", "packet", m)

	m.setTransactionID(devConn.txCount)
	if _, err := devConn.Write(m.bytes()); err != nil {
		return err
	}

	devConn.txCount++

	return nil
}

// openConnection initializes a new deviceConnection for the specified device.
func (rd *RemoteDB) openConnection(dev *Device) {
	if _, ok := allowedDevices[dev.Type]; !ok {
		return
	}

	Log.Info("Opening Remote DB connection", "target", dev)

	conn := &deviceConnection{
		remoteDB:   rd,
		device:     dev,
		lock:       &sync.Mutex{},
		txCount:    1,
		retryEvery: 5 * time.Second,
		timeout:    5 * time.Second,
	}

	conn.Open()

	rd.connsLock.Lock()
	defer rd.connsLock.Unlock()

	rd.conns[dev.ID] = conn
}

// closeConnection closes the active connection for the specified device.
func (rd *RemoteDB) closeConnection(dev *Device) {
	if _, ok := rd.conns[dev.ID]; !ok {
		return
	}

	rd.conns[dev.ID].Close()

	rd.connsLock.Lock()
	defer rd.connsLock.Unlock()

	delete(rd.conns, dev.ID)
}

// refreshConnection attempts to reconnect to the specified device.
func (rd *RemoteDB) refreshConnection(dev *Device) {
	rd.closeConnection(dev)
	rd.openConnection(dev)
}

// setRequestingDeviceID specifies what device ID the requests to the remote DB
// servers should identify themselves as.
func (rd *RemoteDB) setRequestingDeviceID(deviceID DeviceID) {
	rd.deviceID = deviceID
}

// activate begins actively listening for devices on the network hat support
// remote database queries to be added to the PRO DJ LINK network. This
// maintains adding and removing of device connections.
func (rd *RemoteDB) activate(dm *DeviceManager) {
	// Connect to already active devices on the network
	for _, dev := range dm.ActiveDeviceMap() {
		rd.openConnection(dev)
	}

	key := fmt.Sprintf("remotedb_%d", rd.deviceID)

	dm.OnDeviceAdded(key, DeviceListenerFunc(rd.openConnection))
	dm.OnDeviceRemoved(key, DeviceListenerFunc(rd.closeConnection))
}

// deactivate closes any open remote DB connections and stops waiting to
// connect to new devices that appear on the network.
func (rd *RemoteDB) deactivate(dm *DeviceManager) {
	key := fmt.Sprintf("remotedb_%d", rd.deviceID)

	dm.RemoveListener(key, DeviceListenerFunc(rd.openConnection))
	dm.RemoveListener(key, DeviceListenerFunc(rd.closeConnection))

	for _, conn := range rd.conns {
		rd.closeConnection(conn.device)
	}
}

func newRemoteDB() *RemoteDB {
	return &RemoteDB{
		conns:     map[DeviceID]*deviceConnection{},
		connsLock: &sync.Mutex{},
	}
}
