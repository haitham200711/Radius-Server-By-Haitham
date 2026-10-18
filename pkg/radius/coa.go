package radius

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"time"
)

// CoAResult contains response details from Disconnect/CoA request
type CoAResult struct {
	Success bool
	Code    uint8
	Message string
}

// SendDisconnectRequest sends RFC 3576 Disconnect-Request (PoD) to NAS (MikroTik)
func SendDisconnectRequest(nasIP string, nasPort int, secret []byte, username, sessionID string, framedIP net.IP) (*CoAResult, error) {
	if nasPort <= 0 {
		nasPort = 3799 // Default CoA / PoD port for MikroTik
	}

	addr := fmt.Sprintf("%s:%d", nasIP, nasPort)
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to dial NAS %s: %w", addr, err)
	}
	defer conn.Close()

	// Random identifier
	var idByte [1]byte
	_, _ = rand.Read(idByte[:])

	pkt := NewPacket(CodeDisconnectRequest, idByte[0], secret)
	if username != "" {
		pkt.AddString(AttrUserName, username)
	}
	if sessionID != "" {
		pkt.AddString(AttrAcctSessionID, sessionID)
	}
	if framedIP != nil {
		pkt.AddIP(AttrFramedIPAddress, framedIP)
	}

	data, err := pkt.Encode(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encode disconnect packet: %w", err)
	}

	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("failed to send disconnect packet: %w", err)
	}

	replyBuf := make([]byte, 4096)
	n, err := conn.Read(replyBuf)
	if err != nil {
		return nil, fmt.Errorf("timeout waiting for NAS reply: %w", err)
	}

	replyPkt, err := ParsePacket(replyBuf[:n], secret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reply packet: %w", err)
	}

	switch replyPkt.Code {
	case CodeDisconnectACK:
		return &CoAResult{
			Success: true,
			Code:    CodeDisconnectACK,
			Message: "Session disconnected successfully (Disconnect-ACK)",
		}, nil
	case CodeDisconnectNAK:
		errMsg := replyPkt.GetString(AttrReplyMessage)
		if errMsg == "" {
			errMsg = "Session not found or already disconnected (Disconnect-NAK)"
		}
		return &CoAResult{
			Success: false,
			Code:    CodeDisconnectNAK,
			Message: errMsg,
		}, nil
	default:
		return &CoAResult{
			Success: false,
			Code:    replyPkt.Code,
			Message: fmt.Sprintf("Unexpected response code %d", replyPkt.Code),
		}, nil
	}
}

// SendCoARateLimit sends CoA-Request to change user bandwidth on the fly without disconnect
func SendCoARateLimit(nasIP string, nasPort int, secret []byte, username, sessionID, newRateLimit string) (*CoAResult, error) {
	if nasPort <= 0 {
		nasPort = 3799
	}

	addr := fmt.Sprintf("%s:%d", nasIP, nasPort)
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to dial NAS %s: %w", addr, err)
	}
	defer conn.Close()

	var idByte [1]byte
	_, _ = rand.Read(idByte[:])

	pkt := NewPacket(CodeCoARequest, idByte[0], secret)
	if username != "" {
		pkt.AddString(AttrUserName, username)
	}
	if sessionID != "" {
		pkt.AddString(AttrAcctSessionID, sessionID)
	}
	if newRateLimit != "" {
		pkt.AddMikrotikRateLimit(newRateLimit)
	}

	data, err := pkt.Encode(nil)
	if err != nil {
		return nil, errors.New("failed to encode CoA packet")
	}

	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}

	replyBuf := make([]byte, 4096)
	n, err := conn.Read(replyBuf)
	if err != nil {
		return nil, errors.New("timeout waiting for CoA reply")
	}

	replyPkt, err := ParsePacket(replyBuf[:n], secret)
	if err != nil {
		return nil, err
	}

	if replyPkt.Code == CodeCoAACK {
		return &CoAResult{
			Success: true,
			Code:    CodeCoAACK,
			Message: "Bandwidth updated on the fly (CoA-ACK)",
		}, nil
	}

	return &CoAResult{
		Success: false,
		Code:    replyPkt.Code,
		Message: "Failed to update bandwidth (CoA-NAK)",
	}, nil
}
