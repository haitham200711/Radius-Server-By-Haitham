package radius

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Standard Error-Cause codes (RFC 5176 Section 3.6)
const (
	AttrErrorCause = 101

	ErrorCauseResidualSessionContextRemoved = 201
	ErrorCauseInvalidEAP                    = 202
	ErrorCauseUnsupportedAttribute          = 401
	ErrorCauseMissingAttribute              = 402
	ErrorCauseValueNotAllowed               = 403
	ErrorCauseInvalidRequest                = 404
	ErrorCauseMultipleSessionMatch          = 405
	ErrorCauseUnsupportedExtension          = 406
	ErrorCauseAdministrativelyProhibited    = 501
	ErrorCauseRequestNotRoutable            = 502
	ErrorCauseSessionContextNotFound        = 503
	ErrorCauseSessionContextNotRemovable    = 504
	ErrorCauseOtherProxyProcessingError     = 505
	ErrorCauseResourcesUnavailable          = 506
	ErrorCauseRequestInitiated              = 507
)

// CoAResult contains response details from Disconnect/CoA request
type CoAResult struct {
	Success    bool   `json:"success"`
	Code       uint8  `json:"code"`
	ErrorCause uint32 `json:"error_cause,omitempty"`
	Message    string `json:"message"`
}

// CoAOptions holds parameters to dynamically modify an active session without disconnection
type CoAOptions struct {
	RateLimit   string
	AddressList string
	PoolName    string
	FramedIP    net.IP
}

// sendWithRetry handles transmission, RFC 5176 retransmissions, identifier matching,
// response authenticator verification, and Message-Authenticator checking.
func sendWithRetry(nasIP string, nasPort int, pkt *Packet, secret []byte, isCoA bool) (*CoAResult, error) {
	if nasPort <= 0 {
		nasPort = 3799 // Default CoA / PoD port for MikroTik
	}

	addr := net.JoinHostPort(nasIP, strconv.Itoa(nasPort))
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to dial NAS %s: %w", addr, err)
	}
	defer conn.Close()

	// Encode once to freeze Identifier and Request Authenticator across retransmissions
	rawReq, err := pkt.Encode(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encode radius packet: %w", err)
	}

	// Save request authenticator for response verification
	var reqAuth [16]byte
	copy(reqAuth[:], rawReq[4:20])

	// RFC 5176 retry policy: up to 3 attempts with exponential backoff
	backoffs := []time.Duration{1500 * time.Millisecond, 2000 * time.Millisecond, 2500 * time.Millisecond}
	var lastErr error

	replyBuf := make([]byte, 4096)

	for attempt, timeout := range backoffs {
		if attempt > 0 {
			// Backoff before retransmitting
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}

		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(rawReq); err != nil {
			lastErr = fmt.Errorf("failed to send packet on attempt %d: %w", attempt+1, err)
			continue
		}

		// Read response loop (to filter stale packets with mismatched identifiers)
		for {
			n, err := conn.Read(replyBuf)
			if err != nil {
				lastErr = err
				break // Deadline hit, try next retry attempt
			}

			if n < 20 {
				continue // Corrupted or truncated packet, ignore
			}

			// Validate Identifier
			respID := replyBuf[1]
			if respID != pkt.Identifier {
				continue // Mismatched identifier, possibly from another session, ignore
			}

			// Validate RFC 5176 Response Authenticator
			if !VerifyResponseAuthenticator(replyBuf[:n], reqAuth, secret) {
				return nil, errors.New("invalid response authenticator: possible secret mismatch or spoofing")
			}

			replyPkt, err := ParsePacket(replyBuf[:n], secret)
			if err != nil {
				return nil, fmt.Errorf("failed to parse reply packet: %w", err)
			}

			// Verify Message-Authenticator (Type 80) if present
			if !replyPkt.VerifyMessageAuthenticator(replyBuf[:n], secret) {
				return nil, errors.New("invalid Message-Authenticator in NAS response")
			}

			expectedACK := uint8(CodeDisconnectACK)
			expectedNAK := uint8(CodeDisconnectNAK)
			opName := "Disconnect"
			if isCoA {
				expectedACK = CodeCoAACK
				expectedNAK = CodeCoANAK
				opName = "CoA"
			}

			if replyPkt.Code == expectedACK {
				msg := replyPkt.GetString(AttrReplyMessage)
				if msg == "" {
					msg = fmt.Sprintf("%s-ACK accepted by NAS", opName)
				}
				return &CoAResult{
					Success: true,
					Code:    replyPkt.Code,
					Message: msg,
				}, nil
			}

			if replyPkt.Code == expectedNAK {
				msg := replyPkt.GetString(AttrReplyMessage)
				var errCause uint32
				if causeBytes := replyPkt.GetAttribute(AttrErrorCause); len(causeBytes) >= 4 {
					errCause = replyPkt.GetUint32(AttrErrorCause)
				}
				if msg == "" {
					if errCause > 0 {
						msg = fmt.Sprintf("%s-NAK rejected by NAS (Error-Cause: %d)", opName, errCause)
					} else {
						msg = fmt.Sprintf("%s-NAK rejected by NAS (Session not found or prohibited)", opName)
					}
				}
				return &CoAResult{
					Success:    false,
					Code:       replyPkt.Code,
					ErrorCause: errCause,
					Message:    msg,
				}, nil
			}

			return &CoAResult{
				Success: false,
				Code:    replyPkt.Code,
				Message: fmt.Sprintf("Unexpected response code %d from NAS", replyPkt.Code),
			}, nil
		}
	}

	return nil, fmt.Errorf("timeout waiting for NAS %s reply after %d attempts: %w", addr, len(backoffs), lastErr)
}

// SendDisconnectRequest sends RFC 3576 / RFC 5176 Disconnect-Request (PoD) to NAS (MikroTik)
func SendDisconnectRequest(nasIP string, nasPort int, secret []byte, username, sessionID string, framedIP net.IP) (*CoAResult, error) {
	// Generate random identifier
	var idByte [1]byte
	_, _ = rand.Read(idByte[:])

	pkt := NewPacket(CodeDisconnectRequest, idByte[0], secret)
	// Generate 16 random bytes for RFC 5176 Request Authenticator
	_, _ = rand.Read(pkt.Authenticator[:])

	if username != "" {
		pkt.AddString(AttrUserName, username)
	}
	if sessionID != "" {
		pkt.AddString(AttrAcctSessionID, sessionID)
	}
	if framedIP != nil {
		pkt.AddIP(AttrFramedIPAddress, framedIP)
	}

	// Include Message-Authenticator (RFC 2869 / RFC 5176)
	pkt.AddMessageAuthenticator()

	return sendWithRetry(nasIP, nasPort, pkt, secret, false)
}

// SendCoARequest sends RFC 3576 / RFC 5176 CoA-Request to NAS (MikroTik) to dynamically
// modify active session attributes (bandwidth rate-limit, address-list, pool) without disconnecting
func SendCoARequest(nasIP string, nasPort int, secret []byte, username, sessionID string, opts CoAOptions) (*CoAResult, error) {
	var idByte [1]byte
	_, _ = rand.Read(idByte[:])

	pkt := NewPacket(CodeCoARequest, idByte[0], secret)
	_, _ = rand.Read(pkt.Authenticator[:])

	if username != "" {
		pkt.AddString(AttrUserName, username)
	}
	if sessionID != "" {
		pkt.AddString(AttrAcctSessionID, sessionID)
	}
	if opts.FramedIP != nil {
		pkt.AddIP(AttrFramedIPAddress, opts.FramedIP)
	}
	if opts.RateLimit != "" {
		pkt.AddMikrotikRateLimit(opts.RateLimit)
	}
	if opts.AddressList != "" {
		pkt.AddMikrotikAddressList(opts.AddressList)
	}
	if opts.PoolName != "" {
		pkt.AddString(AttrFramedPool, opts.PoolName)
	}

	pkt.AddMessageAuthenticator()

	return sendWithRetry(nasIP, nasPort, pkt, secret, true)
}

// SendCoARateLimit sends CoA-Request to change user bandwidth on the fly without disconnect
func SendCoARateLimit(nasIP string, nasPort int, secret []byte, username, sessionID, newRateLimit string) (*CoAResult, error) {
	return SendCoARequest(nasIP, nasPort, secret, username, sessionID, CoAOptions{RateLimit: newRateLimit})
}
