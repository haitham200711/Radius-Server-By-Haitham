package sstp

import (
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

func New(db *database.DB, r *web.Renderer) *Blueprint {
	return &Blueprint{db: db, renderer: r}
}

type SessionInfo struct {
	IfName     string
	Username   string
	CallingIP  string
	IP         string
	Type       string
	State      string
	Uptime     string
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	settings, err := bp.db.GetSSTPSettings()
	if err != nil {
		settings = &models.SSTPSettings{Enabled: true, Port: 4444, GatewayIP: "10.10.10.1", IPPool: "10.10.10.2-254"}
	}

	clients, err := bp.db.GetSSTPClients()
	if err != nil {
		clients = []models.SSTPClient{}
	}

	// Fetch active sessions from accel-cmd
	var activeSessions []SessionInfo
	out, err := exec.Command("accel-cmd", "show", "sessions").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) >= 8 {
				ifname := strings.TrimSpace(parts[0])
				if ifname == "ifname" || strings.HasPrefix(ifname, "-") {
					continue
				}
				s := SessionInfo{
					IfName:    ifname,
					Username:  strings.TrimSpace(parts[1]),
					CallingIP: strings.TrimSpace(parts[2]),
					IP:        strings.TrimSpace(parts[3]),
					Type:      strings.TrimSpace(parts[4]),
					State:     strings.TrimSpace(parts[6]),
					Uptime:    strings.TrimSpace(parts[7]),
				}
				activeSessions = append(activeSessions, s)
			}
		}
	}

	// Mark clients online if session exists
	onlineCount := 0
	for i := range clients {
		for _, s := range activeSessions {
			if strings.EqualFold(clients[i].Username, s.Username) {
				clients[i].IsOnline = true
				onlineCount++
				break
			}
		}
	}

	serverHost := strings.Split(r.Host, ":")[0]
	if serverHost == "" || serverHost == "localhost" || serverHost == "127.0.0.1" {
		serverHost = "192.168.2.90"
	}

	// Suggested random client credentials
	randUser := fmt.Sprintf("mk-%s", randomAlpha(6))
	randPass := randomAlpha(8)

	_ = bp.renderer.Render(w, "sstp/index", map[string]interface{}{
		"Title":          "إدارة سيرفر SSTP VPN للمايكروتك",
		"ActiveMenu":     "sstp",
		"CurrentUser":    middleware.GetUser(r),
		"Settings":       settings,
		"Clients":        clients,
		"ActiveSessions": activeSessions,
		"OnlineCount":    onlineCount,
		"ServerHost":     serverHost,
		"RandUser":       randUser,
		"RandPass":       randPass,
		"Message":        r.URL.Query().Get("msg"),
		"Error":          r.URL.Query().Get("err"),
	})
}

func (bp *Blueprint) SaveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sstp", http.StatusFound)
		return
	}

	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"
	port, _ := strconv.Atoi(r.FormValue("port"))
	if port <= 0 {
		port = 4444
	}
	gatewayIP := strings.TrimSpace(r.FormValue("gateway_ip"))
	if gatewayIP == "" {
		gatewayIP = "10.10.10.1"
	}
	ipPool := strings.TrimSpace(r.FormValue("ip_pool"))
	if ipPool == "" {
		ipPool = "10.10.10.2-254"
	}

	settings := &models.SSTPSettings{
		Enabled:   enabled,
		Port:      port,
		GatewayIP: gatewayIP,
		IPPool:    ipPool,
	}

	_ = bp.db.SaveSSTPSettings(settings)
	_ = bp.db.SyncSSTPFiles()

	http.Redirect(w, r, "/sstp?msg=settings_saved", http.StatusFound)
}

func (bp *Blueprint) AddClientHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sstp", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))
	assignedIP := strings.TrimSpace(r.FormValue("assigned_ip"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if name == "" {
		name = "راوتر مايكروتك"
	}
	if username == "" {
		username = fmt.Sprintf("mk-%s", randomAlpha(6))
	}
	if password == "" {
		password = randomAlpha(8)
	}

	client := &models.SSTPClient{
		Name:       name,
		Username:   username,
		Password:   password,
		AssignedIP: assignedIP,
		Notes:      notes,
		Enabled:    true,
	}

	if err := bp.db.SaveSSTPClient(client); err != nil {
		http.Redirect(w, r, "/sstp?err=user_exists", http.StatusFound)
		return
	}

	_ = bp.db.SyncSSTPFiles()
	http.Redirect(w, r, "/sstp?msg=client_added", http.StatusFound)
}

func (bp *Blueprint) DeleteClientHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id > 0 {
		_ = bp.db.DeleteSSTPClient(id)
		_ = bp.db.SyncSSTPFiles()
	}
	http.Redirect(w, r, "/sstp?msg=client_deleted", http.StatusFound)
}

func randomAlpha(n int) string {
	const chars = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, n)
	rand.Seed(time.Now().UnixNano())
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
