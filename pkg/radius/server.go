package radius

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/database"
)

// ServerConfig configures RADIUS server listener parameters
type ServerConfig struct {
	AuthPort int    // Default 1812
	AcctPort int    // Default 1813
	CoAPort  int    // Default 3799
	BindAddr string // e.g. "0.0.0.0"
}

// Server is the central RADIUS server instance
type Server struct {
	cfg      ServerConfig
	db       *database.DB
	authConn *net.UDPConn
	acctConn *net.UDPConn
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewServer creates a new RADIUS server instance
func NewServer(cfg ServerConfig, db *database.DB) *Server {
	if cfg.AuthPort <= 0 {
		cfg.AuthPort = 1812
	}
	if cfg.AcctPort <= 0 {
		cfg.AcctPort = 1813
	}
	if cfg.CoAPort <= 0 {
		cfg.CoAPort = 3799
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "0.0.0.0"
	}

	return &Server{
		cfg:      cfg,
		db:       db,
		stopChan: make(chan struct{}),
	}
}

// Start begins UDP listening for Auth and Accounting
func (s *Server) Start() error {
	// 1. Auth Listener
	authAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", s.cfg.BindAddr, s.cfg.AuthPort))
	if err != nil {
		return fmt.Errorf("failed to resolve auth UDP address: %w", err)
	}
	authConn, err := net.ListenUDP("udp", authAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on auth UDP port %d: %w", s.cfg.AuthPort, err)
	}
	s.authConn = authConn

	// 2. Acct Listener
	acctAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", s.cfg.BindAddr, s.cfg.AcctPort))
	if err != nil {
		return fmt.Errorf("failed to resolve acct UDP address: %w", err)
	}
	acctConn, err := net.ListenUDP("udp", acctAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on acct UDP port %d: %w", s.cfg.AcctPort, err)
	}
	s.acctConn = acctConn

	log.Printf("[RADIUS] Authentication server listening on %s:%d UDP", s.cfg.BindAddr, s.cfg.AuthPort)
	log.Printf("[RADIUS] Accounting server listening on %s:%d UDP", s.cfg.BindAddr, s.cfg.AcctPort)

	s.wg.Add(2)
	go s.listenAuth()
	go s.listenAcct()

	return nil
}

// Stop gracefully stops the RADIUS server
func (s *Server) Stop() {
	close(s.stopChan)
	if s.authConn != nil {
		_ = s.authConn.Close()
	}
	if s.acctConn != nil {
		_ = s.acctConn.Close()
	}
	s.wg.Wait()
	log.Printf("[RADIUS] Server stopped gracefully")
}

func (s *Server) getNASSecret(clientIP net.IP) []byte {
	ipStr := clientIP.String()
	nas, ok := s.db.Cache.GetNAS(ipStr)
	if ok {
		return []byte(nas.Secret)
	}
	// Fallback check: check if there's a 0.0.0.0 or default NAS
	defaultNAS, ok := s.db.Cache.GetNAS("0.0.0.0")
	if ok {
		return []byte(defaultNAS.Secret)
	}
	return []byte("radius123") // Default fallback secret
}

// --- Authentication Loop ---

func (s *Server) listenAuth() {
	defer s.wg.Done()
	buf := make([]byte, 4096)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		n, remoteAddr, err := s.authConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.stopChan:
				return
			default:
				continue
			}
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		go s.handleAuthPacket(data, remoteAddr)
	}
}

func (s *Server) handleAuthPacket(data []byte, remoteAddr *net.UDPAddr) {
	secret := s.getNASSecret(remoteAddr.IP)

	pkt, err := ParsePacket(data, secret)
	if err != nil {
		log.Printf("[RADIUS-AUTH] Malformed packet from %s: %v", remoteAddr.String(), err)
		return
	}

	if pkt.Code != CodeAccessRequest {
		return
	}

	username := pkt.GetString(AttrUserName)
	if username == "" {
		s.sendReject(pkt, remoteAddr, secret, "Username missing")
		return
	}

	// Clean username (e.g. trim spaces or domain)
	username = strings.TrimSpace(username)

	// Ultra-fast in-memory lookup
	user, exists := s.db.Cache.GetUser(username)
	if !exists {
		log.Printf("[RADIUS-AUTH] REJECT: User '%s' not found (from %s)", username, remoteAddr.String())
		s.sendReject(pkt, remoteAddr, secret, "User not found")
		return
	}

	// Check status
	if user.Status != "active" {
		log.Printf("[RADIUS-AUTH] REJECT: User '%s' status is %s", username, user.Status)
		s.sendReject(pkt, remoteAddr, secret, fmt.Sprintf("Account is %s", user.Status))
		return
	}

	// Check expiration date
	if user.ExpirationDate != nil && time.Now().After(*user.ExpirationDate) {
		log.Printf("[RADIUS-AUTH] REJECT: User '%s' expired on %s", username, user.ExpirationDate.Format(time.RFC3339))
		s.sendReject(pkt, remoteAddr, secret, "Account has expired")
		return
	}

	// Check data quota limit
	if user.DataLimitBytes > 0 && user.UsedDataBytes >= user.DataLimitBytes {
		log.Printf("[RADIUS-AUTH] REJECT: User '%s' exceeded quota (%d / %d bytes)", username, user.UsedDataBytes, user.DataLimitBytes)
		s.sendReject(pkt, remoteAddr, secret, "Data quota exceeded")
		return
	}

	// Check MAC binding if configured
	if user.MACAddress != "" {
		callingStation := pkt.GetString(AttrCallingStationID)
		if callingStation != "" && !strings.EqualFold(user.MACAddress, callingStation) {
			log.Printf("[RADIUS-AUTH] REJECT: User '%s' MAC mismatch (expected %s, got %s)", username, user.MACAddress, callingStation)
			s.sendReject(pkt, remoteAddr, secret, "MAC address not allowed")
			return
		}
	}

	// Authenticate password
	authenticated := false

	// 1. PAP Check (RFC 2865)
	if papPass := pkt.GetAttribute(AttrUserPassword); len(papPass) > 0 {
		authenticated = VerifyPAP(papPass, secret, pkt.Authenticator, user.Password)
	}

	// 2. CHAP Check (RFC 2865)
	if !authenticated {
		if chapPass := pkt.GetAttribute(AttrCHAPPassword); len(chapPass) == 17 {
			challenge := pkt.GetAttribute(AttrCHAPChallenge)
			if len(challenge) == 0 {
				challenge = pkt.Authenticator[:]
			}
			authenticated = VerifyCHAP(chapPass, challenge, user.Password)
		}
	}

	if !authenticated {
		log.Printf("[RADIUS-AUTH] REJECT: User '%s' invalid credentials", username)
		s.sendReject(pkt, remoteAddr, secret, "Invalid credentials")
		return
	}

	// Authenticated successfully -> build Access-Accept
	log.Printf("[RADIUS-AUTH] ACCEPT: User '%s' authenticated successfully from %s", username, remoteAddr.String())
	s.sendAccept(pkt, remoteAddr, secret, user)
}

func (s *Server) sendAccept(req *Packet, remoteAddr *net.UDPAddr, secret []byte, user *database.CachedUser) {
	resp := NewPacket(CodeAccessAccept, req.Identifier, secret)

	// Standard attributes
	resp.AddUint32(AttrServiceType, 2)    // Framed-User
	resp.AddUint32(AttrFramedProtocol, 1) // PPP

	// Static IP or Pool
	if user.IPAddress != "" {
		if ip := net.ParseIP(user.IPAddress); ip != nil {
			resp.AddIP(AttrFramedIPAddress, ip)
		}
	}

	// MikroTik Rate-Limit VSA (Bandwidth shaping, e.g. "5M/10M")
	if user.RateLimit != "" {
		resp.AddMikrotikRateLimit(user.RateLimit)
	}

	// MikroTik Address-List VSA
	if user.AddressList != "" {
		resp.AddMikrotikAddressList(user.AddressList)
	}

	// Session Timeout
	if user.SessionTimeout > 0 {
		resp.AddUint32(AttrSessionTimeout, uint32(user.SessionTimeout))
	}

	// Idle Timeout
	if user.IdleTimeout > 0 {
		resp.AddUint32(AttrIdleTimeout, uint32(user.IdleTimeout))
	}

	// Acct-Interim-Interval (tell MikroTik to report interim accounting every 120s)
	resp.AddUint32(AttrAcctInterimInterval, 120)

	respBytes, err := resp.Encode(&req.Authenticator)
	if err != nil {
		log.Printf("[RADIUS-AUTH] Error encoding Access-Accept: %v", err)
		return
	}

	_, _ = s.authConn.WriteToUDP(respBytes, remoteAddr)
}

func (s *Server) sendReject(req *Packet, remoteAddr *net.UDPAddr, secret []byte, replyMsg string) {
	resp := NewPacket(CodeAccessReject, req.Identifier, secret)
	if replyMsg != "" {
		resp.AddString(AttrReplyMessage, replyMsg)
	}

	respBytes, err := resp.Encode(&req.Authenticator)
	if err != nil {
		log.Printf("[RADIUS-AUTH] Error encoding Access-Reject: %v", err)
		return
	}

	_, _ = s.authConn.WriteToUDP(respBytes, remoteAddr)
}

// --- Accounting Loop ---

func (s *Server) listenAcct() {
	defer s.wg.Done()
	buf := make([]byte, 4096)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		n, remoteAddr, err := s.acctConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.stopChan:
				return
			default:
				continue
			}
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		go s.handleAcctPacket(data, remoteAddr)
	}
}

func (s *Server) handleAcctPacket(data []byte, remoteAddr *net.UDPAddr) {
	secret := s.getNASSecret(remoteAddr.IP)

	pkt, err := ParsePacket(data, secret)
	if err != nil {
		return
	}

	if pkt.Code != CodeAccountingRequest {
		return
	}

	// Verify request authenticator
	if !pkt.VerifyAccountingRequest(secret) {
		log.Printf("[RADIUS-ACCT] Authenticator verification failed for %s", remoteAddr.String())
		return
	}

	statusType := pkt.GetUint32(AttrAcctStatusType)
	sessID := pkt.GetString(AttrAcctSessionID)
	username := pkt.GetString(AttrUserName)
	nasIP := remoteAddr.IP.String()

	// Octets + Gigawords calculation
	inOctets := int64(pkt.GetUint32(AttrAcctInputOctets))
	inGiga := int64(pkt.GetUint32(AttrAcctInputGigawords))
	totalIn := (inGiga << 32) + inOctets

	outOctets := int64(pkt.GetUint32(AttrAcctOutputOctets))
	outGiga := int64(pkt.GetUint32(AttrAcctOutputGigawords))
	totalOut := (outGiga << 32) + outOctets

	sessTime := int64(pkt.GetUint32(AttrAcctSessionTime))
	framedIP := ""
	if fIP := pkt.GetIP(AttrFramedIPAddress); fIP != nil {
		framedIP = fIP.String()
	}
	callingMAC := pkt.GetString(AttrCallingStationID)

	switch statusType {
	case AcctStatusStart:
		log.Printf("[RADIUS-ACCT] START: User '%s', Session '%s', IP '%s', MAC '%s'", username, sessID, framedIP, callingMAC)
		_ = s.db.RecordAcctStart(sessID, nasIP, username, framedIP, callingMAC)

	case AcctStatusInterimUpdate:
		_ = s.db.RecordAcctUpdate(sessID, nasIP, username, sessTime, totalIn, totalOut, framedIP)

	case AcctStatusStop:
		termCause := pkt.GetString(AttrAcctTerminateCause)
		log.Printf("[RADIUS-ACCT] STOP: User '%s', Session '%s', Time %ds, In %d, Out %d, Cause: %s",
			username, sessID, sessTime, totalIn, totalOut, termCause)
		_ = s.db.RecordAcctStop(sessID, nasIP, username, sessTime, totalIn, totalOut, termCause)
	}

	// Always acknowledge valid accounting packets
	resp := NewPacket(CodeAccountingResponse, pkt.Identifier, secret)
	respBytes, err := resp.Encode(&pkt.Authenticator)
	if err == nil {
		_, _ = s.acctConn.WriteToUDP(respBytes, remoteAddr)
	}
}
