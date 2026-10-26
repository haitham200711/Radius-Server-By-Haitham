package crypto

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func TestCryptoGenerateRandom(t *testing.T) {
	s1 := GenerateRandomSecret(16)
	s2 := GenerateRandomSecret(16)
	if len(s1) != 32 || len(s2) != 32 {
		t.Fatalf("expected 32 hex chars for 16 bytes, got %d and %d", len(s1), len(s2))
	}
	if s1 == s2 {
		t.Fatalf("expected unique secrets, got identical")
	}

	p1 := GenerateRandomPassword(16)
	p2 := GenerateRandomPassword(16)
	if len(p1) != 16 || len(p2) != 16 {
		t.Fatalf("expected length 16 password")
	}
	if p1 == p2 {
		t.Fatalf("expected unique passwords")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	_ = os.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	masterKey = nil
	masterKeyOnce = sync.Once{}

	plaintext := []byte("SuperSecretDatabasePassword!#2026")
	enc, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	dec, err := Decrypt(enc)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if string(dec) != string(plaintext) {
		t.Fatalf("expected decrypted string '%s', got '%s'", plaintext, dec)
	}
}

func TestMaskSecret(t *testing.T) {
	if MaskSecret("") != "" {
		t.Errorf("empty secret should mask to empty")
	}
	if MaskSecret("123") != "••••" {
		t.Errorf("short secret should mask to ••••")
	}
	m := MaskSecret("secretValue123")
	if !strings.HasPrefix(m, "se••••") || !strings.HasSuffix(m, "23") {
		t.Errorf("unexpected masked format: %s", m)
	}
}

func TestRedactLog(t *testing.T) {
	raw := "Connect to DB with password=secretPass123 and token=xyz987"
	red := RedactLog(raw)
	if strings.Contains(red, "secretPass123") || strings.Contains(red, "xyz987") {
		t.Fatalf("sensitive info not redacted: %s", red)
	}
}
