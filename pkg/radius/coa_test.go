package radius

import (
	"crypto/md5"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// mockNASServer starts a local UDP server simulating MikroTik RADIUS CoA listener
func mockNASServer(t *testing.T, secret []byte, handler func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn)) (*net.UDPConn, int) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to resolve UDP addr: %v", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("Failed to listen UDP: %v", err)
	}

	port := conn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, clientAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := make([]byte, n)
			copy(req, buf[:n])
			handler(req, clientAddr, conn)
		}
	}()

	return conn, port
}

func makeReply(code, id uint8, reqAuth [16]byte, secret []byte, attrs []Attribute) []byte {
	totalLen := 20
	for _, a := range attrs {
		totalLen += 2 + len(a.Value)
	}
	buf := make([]byte, totalLen)
	buf[0] = code
	buf[1] = id
	binary.BigEndian.PutUint16(buf[2:4], uint16(totalLen))

	off := 20
	for _, a := range attrs {
		buf[off] = a.Type
		buf[off+1] = uint8(2 + len(a.Value))
		copy(buf[off+2:], a.Value)
		off += 2 + len(a.Value)
	}

	// Calculate Response Authenticator = MD5(Code + ID + Length + RequestAuth + Attributes + Secret)
	h := md5.New()
	h.Write(buf[0:4])
	h.Write(reqAuth[:])
	h.Write(buf[20:totalLen])
	h.Write(secret)
	copy(buf[4:20], h.Sum(nil))

	return buf
}

func TestDisconnectRequestACK(t *testing.T) {
	secret := []byte("shared-nas-secret-123456")
	serverConn, port := mockNASServer(t, secret, func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn) {
		if len(req) < 20 {
			return
		}
		var reqAuth [16]byte
		copy(reqAuth[:], req[4:20])

		reply := makeReply(CodeDisconnectACK, req[1], reqAuth, secret, []Attribute{
			{Type: AttrReplyMessage, Value: []byte("Session disconnected")},
		})
		_, _ = conn.WriteToUDP(reply, clientAddr)
	})
	defer serverConn.Close()

	res, err := SendDisconnectRequest("127.0.0.1", port, secret, "testuser", "sess-123", net.ParseIP("10.0.0.5"))
	if err != nil {
		t.Fatalf("SendDisconnectRequest failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Expected success true, got false: %s", res.Message)
	}
	if res.Code != CodeDisconnectACK {
		t.Errorf("Expected CodeDisconnectACK (41), got %d", res.Code)
	}
}

func TestDisconnectRequestNAK(t *testing.T) {
	secret := []byte("shared-nas-secret-123456")
	serverConn, port := mockNASServer(t, secret, func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn) {
		var reqAuth [16]byte
		copy(reqAuth[:], req[4:20])

		errCauseBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(errCauseBytes, ErrorCauseSessionContextNotFound)

		reply := makeReply(CodeDisconnectNAK, req[1], reqAuth, secret, []Attribute{
			{Type: AttrErrorCause, Value: errCauseBytes},
			{Type: AttrReplyMessage, Value: []byte("Session not found")},
		})
		_, _ = conn.WriteToUDP(reply, clientAddr)
	})
	defer serverConn.Close()

	res, err := SendDisconnectRequest("127.0.0.1", port, secret, "testuser", "sess-unknown", nil)
	if err != nil {
		t.Fatalf("SendDisconnectRequest failed: %v", err)
	}
	if res.Success {
		t.Fatalf("Expected success false for NAK, got true")
	}
	if res.Code != CodeDisconnectNAK {
		t.Errorf("Expected CodeDisconnectNAK (42), got %d", res.Code)
	}
	if res.ErrorCause != ErrorCauseSessionContextNotFound {
		t.Errorf("Expected ErrorCause %d, got %d", ErrorCauseSessionContextNotFound, res.ErrorCause)
	}
}

func TestCoARequestACK(t *testing.T) {
	secret := []byte("shared-nas-secret-123456")
	serverConn, port := mockNASServer(t, secret, func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn) {
		var reqAuth [16]byte
		copy(reqAuth[:], req[4:20])

		reply := makeReply(CodeCoAACK, req[1], reqAuth, secret, nil)
		_, _ = conn.WriteToUDP(reply, clientAddr)
	})
	defer serverConn.Close()

	res, err := SendCoARateLimit("127.0.0.1", port, secret, "testuser", "sess-123", "20M/40M")
	if err != nil {
		t.Fatalf("SendCoARateLimit failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Expected success true for CoA ACK, got false: %s", res.Message)
	}
	if res.Code != CodeCoAACK {
		t.Errorf("Expected CodeCoAACK (44), got %d", res.Code)
	}
}

func TestCoARetryOnPacketLoss(t *testing.T) {
	secret := []byte("shared-nas-secret-123456")
	attemptCount := 0
	serverConn, port := mockNASServer(t, secret, func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn) {
		attemptCount++
		// Drop first attempt, answer on second attempt
		if attemptCount == 1 {
			return
		}
		var reqAuth [16]byte
		copy(reqAuth[:], req[4:20])

		reply := makeReply(CodeDisconnectACK, req[1], reqAuth, secret, nil)
		_, _ = conn.WriteToUDP(reply, clientAddr)
	})
	defer serverConn.Close()

	res, err := SendDisconnectRequest("127.0.0.1", port, secret, "testuser", "sess-123", nil)
	if err != nil {
		t.Fatalf("SendDisconnectRequest failed on retry: %v", err)
	}
	if !res.Success {
		t.Fatalf("Expected success true after retry, got false")
	}
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts, got %d", attemptCount)
	}
}

func TestCoAInvalidResponseAuthenticator(t *testing.T) {
	secret := []byte("shared-nas-secret-123456")
	wrongSecret := []byte("wrong-nas-secret-999999")

	serverConn, port := mockNASServer(t, secret, func(req []byte, clientAddr *net.UDPAddr, conn *net.UDPConn) {
		var reqAuth [16]byte
		copy(reqAuth[:], req[4:20])

		// Corrupt authenticator by signing with wrong secret
		reply := makeReply(CodeDisconnectACK, req[1], reqAuth, wrongSecret, nil)
		_, _ = conn.WriteToUDP(reply, clientAddr)
	})
	defer serverConn.Close()

	_, err := SendDisconnectRequest("127.0.0.1", port, secret, "testuser", "sess-123", nil)
	if err == nil {
		t.Fatalf("Expected error due to invalid response authenticator, got nil")
	}
}
