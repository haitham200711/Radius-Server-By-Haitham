package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
)

//go:embed static/*
var staticFS embed.FS

// Server is the HTTP Web & REST API server
type Server struct {
	addr       string
	db         *database.DB
	httpServer *http.Server
}

// NewServer creates a new web dashboard server
func NewServer(addr string, db *database.DB) *Server {
	if addr == "" {
		addr = ":8080"
	}
	return &Server{
		addr: addr,
		db:   db,
	}
}

// Start launches the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API Endpoints
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/users", s.handleUsers)
	mux.HandleFunc("/api/users/batch", s.handleBatchUsers)
	mux.HandleFunc("/api/nas", s.handleNAS)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/mikrotik/script", s.handleMikrotikScript)

	// Static Assets embedded
	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to load static files: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(subFS)))

	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	log.Printf("[WEB] Web Dashboard & API listening on http://%s", s.addr)
	return s.httpServer.ListenAndServe()
}

// Stop stops the HTTP server
func (s *Server) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

// Helper to write JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetDashboardStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		pageSize, _ := strconv.Atoi(q.Get("pageSize"))
		profileID, _ := strconv.ParseInt(q.Get("profileId"), 10, 64)

		filter := database.UserFilter{
			Search:    q.Get("search"),
			ProfileID: profileID,
			Status:    q.Get("status"),
			Page:      page,
			PageSize:  pageSize,
		}

		res, err := s.db.GetUsers(filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)

	case http.MethodPost:
		var u database.User
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
			return
		}
		if u.Username == "" || u.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Username and password required"})
			return
		}
		if err := s.db.SaveUser(&u); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, u)

	case http.MethodDelete:
		idStr := r.URL.Query().Get("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid user ID"})
			return
		}
		if err := s.db.DeleteUser(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type BatchRequest struct {
	Count        int    `json:"count"`
	Prefix       string `json:"prefix"`
	UserLength   int    `json:"user_length"`
	ProfileID    int64  `json:"profile_id"`
	DataLimitGB  int64  `json:"data_limit_gb"`
	ValidityDays int    `json:"validity_days"`
}

func (s *Server) handleBatchUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
		return
	}

	count, err := s.db.BatchGenerateUsers(req.Count, req.Prefix, req.UserLength, req.ProfileID, req.DataLimitGB, req.ValidityDays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"created": count,
	})
}

func (s *Server) handleNAS(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.db.GetAllNAS()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, items)

	case http.MethodPost:
		var n database.NAS
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
			return
		}
		if n.IP == "" || n.Secret == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "IP and Secret are required"})
			return
		}
		if err := s.db.SaveNAS(&n); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, n)

	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err := s.db.DeleteNAS(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.db.GetAllProfiles()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, items)

	case http.MethodPost:
		var p database.Profile
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
			return
		}
		if p.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Profile name is required"})
			return
		}
		if err := s.db.SaveProfile(&p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, p)

	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err := s.db.DeleteProfile(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.GetActiveSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

type DisconnectRequest struct {
	NASIP     string `json:"nas_ip"`
	CoAPort   int    `json:"coa_port"`
	Username  string `json:"username"`
	SessionID string `json:"session_id"`
	FramedIP  string `json:"framed_ip"`
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req DisconnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
		return
	}

	nas, ok := s.db.Cache.GetNAS(req.NASIP)
	secret := []byte("radius123")
	coaPort := 3799
	if ok {
		secret = []byte(nas.Secret)
		if nas.CoAPort > 0 {
			coaPort = nas.CoAPort
		}
	}
	if req.CoAPort > 0 {
		coaPort = req.CoAPort
	}

	var framedIP net.IP
	if req.FramedIP != "" {
		framedIP = net.ParseIP(req.FramedIP)
	}

	res, err := radius.SendDisconnectRequest(req.NASIP, coaPort, secret, req.Username, req.SessionID, framedIP)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Mark session stopped in DB
	_ = s.db.RecordAcctStop(req.SessionID, req.NASIP, req.Username, 0, 0, 0, "Admin-Reset")

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	logs, total, err := s.db.GetAcctLogs(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":   logs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleMikrotikScript(w http.ResponseWriter, r *http.Request) {
	nasIP := r.URL.Query().Get("serverIp")
	secret := r.URL.Query().Get("secret")
	if nasIP == "" {
		nasIP = "192.168.2.90"
	}
	if secret == "" {
		secret = "radius123"
	}

	script := fmt.Sprintf(`# ========================================================
# MikroTik RouterOS RADIUS Configuration Script
# Generated by Radius-Server-By-Haitham
# ========================================================

# 1. Enable RADIUS for PPP (PPPoE / PPTP / L2TP / SSTP / OVPN)
/ppp aaa set use-radius=yes interim-update=2m accounting=yes

# 2. Enable RADIUS for Hotspot (if using Hotspot)
/ip hotspot profile set [find default=yes] use-radius=yes radius-interim-update=2m

# 3. Enable CoA (Incoming Disconnect & Bandwidth change)
/radius incoming set accept=yes port=3799

# 4. Remove previous Radius configuration if needed
/radius remove [find address="%s"]

# 5. Add RADIUS Server
/radius add address=%s secret="%s" service=ppp,hotspot,login authentication-port=1812 accounting-port=1813 timeout=3s comment="Radius-Server-By-Haitham"

:put ">>> RADIUS Configuration Applied Successfully! <<<"
`, nasIP, nasIP, secret)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}
