package prolink

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"unicode/utf16"
)

var be = binary.BigEndian

// Implements structs needed to create and parse packets passed over the TCP
// portion of the Pioneer PRO DJ LINK network.
//
// James Elliot has extensively documented the protocol in his analysis
// document [1]
//
// [1]: https://github.com/brunchboy/dysentery/blob/master/doc/Analysis.pdf

// message type identifiers. Each message has a unique identifier, some
// identifiers are used for requests, others represent response messages.
const (
	// request messages
	msgTypeIntroduce     uint16 = 0x0000
	msgTypeGetMetadata   uint16 = 0x2002
	msgTypeGetArtwork    uint16 = 0x2003
	msgTypeGetTrackInfo  uint16 = 0x2102
	msgTypeGetCDMetadata uint16 = 0x2202

	// render menu requests
	msgTypeRenderRequest uint16 = 0x3000
	msgTypeResponse      uint16 = 0x4000

	// response message types
	msgTypeArtwork    uint16 = 0x4002
	msgTypeMenuItem   uint16 = 0x4101
	msgTypeMenuHeader uint16 = 0x4001
	msgTypeMenuFooter uint16 = 0x4201
)

// Render targets aren't fully understood, but they seem to relate a bit to
// what data is returned, for example, requesting metadat to the main menu will
// *not* return the track path, while rendering to the 'system' target includes
// a file path.
const (
	renderMainMenu  = 0x01
	renderTrackInfo = 0x03
	renderSystem    = 0x08
)

// When receiving a msgTypeMenuItem a item type field is included, this list
// contains the various item types.
const (
	itemTypePath      = 0x00
	itemTypeAlbum     = 0x02
	itemTypeDisc      = 0x03
	itemTypeTitle     = 0x04
	itemTypeGenre     = 0x06
	itemTypeArtist    = 0x07
	itemTypeRating    = 0x0a
	itemTypeDuration  = 0x0b
	itemTypeLabel     = 0x0e
	itemTypeKey       = 0x0f
	itemTypeColor     = 0x13
	itemTypeComment   = 0x23
	itemTypeDateAdded = 0x2e

	// item colors
	itemTypeColorNone   = 0x13
	itemTypeColorPink   = 0x14
	itemTypeColorRed    = 0x15
	itemTypeColorOrange = 0x16
	itemTypeColorYellow = 0x17
	itemTypeColorGreen  = 0x18
	itemTypeColorAqua   = 0x19
	itemTypeColorBlue   = 0x1a
	itemTypeColorPurple = 0x1b
)

// date layout for the date added field
const dateAddedLayout = "2006-01-02"

// fieldTypes are the single byte prefixes for each field, specifying the type
// of data that will follow.
const (
	fieldTypeNumber01 = 0x0f
	fieldTypeNumber02 = 0x10
	fieldTypeNumber04 = 0x11
	fieldTypeBinary   = 0x14
	fieldTypeString   = 0x26
)

// argTypes are used in the argument list field. This essentially duplicates
// the data that the field types provide. It is unknown the reason for this
// data duplication.
const (
	argTypeString   = 0x02
	argTypeBinary   = 0x03
	argTypeNumber04 = 0x06
)

// field represents a single field within a remoteDB packet
type field interface {
	bytes() []byte
	argType() byte
}

// fieldNumber01 represents a unit8 number field
type fieldNumber01 uint8

func (v fieldNumber01) bytes() []byte {
	return []byte{fieldTypeNumber01, byte(v)}
}

func (v fieldNumber01) argType() byte {
	return 0x00 // field does not appear in arguments
}

// fieldNumber02 represents a uint16 number field
type fieldNumber02 uint16

func (v fieldNumber02) bytes() []byte {
	data := make([]byte, 2)
	be.PutUint16(data, uint16(v))

	return append([]byte{fieldTypeNumber02}, data...)
}

func (v fieldNumber02) argType() byte {
	return 0x00 // field does not appear in arguments
}

// fieldNumber04 represents a uint32 number field
type fieldNumber04 uint32

func (v fieldNumber04) bytes() []byte {
	data := make([]byte, 4)
	be.PutUint32(data, uint32(v))

	return append([]byte{fieldTypeNumber04}, data...)
}

func (v fieldNumber04) argType() byte {
	return argTypeNumber04
}

// fieldBinary represents a binary field
type fieldBinary []byte

func (v fieldBinary) bytes() []byte {
	dataLen := make([]byte, 4)
	be.PutUint32(dataLen, uint32(len(v)))

	return append([]byte{fieldTypeBinary}, append(dataLen, v...)...)
}

func (v fieldBinary) argType() byte {
	return argTypeBinary
}

// fieldString represents a utf-16 big endian string
type fieldString string

func (v fieldString) bytes() []byte {
	// Includes the null terminating byte
	str := append(utf16.Encode([]rune(string(v))), 0)

	strData := make([]byte, 0, len(str)*2)
	for _, strUint16 := range str {
		runeBytes := make([]byte, 2)
		be.PutUint16(runeBytes, strUint16)
		strData = append(strData, runeBytes...)
	}

	strLenData := make([]byte, 4)
	be.PutUint32(strLenData, uint32(len(str)+1))

	return append([]byte{fieldTypeString}, append(strLenData, strData...)...)
}

func (v fieldString) argType() byte {
	return argTypeString
}

// pioneerMagic is the magic number that almost every packet sent over the
// Pioneer PRO DJ LINK network is introduced with.
const pioneerMagic uint32 = 0x872349ae

// messagePacket is an interface describing any message packets that pass over
// the  PRO DJ LINK network. Packets may be constructed and parsed.
type messagePacket interface {
	// bytes returns the []byte representation of the message packet.
	bytes() []byte

	// setTransactionID sets the transaction ID number of the packet.
	setTransactionID(uint32)
}

// transactionPacket is intended to be composed into other packet types, to
// make fulfilling the messagePacket interface simpler.
type transactionPacket struct {
	transaction uint32
}

func (p *transactionPacket) setTransactionID(txID uint32) {
	p.transaction = txID
}

// genericPacket represents a standard message passed over the network. It
// generically has a list of fields, a message type, and is made up of a
// transactionPacket.
type genericPacket struct {
	transactionPacket
	messageType uint16
	arguments   []field
}

func (p *genericPacket) bytes() []byte {
	argCount := uint8(len(p.arguments))

	// Construct the arg type list field (sometimes known as a tags field).
	// Argument lists are always 12 bytes long with trailing 0x00 padding. At
	// least, from what we've seen.
	argTypes := make([]byte, 12)

	for i, field := range p.arguments {
		argTypes[i] = field.argType()
	}

	baseArgs := []field{
		fieldNumber04(pioneerMagic),
		fieldNumber04(p.transaction),
		fieldNumber02(p.messageType),
		fieldNumber01(argCount),
		fieldBinary(argTypes),
	}

	bytesData := []byte{}
	builtArgs := append(baseArgs, p.arguments...)

	for _, arg := range builtArgs {
		bytesData = append(bytesData, arg.bytes()...)
	}

	return bytesData
}

func (p *genericPacket) String() string {
	return hex.Dump(p.bytes())
}

// introducePacket is the message that must be sent to the remote DB before any
// queries may be made against the database.
type introducePacket struct {
	deviceID DeviceID
}

// setTransactionID for the introducePacket is a no-op, as the transaction
// number for the intro packet contains a magic number.
func (p *introducePacket) setTransactionID(txID uint32) {
	return
}

func (p *introducePacket) bytes() []byte {
	devID := []byte{0x0, 0x0, 0x0, byte(p.deviceID)}

	args := []field{
		fieldNumber04(be.Uint32(devID)),
	}

	identifyPacket := &genericPacket{
		messageType: msgTypeIntroduce,
		arguments:   args,
	}

	// Magic transaction identifier for announce packets. Perhaps some type of
	// transaction reset mask?
	identifyPacket.transaction = 0xfffffffe

	return identifyPacket.bytes()
}

func (p *introducePacket) String() string {
	return hex.Dump(p.bytes())
}

// metadataRequestPacket is the message that must be sent to request for track
// metadata.
type metadataRequestPacket struct {
	transactionPacket
	deviceID DeviceID
	slot     TrackSlot
	trackID  uint32
}

func (p *metadataRequestPacket) bytes() []byte {
	messageType := msgTypeGetMetadata

	// CD Metadata requests have their own message type
	if p.slot == TrackSlotCD {
		messageType = msgTypeGetCDMetadata
	}

	args := []field{
		makeRequestField(p.deviceID, p.slot, renderMainMenu),
		fieldNumber04(p.trackID),
	}

	request := &genericPacket{
		messageType: messageType,
		arguments:   args,
	}

	request.transaction = p.transaction

	return request.bytes()
}

func (p *metadataRequestPacket) String() string {
	return hex.Dump(p.bytes())
}

// trackInfoRequestPacket is the message that must be sent to request track
// information. This is different from a metadata request in that it requests
// 'system info' such as the path.
type trackInfoRequestPacket struct {
	transactionPacket
	deviceID DeviceID
	slot     TrackSlot
	trackID  uint32
}

func (p *trackInfoRequestPacket) bytes() []byte {
	args := []field{
		makeRequestField(p.deviceID, p.slot, renderSystem),
		fieldNumber04(p.trackID),
	}

	request := &genericPacket{
		messageType: msgTypeGetTrackInfo,
		arguments:   args,
	}

	request.transaction = p.transaction

	return request.bytes()
}

func (p *trackInfoRequestPacket) String() string {
	return hex.Dump(p.bytes())
}

// renderRequestPacket is the message that must be sent to request that data is
// rendered back from the remote DB to the client.
type renderRequestPacket struct {
	transactionPacket
	deviceID   DeviceID
	slot       TrackSlot
	offset     uint32
	limit      uint32
	renderType byte
}

func (p *renderRequestPacket) bytes() []byte {
	renderType := p.renderType

	// Default to rendering to the main menu
	if renderType == 0x0 {
		renderType = renderMainMenu
	}

	args := []field{
		makeRequestField(p.deviceID, p.slot, renderType),
		fieldNumber04(p.offset),
		fieldNumber04(p.limit),
		fieldNumber04(0),       // (?) Unknown what this field is for
		fieldNumber04(p.limit), // (?) Unknown why the limit is sent twice.
		fieldNumber04(0),       // (?) Unknown what this field is for
	}

	request := &genericPacket{
		messageType: msgTypeRenderRequest,
		arguments:   args,
	}

	request.transaction = p.transaction

	return request.bytes()
}

func (p *renderRequestPacket) String() string {
	return hex.Dump(p.bytes())
}

// requestArtwork is the message that must be sent to request artwork binary
// data.
type requestArtwork struct {
	transactionPacket
	deviceID  DeviceID
	slot      TrackSlot
	artworkID uint32
}

func (p *requestArtwork) bytes() []byte {
	args := []field{
		makeRequestField(p.deviceID, p.slot, renderSystem),
		fieldNumber04(p.artworkID),
	}

	request := &genericPacket{
		messageType: msgTypeGetArtwork,
		arguments:   args,
	}

	request.transaction = p.transaction

	return request.bytes()
}

func (p *requestArtwork) String() string {
	return hex.Dump(p.bytes())
}

// menuItem is a higher level convinience struct that is created from a generic
// packet for a menu item type
type menuItem struct {
	num       uint32
	text1     string
	text2     string
	itemType  byte
	artworkID uint32
}

// makeMenuItem constructs a menuItem from a genericPacket, pulling out
// arguments as their correct struct fields.
func makeMenuItem(p *genericPacket) *menuItem {
	// Single byte fields (fieldNumber01) don't appear to be supported in
	// arguments list, so even though the menu item type is a single byte we
	// still have to extract it as byte
	typeBytes := make([]byte, 4)
	be.PutUint32(typeBytes, uint32(p.arguments[6].(fieldNumber04)))

	return &menuItem{
		num:       uint32(p.arguments[1].(fieldNumber04)),
		text1:     string(p.arguments[3].(fieldString)),
		text2:     string(p.arguments[5].(fieldString)),
		artworkID: uint32(p.arguments[8].(fieldNumber04)),
		itemType:  typeBytes[3:][0],
	}
}

// makeRequestField constructs an fieldNumber4 with the device ID, slot, and
// render target field. This is used in various messages.
func makeRequestField(devID DeviceID, slot TrackSlot, renderTo byte) field {
	value := []byte{
		byte(devID), renderTo, byte(slot),

		// This last byte still has unknown meaning here. In the libpdjl
		// project it's described as 'source analyzed' but it's unclear.
		0x01,
	}

	// Although this is more of a packet byte field, it's represented as a uint32
	return fieldNumber04(be.Uint32(value))
}

func readMessagePacket(conn io.Reader) (*genericPacket, error) {
	preamble, err := readField(conn)
	if err != nil {
		return nil, err
	}

	// Ensure preamble matches the magic byte, otherwise this is not a Pioneer
	// PRO LINK message packet.
	if d, ok := preamble.(fieldNumber04); !ok || uint32(d) != pioneerMagic {
		return nil, fmt.Errorf("Invalid packet, does not contain magic preamble")
	}

	// Read the next four standard message fields

	txIDField, err := readField(conn)
	if err != nil {
		return nil, err
	}

	msgTypeField, err := readField(conn)
	if err != nil {
		return nil, err
	}

	argsCountField, err := readField(conn)
	if err != nil {
		return nil, err
	}

	// We're not going to do anything with the tags field, as noted in the
	// genericPacket, the tags fields is redundant information afaict.
	_, err = readField(conn)
	if err != nil {
		return nil, err
	}

	// XXX: This is an absolute hack, but for whatever reason when requesting
	// artwork it will specify that it has 4 arguments, but if there is no
	// artwork *will only send 3*. in which case we cannot try and read the 4th
	// argument. Pioneer WHY??
	artworkHack := uint16(msgTypeField.(fieldNumber02)) == msgTypeArtwork

	argsCount := int(argsCountField.(fieldNumber01))
	argFields := make([]field, argsCount)

	for i := 0; i < argsCount; i++ {
		argField, err := readField(conn)
		if err != nil {
			return nil, err
		}

		argFields[i] = argField

		// XXX: See note above. WHY PIONEER??
		if artworkHack && i == 2 && int32(argField.(fieldNumber04)) == 0 {
			argFields[3] = fieldBinary{}
			break
		}
	}

	packet := &genericPacket{
		messageType: uint16(msgTypeField.(fieldNumber02)),
		arguments:   argFields,
	}

	packet.transaction = uint32(txIDField.(fieldNumber04))

	return packet, nil
}

// readField reads a single field type, returning the parsed field object that
// implements the field interface. Supports all defined fields.
func readField(conn io.Reader) (field, error) {
	fieldType := make([]byte, 1)
	if _, err := conn.Read(fieldType); err != nil {
		return nil, err
	}

	switch fieldType[0] {
	case fieldTypeNumber01:
		fieldByte := make([]byte, 1)
		if _, err := conn.Read(fieldByte); err != nil {
			return nil, err
		}

		return fieldNumber01(fieldByte[0]), nil
	case fieldTypeNumber02:
		fieldBytes := make([]byte, 2)
		if _, err := conn.Read(fieldBytes); err != nil {
			return nil, err
		}

		return fieldNumber02(be.Uint16(fieldBytes)), nil
	case fieldTypeNumber04:
		fieldBytes := make([]byte, 4)
		if _, err := conn.Read(fieldBytes); err != nil {
			return nil, err
		}

		return fieldNumber04(be.Uint32(fieldBytes)), nil
	case fieldTypeString:
		fieldLenBytes := make([]byte, 4)
		if _, err := conn.Read(fieldLenBytes); err != nil {
			return nil, err
		}

		stringLen := be.Uint32(fieldLenBytes)

		s := make([]byte, stringLen*2)
		if _, err := conn.Read(s); err != nil {
			return nil, err
		}

		str16Bit := make([]uint16, 0, stringLen)
		for ; len(s) > 0; s = s[2:] {
			str16Bit = append(str16Bit, be.Uint16(s[:2]))
		}

		// Remove the trailing NULL character
		return fieldString(utf16.Decode(str16Bit)[:stringLen-1]), nil
	case fieldTypeBinary:
		fieldLenBytes := make([]byte, 4)
		if _, err := conn.Read(fieldLenBytes); err != nil {
			return nil, err
		}

		dataSize := be.Uint32(fieldLenBytes)

		data := make([]byte, dataSize)
		io.ReadFull(conn, data)

		return fieldBinary(data), nil
	}

	return nil, fmt.Errorf("Invalid field Type: %x", fieldType[0])
}
