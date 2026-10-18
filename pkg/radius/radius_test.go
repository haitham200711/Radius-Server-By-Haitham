package radius

import (
	"crypto/md5"
	"net"
	"testing"
)

func TestPacketEncodingDecoding(t *testing.T) {
	secret := []byte("radius123")
	pkt := NewPacket(CodeAccessRequest, 1, secret)
	pkt.AddString(AttrUserName, "testuser")
	pkt.AddMikrotikRateLimit("10M/20M")

	// Mock request authenticator
	var reqAuth [16]byte
	copy(reqAuth[:], []byte("1234567890123456"))
	pkt.Authenticator = reqAuth

	data, err := pkt.Encode(nil)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	parsed, err := ParsePacket(data, secret)
	if err != nil {
		t.Fatalf("ParsePacket failed: %v", err)
	}

	if parsed.Code != CodeAccessRequest {
		t.Errorf("Expected code %d, got %d", CodeAccessRequest, parsed.Code)
	}
	if parsed.Identifier != 1 {
		t.Errorf("Expected ident 1, got %d", parsed.Identifier)
	}

	username := parsed.GetString(AttrUserName)
	if username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", username)
	}

	rateLimit := parsed.GetMikrotikVSA(MikrotikRateLimit)
	if string(rateLimit) != "10M/20M" {
		t.Errorf("Expected rate limit '10M/20M', got '%s'", string(rateLimit))
	}
}

func TestCHAPAuthentication(t *testing.T) {
	password := "mypassword"
	challenge := []byte("randomchallenge1")
	ident := uint8(42)

	h := md5.New()
	h.Write([]byte{ident})
	h.Write([]byte(password))
	h.Write(challenge)
	expectedHash := h.Sum(nil)

	chapPass := make([]byte, 17)
	chapPass[0] = ident
	copy(chapPass[1:], expectedHash)

	// Valid password
	if !VerifyCHAP(chapPass, challenge, password) {
		t.Errorf("VerifyCHAP failed for valid password")
	}

	// Wrong password
	if VerifyCHAP(chapPass, challenge, "wrongpass") {
		t.Errorf("VerifyCHAP succeeded for wrong password")
	}
}

func TestPAPAuthentication(t *testing.T) {
	secret := []byte("sharedsecret")
	var auth [16]byte
	copy(auth[:], []byte("authenticator012"))

	password := "secret123"

	// Encrypt password PAP style
	paddedPass := make([]byte, 16)
	copy(paddedPass, []byte(password))

	h := md5.New()
	h.Write(secret)
	h.Write(auth[:])
	mask := h.Sum(nil)

	encPass := make([]byte, 16)
	for i := 0; i < 16; i++ {
		encPass[i] = paddedPass[i] ^ mask[i]
	}

	// Verify PAP
	if !VerifyPAP(encPass, secret, auth, password) {
		t.Errorf("VerifyPAP failed with valid password")
	}

	if VerifyPAP(encPass, secret, auth, "wrongpassword") {
		t.Errorf("VerifyPAP succeeded with wrong password")
	}
}

func TestAccessAcceptEncoding(t *testing.T) {
	secret := []byte("radius123")
	var reqAuth [16]byte
	copy(reqAuth[:], []byte("0123456789abcdef"))

	resp := NewPacket(CodeAccessAccept, 5, secret)
	resp.AddIP(AttrFramedIPAddress, net.ParseIP("192.168.88.50"))
	resp.AddMikrotikRateLimit("5M/10M")
	resp.AddUint32(AttrSessionTimeout, 86400)

	data, err := resp.Encode(&reqAuth)
	if err != nil {
		t.Fatalf("Failed to encode Access-Accept: %v", err)
	}

	parsed, err := ParsePacket(data, secret)
	if err != nil {
		t.Fatalf("Failed to parse Access-Accept: %v", err)
	}

	if parsed.Code != CodeAccessAccept {
		t.Errorf("Expected code Access-Accept, got %d", parsed.Code)
	}

	ip := parsed.GetIP(AttrFramedIPAddress)
	if ip == nil || ip.String() != "192.168.88.50" {
		t.Errorf("Expected Framed-IP 192.168.88.50, got %v", ip)
	}

	timeout := parsed.GetUint32(AttrSessionTimeout)
	if timeout != 86400 {
		t.Errorf("Expected Session-Timeout 86400, got %d", timeout)
	}
}
