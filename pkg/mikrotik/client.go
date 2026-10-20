package mikrotik

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client represents an active connection to a MikroTik RouterOS API
type Client struct {
	conn net.Conn
	mu   sync.Mutex
}

var (
	poolMu  sync.Mutex
	poolMap = make(map[string]*Client)
)

// GetPooledClient returns an existing active client from the connection pool or establishes a new one.
// Persistent connections drastically improve polling latency (from ~250ms to ~5ms) and avoid socket exhaustion.
func GetPooledClient(addr, user, pass string, timeout time.Duration) (*Client, error) {
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "8728")
	}
	key := fmt.Sprintf("%s|%s|%s", addr, user, pass)

	poolMu.Lock()
	existing, ok := poolMap[key]
	poolMu.Unlock()

	if ok && existing != nil {
		return existing, nil
	}

	// Connect fresh
	newClient, err := Connect(addr, user, pass, timeout)
	if err != nil {
		return nil, err
	}

	poolMu.Lock()
	poolMap[key] = newClient
	poolMu.Unlock()

	return newClient, nil
}

// InvalidatePoolClient removes a broken client from the connection pool
func InvalidatePoolClient(addr, user, pass string) {
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "8728")
	}
	key := fmt.Sprintf("%s|%s|%s", addr, user, pass)

	poolMu.Lock()
	if c, ok := poolMap[key]; ok {
		_ = c.Close()
		delete(poolMap, key)
	}
	poolMu.Unlock()
}

// Connect dials the MikroTik RouterOS API at addr (e.g. "192.168.2.1:8728")
// and performs authentication supporting RouterOS v6 (including legacy MD5 challenge) and v7.
func Connect(addr string, user, pass string, timeout time.Duration) (*Client, error) {
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "8728")
	}

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("mikrotik dial failed (%s): %w", addr, err)
	}

	c := &Client{conn: conn}

	// Set connection deadline for authentication handshake
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() {
		_ = conn.SetDeadline(time.Time{})
	}()

	// 1. Try modern post-6.43 & RouterOS v7 login
	loginWords := []string{"/login", "=name=" + user, "=password=" + pass}
	if err := c.sendSentence(loginWords); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed sending login sentence: %w", err)
	}

	reply, err := c.readSentence()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed reading login response: %w", err)
	}

	if len(reply) == 0 {
		_ = conn.Close()
		return nil, fmt.Errorf("empty login response from router")
	}

	// Check if router returned MD5 challenge (=ret=HEX) for pre-6.43 legacy login
	if reply[0] == "!done" {
		var challengeHex string
		for _, w := range reply[1:] {
			if strings.HasPrefix(w, "=ret=") {
				challengeHex = strings.TrimPrefix(w, "=ret=")
				break
			}
		}

		if challengeHex != "" {
			// Legacy MD5 Challenge Response
			challengeBytes, decErr := hex.DecodeString(challengeHex)
			if decErr != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("invalid challenge hex: %w", decErr)
			}

			hasher := md5.New()
			hasher.Write([]byte{0})
			hasher.Write([]byte(pass))
			hasher.Write(challengeBytes)
			responseHex := "00" + hex.EncodeToString(hasher.Sum(nil))

			if err := c.sendSentence([]string{"/login", "=name=" + user, "=response=" + responseHex}); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("legacy login response send failed: %w", err)
			}

			legReply, err := c.readSentence()
			if err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("legacy login read failed: %w", err)
			}
			if len(legReply) == 0 || legReply[0] != "!done" {
				_ = conn.Close()
				return nil, fmt.Errorf("legacy login rejected: %v", legReply)
			}
		}
		// Successfully authenticated
		return c, nil
	}

	if reply[0] == "!trap" {
		msg := "authentication failed"
		for _, w := range reply[1:] {
			if strings.HasPrefix(w, "=message=") {
				msg = strings.TrimPrefix(w, "=message=")
				break
			}
		}
		_ = conn.Close()
		return nil, fmt.Errorf("login error: %s", msg)
	}

	_ = conn.Close()
	return nil, fmt.Errorf("unexpected login response: %v", reply)
}

// Close closes the underlying network connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Run executes a command sentence and returns parsed reply maps
func (c *Client) Run(sentence ...string) ([]map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.conn.SetDeadline(time.Now().Add(4 * time.Second))
	defer func() {
		_ = c.conn.SetDeadline(time.Time{})
	}()

	if err := c.sendSentence(sentence); err != nil {
		return nil, err
	}

	var results []map[string]string
	var lastTrapMsg string

	for {
		words, err := c.readSentence()
		if err != nil {
			return nil, err
		}
		if len(words) == 0 {
			continue
		}

		status := words[0]
		if status == "!re" {
			row := make(map[string]string)
			for _, w := range words[1:] {
				if strings.HasPrefix(w, "=") {
					parts := strings.SplitN(w[1:], "=", 2)
					if len(parts) == 2 {
						row[parts[0]] = parts[1]
					} else if len(parts) == 1 {
						row[parts[0]] = ""
					}
				}
			}
			results = append(results, row)
		} else if status == "!trap" {
			for _, w := range words[1:] {
				if strings.HasPrefix(w, "=message=") {
					lastTrapMsg = strings.TrimPrefix(w, "=message=")
				}
			}
		} else if status == "!done" {
			break
		} else if status == "!fatal" {
			return nil, fmt.Errorf("fatal router error: %v", words)
		}
	}

	if lastTrapMsg != "" && len(results) == 0 {
		return nil, fmt.Errorf("mikrotik trap: %s", lastTrapMsg)
	}

	return results, nil
}

// GetInterfaceTraffic monitors real-time traffic for the given interface name once.
// In RouterOS interface monitor-traffic:
// - tx-bits-per-second is traffic sent to client (Download)
// - rx-bits-per-second is traffic received from client (Upload)
func (c *Client) GetInterfaceTraffic(iface string) (rxBps int64, txBps int64, err error) {
	candidates := []string{
		"<pppoe-" + iface + ">",
		iface,
		"<sstp-" + iface + ">",
		"<l2tp-" + iface + ">",
		"pppoe-" + iface,
	}

	// Avoid duplicates
	seen := make(map[string]bool)
	var uniqueCandidates []string
	for _, cand := range candidates {
		if !seen[cand] {
			seen[cand] = true
			uniqueCandidates = append(uniqueCandidates, cand)
		}
	}

	var lastErr error
	for _, cand := range uniqueCandidates {
		res, err := c.Run("/interface/monitor-traffic", "=interface="+cand, "=once=")
		if err == nil && len(res) > 0 {
			rx, _ := strconv.ParseInt(res[0]["rx-bits-per-second"], 10, 64)
			tx, _ := strconv.ParseInt(res[0]["tx-bits-per-second"], 10, 64)
			return rx, tx, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	// Try Simple Queue fallback (e.g. if hotspot or queue-based)
	queueRes, qErr := c.Run("/queue/simple/print", "=?name="+iface)
	if qErr == nil && len(queueRes) > 0 {
		rate := queueRes[0]["rate"]
		if rate != "" {
			parts := strings.Split(rate, "/")
			if len(parts) >= 2 {
				rx, _ := strconv.ParseInt(parts[0], 10, 64)
				tx, _ := strconv.ParseInt(parts[1], 10, 64)
				return rx, tx, nil
			}
		}
	}

	if lastErr != nil {
		return 0, 0, lastErr
	}
	return 0, 0, fmt.Errorf("interface %s not found on router", iface)
}

// TestConnection queries /system/resource/print to verify credentials and fetch info
func (c *Client) TestConnection() (version string, board string, cpu int, uptime string, err error) {
	res, err := c.Run("/system/resource/print")
	if err != nil {
		return "", "", 0, "", err
	}
	if len(res) == 0 {
		return "", "", 0, "", fmt.Errorf("empty system resource output")
	}

	version = res[0]["version"]
	board = res[0]["board-name"]
	if board == "" {
		board = res[0]["platform"]
	}
	cpuLoad, _ := strconv.Atoi(res[0]["cpu-load"])
	uptime = res[0]["uptime"]
	return version, board, cpuLoad, uptime, nil
}

// --- RouterOS Binary Protocol Low-level Helpers ---

func (c *Client) sendSentence(words []string) error {
	for _, w := range words {
		if err := writeWord(c.conn, w); err != nil {
			return err
		}
	}
	// Sentence termination word (length 0)
	_, err := c.conn.Write([]byte{0x00})
	return err
}

func (c *Client) readSentence() ([]string, error) {
	var words []string
	for {
		word, err := readWord(c.conn)
		if err != nil {
			return nil, err
		}
		if word == "" {
			return words, nil
		}
		words = append(words, word)
	}
}

func writeWord(w io.Writer, word string) error {
	b := []byte(word)
	l := len(b)
	switch {
	case l < 0x80:
		if _, err := w.Write([]byte{byte(l)}); err != nil {
			return err
		}
	case l < 0x4000:
		val := l | 0x8000
		if _, err := w.Write([]byte{byte(val >> 8), byte(val & 0xFF)}); err != nil {
			return err
		}
	case l < 0x200000:
		val := l | 0xC00000
		if _, err := w.Write([]byte{byte(val >> 16), byte((val >> 8) & 0xFF), byte(val & 0xFF)}); err != nil {
			return err
		}
	case l < 0x10000000:
		val := l | int(0xE0000000)
		if _, err := w.Write([]byte{byte(val >> 24), byte((val >> 16) & 0xFF), byte((val >> 8) & 0xFF), byte(val & 0xFF)}); err != nil {
			return err
		}
	default:
		if _, err := w.Write([]byte{0xF0, byte(l >> 24), byte((l >> 16) & 0xFF), byte((l >> 8) & 0xFF), byte(l & 0xFF)}); err != nil {
			return err
		}
	}

	_, err := w.Write(b)
	return err
}

func readWord(r io.Reader) (string, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}

	var l int
	switch {
	case b[0] < 0x80:
		l = int(b[0])
	case (b[0] & 0xC0) == 0x80:
		var b2 [1]byte
		if _, err := io.ReadFull(r, b2[:]); err != nil {
			return "", err
		}
		l = int(b[0]&0x3F)<<8 | int(b2[0])
	case (b[0] & 0xE0) == 0xC0:
		var b2 [2]byte
		if _, err := io.ReadFull(r, b2[:]); err != nil {
			return "", err
		}
		l = int(b[0]&0x1F)<<16 | int(b2[0])<<8 | int(b2[1])
	case (b[0] & 0xF0) == 0xE0:
		var b2 [3]byte
		if _, err := io.ReadFull(r, b2[:]); err != nil {
			return "", err
		}
		l = int(b[0]&0x0F)<<24 | int(b2[0])<<16 | int(b2[1])<<8 | int(b2[2])
	case b[0] == 0xF0:
		var b2 [4]byte
		if _, err := io.ReadFull(r, b2[:]); err != nil {
			return "", err
		}
		l = int(b2[0])<<24 | int(b2[1])<<16 | int(b2[2])<<8 | int(b2[3])
	default:
		return "", fmt.Errorf("invalid length byte prefix: %02x", b[0])
	}

	if l == 0 {
		return "", nil
	}

	buf := make([]byte, l)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
