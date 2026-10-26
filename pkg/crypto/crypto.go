package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

var (
	masterKey     []byte
	masterKeyOnce sync.Once
	masterKeyErr  error
)

// GenerateRandomSecret generates cryptographically secure hex string of specified byte length
func GenerateRandomSecret(byteLen int) string {
	b := make([]byte, byteLen)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		// Fallback panic should never happen with system CSPRNG
		panic("crypto/rand is unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// GenerateRandomPassword generates a secure alphanumeric password of specified length
func GenerateRandomPassword(length int) string {
	const charset = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#%^&*"
	b := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto/rand is unavailable: " + err.Error())
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// GetMasterEncryptionKey returns the 256-bit (32 byte) key for AES-GCM
func GetMasterEncryptionKey() ([]byte, error) {
	masterKeyOnce.Do(func() {
		keyStr := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY"))
		if keyStr == "" {
			keyStr = strings.TrimSpace(os.Getenv("SESSION_SECRET"))
		}
		if keyStr == "" {
			masterKeyErr = errors.New("neither ENCRYPTION_KEY nor SESSION_SECRET is set in environment")
			return
		}

		// If hex string of at least 64 chars (32 bytes)
		if decoded, err := hex.DecodeString(keyStr); err == nil && len(decoded) >= 32 {
			masterKey = decoded[:32]
			return
		}

		// Otherwise derive 32 bytes from the string
		b := []byte(keyStr)
		if len(b) < 32 {
			padded := make([]byte, 32)
			copy(padded, b)
			masterKey = padded
		} else {
			masterKey = b[:32]
		}
	})

	return masterKey, masterKeyErr
}

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce
func Encrypt(plaintext []byte) (string, error) {
	key, err := GetMasterEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("master key error: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts hex-encoded ciphertext using AES-256-GCM
func Decrypt(ciphertextHex string) ([]byte, error) {
	data, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}

	key, err := GetMasterEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("master key error: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// MaskSecret returns a masked representation of a secret string (e.g. for display or logs)
func MaskSecret(secret string) string {
	if len(secret) == 0 {
		return ""
	}
	if len(secret) <= 4 {
		return "••••"
	}
	return secret[:2] + "••••" + secret[len(secret)-2:]
}

var (
	redactPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(password|passwd|pass|secret|token|api_token|auth_token|bearer)\s*[:=]\s*["']?([^"'\s&]+)["']?`),
		regexp.MustCompile(`(?i)(p=)([^&\s]+)`),
		regexp.MustCompile(`(?i)(-p)(\S+)`),
	}
)

// RedactLog sanitizes a log message by replacing sensitive keywords with [REDACTED]
func RedactLog(msg string) string {
	res := msg
	for _, p := range redactPatterns {
		res = p.ReplaceAllString(res, `$1=[REDACTED]`)
	}
	return res
}
