package radius

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// Standard RADIUS Codes (RFC 2865, 2866, 3576, 5176)
const (
	CodeAccessRequest      = 1
	CodeAccessAccept       = 2
	CodeAccessReject       = 3
	CodeAccountingRequest  = 4
	CodeAccountingResponse = 5
	CodeAccessChallenge    = 11
	CodeDisconnectRequest  = 40
	CodeDisconnectACK      = 41
	CodeDisconnectNAK      = 42
	CodeCoARequest         = 43
	CodeCoAACK             = 44
	CodeCoANAK             = 45
)

// Standard RADIUS Attribute Types
const (
	AttrUserName             = 1
	AttrUserPassword         = 2
	AttrCHAPPassword         = 3
	AttrNASIPAddress         = 4
	AttrNASPort              = 5
	AttrServiceType          = 6
	AttrFramedProtocol       = 7
	AttrFramedIPAddress      = 8
	AttrFramedIPNetmask      = 9
	AttrFramedRouting        = 10
	AttrFilterID             = 11
	AttrFramedMTU            = 12
	AttrFramedCompression    = 13
	AttrLoginIPHost          = 14
	AttrLoginService         = 15
	AttrLoginTCPPort         = 16
	AttrReplyMessage         = 18
	AttrCallbackNumber       = 19
	AttrCallbackID           = 20
	AttrFramedRoute          = 22
	AttrFramedIPXNetwork     = 23
	AttrState                = 24
	AttrClass                = 25
	AttrVendorSpecific       = 26
	AttrSessionTimeout       = 27
	AttrIdleTimeout          = 28
	AttrTerminationAction    = 29
	AttrCalledStationID      = 30
	AttrCallingStationID     = 31
	AttrNASIdentifier        = 32
	AttrProxyState           = 33
	AttrLoginLATService      = 34
	AttrLoginLATNode         = 35
	AttrLoginLATGroup        = 36
	AttrFramedAppleTalkLink  = 37
	AttrFramedAppleTalkNet   = 38
	AttrFramedAppleTalkZone  = 39
	AttrAcctStatusType       = 40
	AttrAcctDelayTime        = 41
	AttrAcctInputOctets      = 42
	AttrAcctOutputOctets     = 43
	AttrAcctSessionID        = 44
	AttrAcctAuthentic        = 45
	AttrAcctSessionTime      = 46
	AttrAcctInputPackets     = 47
	AttrAcctOutputPackets    = 48
	AttrAcctTerminateCause   = 49
	AttrAcctMultiSessionID   = 50
	AttrAcctLinkCount        = 51
	AttrAcctInputGigawords   = 52
	AttrAcctOutputGigawords  = 53
	AttrCHAPChallenge        = 60
	AttrNASPortType          = 61
	AttrPortLimit            = 62
	AttrLoginLATPort         = 63
	AttrAcctInterimInterval  = 85
	AttrMessageAuthenticator = 80
	AttrFramedPool           = 88
)

// Accounting Status Types (RFC 2866)
const (
	AcctStatusStart         = 1
	AcctStatusStop          = 2
	AcctStatusInterimUpdate = 3
	AcctStatusAccountingOn  = 7
	AcctStatusAccountingOff = 8
)

// MikroTik Vendor ID and Sub-Attribute Types
const (
	MikrotikVendorID = 14988

	MikrotikRecvLimit          = 1
	MikrotikXmitLimit          = 2
	MikrotikGroup              = 3
	MikrotikWirelessForward    = 4
	MikrotikWirelessSkipDot1x  = 5
	MikrotikWirelessEncAlgo    = 6
	MikrotikWirelessEncKey     = 7
	MikrotikRateLimit          = 8
	MikrotikRealm              = 9
	MikrotikHostIP             = 10
	MikrotikMarkID             = 11
	MikrotikAdvertiseURL       = 12
	MikrotikAdvertiseInterval  = 13
	MikrotikRecvLimitGigawords = 14
	MikrotikXmitLimitGigawords = 15
	MikrotikWirelessPSK        = 16
	MikrotikTotalLimit         = 17
	MikrotikTotalLimitGigaword = 18
	MikrotikAddressList        = 19
	MikrotikWirelessMPKey      = 20
	MikrotikWirelessComment    = 21
	MikrotikDelegatedIPv6Pool  = 22
)

// Attribute represents a single RADIUS attribute
type Attribute struct {
	Type  uint8
	Value []byte
}

// Packet represents a RADIUS protocol packet
type Packet struct {
	Code          uint8
	Identifier    uint8
	Length        uint16
	Authenticator [16]byte
	Attributes    []Attribute
	Secret        []byte
}

// NewPacket creates an empty packet
func NewPacket(code, identifier uint8, secret []byte) *Packet {
	return &Packet{
		Code:       code,
		Identifier: identifier,
		Secret:     secret,
		Attributes: make([]Attribute, 0),
	}
}

// AddAttribute adds an attribute to packet
func (p *Packet) AddAttribute(attrType uint8, value []byte) {
	p.Attributes = append(p.Attributes, Attribute{
		Type:  attrType,
		Value: value,
	})
}

// AddString adds a string attribute
func (p *Packet) AddString(attrType uint8, value string) {
	p.AddAttribute(attrType, []byte(value))
}

// AddUint32 adds a 32-bit integer attribute
func (p *Packet) AddUint32(attrType uint8, value uint32) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, value)
	p.AddAttribute(attrType, b)
}

// AddIP adds an IPv4 address attribute
func (p *Packet) AddIP(attrType uint8, ip net.IP) {
	ip4 := ip.To4()
	if ip4 != nil {
		p.AddAttribute(attrType, ip4)
	}
}

// AddMikrotikVSA adds a MikroTik Vendor-Specific Attribute
func (p *Packet) AddMikrotikVSA(subType uint8, val []byte) {
	// Format:
	// VendorID (4 bytes) + SubType (1 byte) + SubLength (1 byte: val_len + 2) + val
	subLen := uint8(len(val) + 2)
	data := make([]byte, 4+2+len(val))
	binary.BigEndian.PutUint32(data[0:4], MikrotikVendorID)
	data[4] = subType
	data[5] = subLen
	copy(data[6:], val)

	p.AddAttribute(AttrVendorSpecific, data)
}

// AddMikrotikRateLimit adds Mikrotik-Rate-Limit string (e.g., "1M/2M" or "2048k/4096k")
func (p *Packet) AddMikrotikRateLimit(rateLimit string) {
	if rateLimit != "" {
		p.AddMikrotikVSA(MikrotikRateLimit, []byte(rateLimit))
	}
}

// AddMikrotikAddressList adds Mikrotik-Address-List
func (p *Packet) AddMikrotikAddressList(listName string) {
	if listName != "" {
		p.AddMikrotikVSA(MikrotikAddressList, []byte(listName))
	}
}

// GetAttribute returns the first attribute matching the given type
func (p *Packet) GetAttribute(attrType uint8) []byte {
	for _, attr := range p.Attributes {
		if attr.Type == attrType {
			return attr.Value
		}
	}
	return nil
}

// GetString returns the value of an attribute as string
func (p *Packet) GetString(attrType uint8) string {
	b := p.GetAttribute(attrType)
	if b == nil {
		return ""
	}
	return string(b)
}

// GetUint32 returns the value of an attribute as uint32
func (p *Packet) GetUint32(attrType uint8) uint32 {
	b := p.GetAttribute(attrType)
	if len(b) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

// GetIP returns the value of an attribute as net.IP
func (p *Packet) GetIP(attrType uint8) net.IP {
	b := p.GetAttribute(attrType)
	if len(b) == 4 {
		return net.IPv4(b[0], b[1], b[2], b[3])
	}
	return nil
}

// GetMikrotikVSA extracts a MikroTik VSA by subType
func (p *Packet) GetMikrotikVSA(subType uint8) []byte {
	for _, attr := range p.Attributes {
		if attr.Type == AttrVendorSpecific && len(attr.Value) >= 6 {
			vendorID := binary.BigEndian.Uint32(attr.Value[0:4])
			if vendorID == MikrotikVendorID {
				// Could contain multiple TLVs inside one VSA
				offset := 4
				for offset+2 <= len(attr.Value) {
					st := attr.Value[offset]
					sl := int(attr.Value[offset+1])
					if sl < 2 || offset+sl > len(attr.Value) {
						break
					}
					if st == subType {
						return attr.Value[offset+2 : offset+sl]
					}
					offset += sl
				}
			}
		}
	}
	return nil
}

// ParsePacket parses raw UDP bytes into a Packet
func ParsePacket(data []byte, secret []byte) (*Packet, error) {
	if len(data) < 20 {
		return nil, errors.New("packet too short (< 20 bytes)")
	}

	length := binary.BigEndian.Uint16(data[2:4])
	if int(length) > len(data) || length < 20 {
		return nil, fmt.Errorf("invalid packet length: header %d, actual %d", length, len(data))
	}

	pkt := &Packet{
		Code:       data[0],
		Identifier: data[1],
		Length:     length,
		Secret:     secret,
	}
	copy(pkt.Authenticator[:], data[4:20])

	offset := 20
	end := int(length)
	for offset < end {
		if offset+2 > end {
			break
		}
		attrType := data[offset]
		attrLen := int(data[offset+1])
		if attrLen < 2 || offset+attrLen > end {
			return nil, errors.New("malformed attribute length")
		}
		val := make([]byte, attrLen-2)
		copy(val, data[offset+2:offset+attrLen])
		pkt.Attributes = append(pkt.Attributes, Attribute{
			Type:  attrType,
			Value: val,
		})
		offset += attrLen
	}

	return pkt, nil
}

// Encode serializes the packet to bytes and generates the proper Authenticator
func (p *Packet) Encode(reqAuth *[16]byte) ([]byte, error) {
	// First calculate total length
	totalLen := 20
	for _, attr := range p.Attributes {
		totalLen += 2 + len(attr.Value)
	}

	if totalLen > 4096 {
		return nil, fmt.Errorf("packet length exceeds max 4096: %d", totalLen)
	}

	buf := make([]byte, totalLen)
	buf[0] = p.Code
	buf[1] = p.Identifier
	binary.BigEndian.PutUint16(buf[2:4], uint16(totalLen))

	// Copy attributes
	offset := 20
	for _, attr := range p.Attributes {
		buf[offset] = attr.Type
		buf[offset+1] = uint8(2 + len(attr.Value))
		copy(buf[offset+2:], attr.Value)
		offset += 2 + len(attr.Value)
	}

	// Calculate authenticator depending on code
	switch p.Code {
	case CodeAccessAccept, CodeAccessReject, CodeAccessChallenge:
		// Response Authenticator = MD5(Code + ID + Length + RequestAuth + Attributes + Secret)
		if reqAuth == nil {
			return nil, errors.New("request authenticator required for response")
		}
		h := md5.New()
		h.Write(buf[0:4])
		h.Write(reqAuth[:])
		h.Write(buf[20:totalLen])
		h.Write(p.Secret)
		auth := h.Sum(nil)
		copy(buf[4:20], auth)

	case CodeAccountingResponse, CodeDisconnectACK, CodeDisconnectNAK, CodeCoAACK, CodeCoANAK:
		// Response Authenticator = MD5(Code + ID + Length + RequestAuth + Attributes + Secret)
		if reqAuth == nil {
			return nil, errors.New("request authenticator required for response")
		}
		h := md5.New()
		h.Write(buf[0:4])
		h.Write(reqAuth[:])
		h.Write(buf[20:totalLen])
		h.Write(p.Secret)
		auth := h.Sum(nil)
		copy(buf[4:20], auth)

	case CodeAccountingRequest:
		// Request Authenticator = MD5(Code + ID + Length + 16-zero-bytes + Attributes + Secret)
		zeros := [16]byte{}
		h := md5.New()
		h.Write(buf[0:4])
		h.Write(zeros[:])
		h.Write(buf[20:totalLen])
		h.Write(p.Secret)
		auth := h.Sum(nil)
		copy(buf[4:20], auth)

	case CodeDisconnectRequest, CodeCoARequest:
		// RFC 5176 Section 2.1 / RFC 3576:
		// The Request Authenticator in Disconnect-Request and CoA-Request packets
		// is a 16-octet random number generated by the sender.
		isZero := true
		for _, b := range p.Authenticator {
			if b != 0 {
				isZero = false
				break
			}
		}
		if isZero {
			var rnd [16]byte
			// Fill with pseudo-random or cryptographically secure random bytes
			h := md5.New()
			h.Write([]byte(fmt.Sprintf("%d-%d-%d", p.Code, p.Identifier, totalLen)))
			h.Write(p.Secret)
			copy(rnd[:], h.Sum(nil))
			p.Authenticator = rnd
		}
		copy(buf[4:20], p.Authenticator[:])

	default:
		copy(buf[4:20], p.Authenticator[:])
	}

	// RFC 2869 / RFC 5176: Calculate Message-Authenticator (Type 80) HMAC-MD5 if present
	msgAuthIdx := -1
	for off := 20; off < totalLen; {
		if off+2 > totalLen {
			break
		}
		aType := buf[off]
		aLen := int(buf[off+1])
		if aLen < 2 || off+aLen > totalLen {
			break
		}
		if aType == AttrMessageAuthenticator && aLen == 18 {
			msgAuthIdx = off + 2
			break
		}
		off += aLen
	}
	if msgAuthIdx != -1 {
		// Zero the 16 bytes first for HMAC calculation
		for i := 0; i < 16; i++ {
			buf[msgAuthIdx+i] = 0
		}
		mac := hmac.New(md5.New, p.Secret)
		mac.Write(buf)
		copy(buf[msgAuthIdx:msgAuthIdx+16], mac.Sum(nil))
	}

	return buf, nil
}

// AddMessageAuthenticator inserts a blank 16-byte Message-Authenticator attribute (Type 80)
// which will be automatically calculated during Encode.
func (p *Packet) AddMessageAuthenticator() {
	p.AddAttribute(AttrMessageAuthenticator, make([]byte, 16))
}

// VerifyResponseAuthenticator validates that the response authenticator matches RFC 2865 / RFC 5176:
// MD5(Code + ID + Length + RequestAuth + Attributes + Secret)
func VerifyResponseAuthenticator(responseBuf []byte, requestAuth [16]byte, secret []byte) bool {
	if len(responseBuf) < 20 {
		return false
	}
	respLen := int(binary.BigEndian.Uint16(responseBuf[2:4]))
	if respLen < 20 || respLen > len(responseBuf) {
		return false
	}
	h := md5.New()
	h.Write(responseBuf[0:4])
	h.Write(requestAuth[:])
	h.Write(responseBuf[20:respLen])
	h.Write(secret)
	expected := h.Sum(nil)
	return hmac.Equal(expected, responseBuf[4:20])
}

// VerifyAccountingRequest verifies accounting request authenticator
func (p *Packet) VerifyAccountingRequest(secret []byte) bool {
	raw := p.rawBytesWithZeroAuth()
	h := md5.New()
	h.Write(raw)
	h.Write(secret)
	expected := h.Sum(nil)
	return bytes.Equal(expected, p.Authenticator[:])
}

// VerifyMessageAuthenticator checks RFC 2869 Message-Authenticator HMAC-MD5
func (p *Packet) VerifyMessageAuthenticator(rawPacket []byte, secret []byte) bool {
	msgAuthIdx := -1
	offset := 20
	for offset < len(rawPacket) {
		aType := rawPacket[offset]
		aLen := int(rawPacket[offset+1])
		if aLen < 2 || offset+aLen > len(rawPacket) {
			break
		}
		if aType == AttrMessageAuthenticator && aLen == 18 {
			msgAuthIdx = offset + 2
			break
		}
		offset += aLen
	}

	if msgAuthIdx == -1 {
		// Not present
		return true
	}

	receivedAuth := make([]byte, 16)
	copy(receivedAuth, rawPacket[msgAuthIdx:msgAuthIdx+16])

	// Zero out Message-Authenticator field
	calcPacket := make([]byte, len(rawPacket))
	copy(calcPacket, rawPacket)
	for i := 0; i < 16; i++ {
		calcPacket[msgAuthIdx+i] = 0
	}

	mac := hmac.New(md5.New, secret)
	mac.Write(calcPacket)
	expectedAuth := mac.Sum(nil)

	return hmac.Equal(receivedAuth, expectedAuth)
}

func (p *Packet) rawBytesWithZeroAuth() []byte {
	totalLen := 20
	for _, attr := range p.Attributes {
		totalLen += 2 + len(attr.Value)
	}
	buf := make([]byte, totalLen)
	buf[0] = p.Code
	buf[1] = p.Identifier
	binary.BigEndian.PutUint16(buf[2:4], uint16(totalLen))
	// 4..20 are zeroes
	offset := 20
	for _, attr := range p.Attributes {
		buf[offset] = attr.Type
		buf[offset+1] = uint8(2 + len(attr.Value))
		copy(buf[offset+2:], attr.Value)
		offset += 2 + len(attr.Value)
	}
	return buf
}
