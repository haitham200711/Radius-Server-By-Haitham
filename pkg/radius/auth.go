package radius

import (
	"bytes"
	"crypto/des"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
)

// DecryptPAP decrypts RFC 2865 User-Password attribute
func DecryptPAP(encryptedPass []byte, secret []byte, auth [16]byte) (string, error) {
	if len(encryptedPass) == 0 || len(encryptedPass)%16 != 0 {
		return "", errors.New("invalid encrypted password length, must be multiple of 16")
	}

	decrypted := make([]byte, len(encryptedPass))
	prevBlock := auth[:]

	for offset := 0; offset < len(encryptedPass); offset += 16 {
		h := md5.New()
		h.Write(secret)
		h.Write(prevBlock)
		b := h.Sum(nil)

		for i := 0; i < 16; i++ {
			decrypted[offset+i] = encryptedPass[offset+i] ^ b[i]
		}
		prevBlock = encryptedPass[offset : offset+16]
	}

	// Remove trailing null bytes (padding)
	n := bytes.IndexByte(decrypted, 0)
	if n != -1 {
		decrypted = decrypted[:n]
	}

	return string(decrypted), nil
}

// VerifyPAP checks plaintext password against decrypted PAP password
func VerifyPAP(encryptedPass []byte, secret []byte, auth [16]byte, plainPass string) bool {
	pwd, err := DecryptPAP(encryptedPass, secret, auth)
	if err != nil {
		return false
	}
	return pwd == plainPass
}

// VerifyCHAP checks RFC 2865 CHAP-Password against user plain password
func VerifyCHAP(chapPass []byte, challenge []byte, plainPass string) bool {
	if len(chapPass) != 17 {
		return false
	}
	ident := chapPass[0]
	response := chapPass[1:17]

	h := md5.New()
	h.Write([]byte{ident})
	h.Write([]byte(plainPass))
	h.Write(challenge)
	expected := h.Sum(nil)

	return bytes.Equal(expected, response)
}

// Microsoft Vendor ID & Sub-attributes for MS-CHAPv2
const (
	MSVendorID          = 311
	MSCHAPChallenge     = 11
	MSCHAP2Response     = 25
	MSCHAP2Success      = 26
	MSCHAPError         = 28
	MSCHAPDomain        = 10
	MSMPPEEncryptionPol = 7
	MSMPPEEncryptionTyp = 8
	MSMPPESendKey       = 16
	MSMPPERecvKey       = 17
)

// VerifyMSCHAPv2 validates MS-CHAPv2 response using user's password
func VerifyMSCHAPv2(authChallenge []byte, msChap2Resp []byte, username string, plainPass string) (bool, []byte) {
	// msChap2Resp format:
	// Ident (1 byte) + Flags (1 byte) + Peer Challenge (16 bytes) + Reserved (8 bytes) + NT Response (24 bytes)
	if len(msChap2Resp) < 50 {
		return false, nil
	}

	ident := msChap2Resp[0]
	flags := msChap2Resp[1]
	peerChallenge := msChap2Resp[2:18]
	ntResponse := msChap2Resp[26:50]

	// Strip domain from username if present (e.g. DOMAIN\user -> user)
	cleanUser := username
	if idx := strings.LastIndex(username, "\\"); idx != -1 {
		cleanUser = username[idx+1:]
	}

	// Generate NT-Response
	passwordHash := ntPasswordHash(plainPass)
	challenge := challengeHash(peerChallenge, authChallenge, cleanUser)
	expectedNTResp := challengeResponse(challenge, passwordHash)

	if !bytes.Equal(ntResponse, expectedNTResp) {
		return false, nil
	}

	// Generate Authenticator Response for MS-CHAP2-Success
	authResp := generateAuthenticatorResponse(passwordHash, ntResponse, peerChallenge, authChallenge, cleanUser)

	// Format MS-CHAP2-Success: Ident + "S=" + 42-hex-chars
	successData := make([]byte, 1+len(authResp))
	successData[0] = ident
	copy(successData[1:], authResp)

	_ = flags
	return true, successData
}

// ntPasswordHash computes MD4(UTF-16LE(password))
func ntPasswordHash(password string) []byte {
	u16 := utf16.Encode([]rune(password))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return md4Hash(b)
}

func challengeHash(peerChallenge, authChallenge []byte, username string) []byte {
	h := sha1.New()
	h.Write(peerChallenge)
	h.Write(authChallenge)
	h.Write([]byte(username))
	return h.Sum(nil)[:8]
}

func challengeResponse(challenge []byte, passwordHash []byte) []byte {
	// Zero pad password hash from 16 to 21 bytes
	zpad := make([]byte, 21)
	copy(zpad, passwordHash)

	resp := make([]byte, 24)
	desEncrypt(resp[0:8], challenge, zpad[0:7])
	desEncrypt(resp[8:16], challenge, zpad[7:14])
	desEncrypt(resp[16:24], challenge, zpad[14:21])
	return resp
}

func desEncrypt(dst, src, key7 []byte) {
	key8 := make56to64(key7)
	block, err := des.NewCipher(key8)
	if err != nil {
		return
	}
	block.Encrypt(dst, src)
}

func make56to64(k []byte) []byte {
	out := make([]byte, 8)
	out[0] = k[0] & 0xFE
	out[1] = ((k[0] << 7) | (k[1] >> 1)) & 0xFE
	out[2] = ((k[1] << 6) | (k[2] >> 2)) & 0xFE
	out[3] = ((k[2] << 5) | (k[3] >> 3)) & 0xFE
	out[4] = ((k[3] << 4) | (k[4] >> 4)) & 0xFE
	out[5] = ((k[4] << 3) | (k[5] >> 5)) & 0xFE
	out[6] = ((k[5] << 2) | (k[6] >> 6)) & 0xFE
	out[7] = (k[6] << 1) & 0xFE
	for i := range out {
		var parity byte
		for b := out[i]; b != 0; b >>= 1 {
			parity ^= (b & 1)
		}
		if parity == 0 {
			out[i] |= 1
		}
	}
	return out
}

func generateAuthenticatorResponse(passwordHash, ntResponse, peerChallenge, authChallenge []byte, username string) []byte {
	magic1 := []byte("Magic server to client signing constant")
	magic2 := []byte("Pad to make it do more than one iteration")

	h1 := sha1.New()
	h1.Write(passwordHash)
	h1.Write(ntResponse)
	h1.Write(magic1)
	digest := h1.Sum(nil)

	challenge := challengeHash(peerChallenge, authChallenge, username)

	h2 := sha1.New()
	h2.Write(digest)
	h2.Write(challenge)
	h2.Write(magic2)
	finalDigest := h2.Sum(nil)

	hexStr := "S="
	for _, b := range finalDigest {
		hexStr += hexChar(b >> 4)
		hexStr += hexChar(b & 0x0F)
	}
	return []byte(strings.ToUpper(hexStr))
}

func hexChar(b byte) string {
	const hex = "0123456789ABCDEF"
	return string(hex[b&0x0F])
}

// Minimal MD4 implementation for NTLM hash
func md4Hash(data []byte) []byte {
	a := uint32(0x67452301)
	b := uint32(0xefcdab89)
	c := uint32(0x98badcfe)
	d := uint32(0x10325476)

	msgLen := uint64(len(data))
	padded := append(data, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0)
	}
	lenBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenBuf, msgLen*8)
	padded = append(padded, lenBuf...)

	f := func(x, y, z uint32) uint32 { return (x & y) | (^x & z) }
	g := func(x, y, z uint32) uint32 { return (x & y) | (x & z) | (y & z) }
	h := func(x, y, z uint32) uint32 { return x ^ y ^ z }
	rol := func(x uint32, n uint) uint32 { return (x << n) | (x >> (32 - n)) }

	for chunk := 0; chunk < len(padded); chunk += 64 {
		var x [16]uint32
		for i := 0; i < 16; i++ {
			x[i] = binary.LittleEndian.Uint32(padded[chunk+i*4 : chunk+i*4+4])
		}

		aa, bb, cc, dd := a, b, c, d

		// Round 1
		for _, s := range []struct {
			idx int
			sh  uint
		}{
			{0, 3}, {1, 7}, {2, 11}, {3, 19},
			{4, 3}, {5, 7}, {6, 11}, {7, 19},
			{8, 3}, {9, 7}, {10, 11}, {11, 19},
			{12, 3}, {13, 7}, {14, 11}, {15, 19},
		} {
			aa = rol(aa+f(bb, cc, dd)+x[s.idx], s.sh)
			aa, bb, cc, dd = dd, aa, bb, cc
		}

		// Round 2
		for _, s := range []struct {
			idx int
			sh  uint
		}{
			{0, 3}, {4, 5}, {8, 9}, {12, 13},
			{1, 3}, {5, 5}, {9, 9}, {13, 13},
			{2, 3}, {6, 5}, {10, 9}, {14, 13},
			{3, 3}, {7, 5}, {11, 9}, {15, 13},
		} {
			aa = rol(aa+g(bb, cc, dd)+x[s.idx]+0x5a827999, s.sh)
			aa, bb, cc, dd = dd, aa, bb, cc
		}

		// Round 3
		for _, s := range []struct {
			idx int
			sh  uint
		}{
			{0, 3}, {8, 9}, {4, 11}, {12, 15},
			{2, 3}, {10, 9}, {6, 11}, {14, 15},
			{1, 3}, {9, 9}, {5, 11}, {13, 15},
			{3, 3}, {11, 9}, {7, 11}, {15, 15},
		} {
			aa = rol(aa+h(bb, cc, dd)+x[s.idx]+0x6ed9eba1, s.sh)
			aa, bb, cc, dd = dd, aa, bb, cc
		}

		a += aa
		b += bb
		c += cc
		d += dd
	}

	res := make([]byte, 16)
	binary.LittleEndian.PutUint32(res[0:4], a)
	binary.LittleEndian.PutUint32(res[4:8], b)
	binary.LittleEndian.PutUint32(res[8:12], c)
	binary.LittleEndian.PutUint32(res[12:16], d)
	return res
}
