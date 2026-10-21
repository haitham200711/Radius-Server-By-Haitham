package sstp

import (
	"encoding/json"
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
	bp := &Blueprint{db: db, renderer: r}
	go bp.startRouteWatcher()
	return bp
}

func (bp *Blueprint) startRouteWatcher() {
	// Periodic sync every 5 seconds ensures routes apply as soon as clients connect
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		bp.SyncAllRoutes()
	}
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

func (bp *Blueprint) getActiveSessions() ([]SessionInfo, map[string]string) {
	var activeSessions []SessionInfo
	ipToIf := make(map[string]string)

	out, err := exec.Command("accel-cmd", "show", "sessions").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) >= 8 {
				ifname := strings.TrimSpace(parts[0])
				if ifname == "ifname" || strings.HasPrefix(ifname, "-") || ifname == "" {
					continue
				}
				ip := strings.TrimSpace(parts[3])
				s := SessionInfo{
					IfName:    ifname,
					Username:  strings.TrimSpace(parts[1]),
					CallingIP: strings.TrimSpace(parts[2]),
					IP:        ip,
					Type:      strings.TrimSpace(parts[4]),
					State:     strings.TrimSpace(parts[6]),
					Uptime:    strings.TrimSpace(parts[7]),
				}
				activeSessions = append(activeSessions, s)
				if ip != "" {
					ipToIf[ip] = ifname
				}
			}
		}
	}
	return activeSessions, ipToIf
}

func (bp *Blueprint) SyncAllRoutes() {
	routes, err := bp.db.GetSSTPStaticRoutes()
	if err != nil {
		return
	}

	_, activeMap := bp.getActiveSessions()
	kernelRoutesOut, _ := exec.Command("ip", "route", "show").Output()
	kernelRoutesStr := string(kernelRoutesOut)

	for _, r := range routes {
		dest := strings.TrimSpace(r.DestinationCIDR)
		gw := strings.TrimSpace(r.GatewayIP)
		if dest == "" || gw == "" {
			continue
		}

		if !r.Enabled {
			if strings.Contains(kernelRoutesStr, dest) {
				_ = exec.Command("ip", "route", "del", dest).Run()
			}
			_ = bp.db.SetSSTPRouteApplied(r.ID, false)
			continue
		}

		// If enabled: try to apply/replace route
		applied := false
		if ifname, ok := activeMap[gw]; ok && ifname != "" {
			err := exec.Command("ip", "route", "replace", dest, "via", gw, "dev", ifname).Run()
			if err == nil {
				applied = true
			}
		}

		if !applied {
			err := exec.Command("ip", "route", "replace", dest, "via", gw).Run()
			if err == nil {
				applied = true
			}
		}

		if applied {
			_ = bp.db.SetSSTPRouteApplied(r.ID, true)
		} else {
			_ = bp.db.SetSSTPRouteApplied(r.ID, false)
		}
	}
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
	activeSessions, activeMap := bp.getActiveSessions()

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

	// Fetch static routes and check kernel status
	staticRoutes, _ := bp.db.GetSSTPStaticRoutes()
	for i := range staticRoutes {
		if ifname, ok := activeMap[staticRoutes[i].GatewayIP]; ok {
			staticRoutes[i].ActiveDev = ifname
		}
	}

	serverHost := strings.Split(r.Host, ":")[0]
	if serverHost == "" || serverHost == "localhost" || serverHost == "127.0.0.1" {
		serverHost = "192.168.2.90"
	}

	// Suggested random client credentials
	randUser := fmt.Sprintf("mk-%s", randomAlpha(6))
	randPass := randomAlpha(8)
	nextIP := bp.db.GetNextSequentialSSTPIP()

	_ = bp.renderer.Render(w, r, "sstp/index", map[string]interface{}{
		"Title":          "إدارة سيرفر SSTP VPN للمايكروتك",
		"ActiveMenu":     "sstp",
		"CurrentUser":    middleware.GetUser(r),
		"Settings":       settings,
		"Clients":        clients,
		"StaticRoutes":   staticRoutes,
		"ActiveSessions": activeSessions,
		"OnlineCount":    onlineCount,
		"ServerHost":     serverHost,
		"RandUser":       randUser,
		"RandPass":       randPass,
		"NextIP":         nextIP,
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
	if assignedIP == "" {
		assignedIP = bp.db.GetNextSequentialSSTPIP()
	}
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
	if assignedIP == "" {
		assignedIP = bp.db.GetNextSequentialSSTPIP()
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

func (bp *Blueprint) EditClientHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sstp", http.StatusFound)
		return
	}

	idStr := strings.TrimSpace(r.FormValue("id"))
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		http.Redirect(w, r, "/sstp?err=invalid_id", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "راوتر مايكروتك"
	}
	assignedIP := strings.TrimSpace(r.FormValue("assigned_ip"))
	if assignedIP == "" {
		assignedIP = bp.db.GetNextSequentialSSTPIP()
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"

	if err := bp.db.UpdateSSTPClient(id, name, assignedIP, notes, enabled); err != nil {
		http.Redirect(w, r, "/sstp?err=update_failed", http.StatusFound)
		return
	}

	_ = bp.db.SyncSSTPFiles()
	http.Redirect(w, r, "/sstp?msg=client_updated", http.StatusFound)
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

func (bp *Blueprint) generateRSCScript(serverHost string, sstpPort int, gatewayIP, username, password string) string {
	if sstpPort <= 0 {
		sstpPort = 4444
	}
	if gatewayIP == "" {
		gatewayIP = "10.10.10.1"
	}
	return fmt.Sprintf(`# ========================================================
# Haitham RADIUS Cloud - Automated SSTP VPN Setup Script
# Target: MikroTik RouterOS v6 / v7
# Generated: %s
# Connection Name: Radius-Pro
# Profile Name:    Radius-VPN
# Gateway Watchdog: %s
# ========================================================

:log info "[RADIUS-SETUP] Starting automated VPN configuration..."

# 1. Clean previous setup if already present (Idempotency)
:do { /interface sstp-client remove [find name="Radius-Pro"] } on-error={}
:do { /ppp profile remove [find name="Radius-VPN"] } on-error={}
:do { /tool netwatch remove [find comment="SSTP-Radius-Pro-Watchdog"] } on-error={}

# 2. Create PPP Profile (Radius-VPN)
/ppp profile add name=Radius-VPN use-encryption=yes comment="Haitham RADIUS VPN Profile"
:log info "[RADIUS-SETUP] Profile Radius-VPN created successfully."

# 3. Create SSTP Client Connection (Radius-Pro)
/interface sstp-client add name=Radius-Pro connect-to=%s:%d user="%s" password="%s" profile=Radius-VPN authentication=mschap2,chap,pap verify-server-certificate=no disabled=no comment="Haitham RADIUS SSTP Tunnel"
:log info "[RADIUS-SETUP] Connection Radius-Pro created and enabled."

:log info "[RADIUS-SETUP] Configuration completed successfully!"
`, time.Now().Format("2006-01-02 15:04:05"), serverHost, sstpPort, username, password)
}

func (bp *Blueprint) GenerateTokenHandler(w http.ResponseWriter, r *http.Request) {
	clientID, _ := strconv.ParseInt(r.URL.Query().Get("client_id"), 10, 64)
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	password := strings.TrimSpace(r.URL.Query().Get("password"))

	if clientID > 0 {
		client, err := bp.db.GetSSTPClientByID(clientID)
		if err == nil && client != nil {
			username = client.Username
			password = client.Password
		}
	}

	if username == "" || password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing client credentials"})
		return
	}

	token, err := bp.db.CreateSSTPToken(clientID, username, password)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	serverHost := strings.Split(r.Host, ":")[0]
	if serverHost == "" || serverHost == "localhost" || serverHost == "127.0.0.1" {
		serverHost = "192.168.2.90"
	}

	settings, err := bp.db.GetSSTPSettings()
	if err != nil || settings == nil {
		settings = &models.SSTPSettings{Enabled: true, Port: 4444, GatewayIP: "10.10.10.1", IPPool: "10.10.10.2-254"}
	}

	fetchURL := fmt.Sprintf("http://%s/sstp/script?token=%s", serverHost, token)
	fetchCmd := fmt.Sprintf(`/tool fetch url="%s" mode=http dst-path=Radius-Pro.rsc; :delay 2s; /import Radius-Pro.rsc; /file remove Radius-Pro.rsc`, fetchURL)
	rscScript := bp.generateRSCScript(serverHost, settings.Port, settings.GatewayIP, username, password)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "success",
		"token":         token,
		"url":           fetchURL,
		"fetch_command": fetchCmd,
		"rsc_content":   rscScript,
		"username":      username,
		"gateway_ip":    settings.GatewayIP,
		"port":          settings.Port,
	})
}

func (bp *Blueprint) ScriptHandler(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "# ERROR: Missing one-time token.")
		return
	}

	_, username, password, err := bp.db.ConsumeSSTPToken(token)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, `# ========================================================
# ERROR: This one-time setup link has already been used or expired!
# Please generate a new one-time link from the RADIUS Dashboard.
# ========================================================
:log error "[RADIUS-SETUP] Token '%s' is already used or expired! Please generate a new link from the dashboard."
`, token)
		return
	}

	serverHost := strings.Split(r.Host, ":")[0]
	if serverHost == "" || serverHost == "localhost" || serverHost == "127.0.0.1" {
		serverHost = "192.168.2.90"
	}

	settings, err := bp.db.GetSSTPSettings()
	if err != nil || settings == nil {
		settings = &models.SSTPSettings{Enabled: true, Port: 4444, GatewayIP: "10.10.10.1", IPPool: "10.10.10.2-254"}
	}

	rscScript := bp.generateRSCScript(serverHost, settings.Port, settings.GatewayIP, username, password)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"Radius-Pro.rsc\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rscScript))
}

func (bp *Blueprint) AddRouteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sstp", http.StatusFound)
		return
	}

	dest := strings.TrimSpace(r.FormValue("destination_cidr"))
	gw := strings.TrimSpace(r.FormValue("gateway_ip"))
	clientID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("client_id")), 10, 64)
	desc := strings.TrimSpace(r.FormValue("description"))
	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"

	if dest == "" || gw == "" {
		http.Redirect(w, r, "/sstp?err=missing_route_fields", http.StatusFound)
		return
	}

	sr := &models.SSTPStaticRoute{
		DestinationCIDR: dest,
		GatewayIP:       gw,
		ClientID:        clientID,
		Description:     desc,
		Enabled:         enabled,
	}

	if err := bp.db.SaveSSTPStaticRoute(sr); err != nil {
		http.Redirect(w, r, "/sstp?err=save_route_failed", http.StatusFound)
		return
	}

	bp.SyncAllRoutes()
	http.Redirect(w, r, "/sstp?msg=route_added", http.StatusFound)
}

func (bp *Blueprint) EditRouteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sstp", http.StatusFound)
		return
	}

	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	dest := strings.TrimSpace(r.FormValue("destination_cidr"))
	gw := strings.TrimSpace(r.FormValue("gateway_ip"))
	clientID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("client_id")), 10, 64)
	desc := strings.TrimSpace(r.FormValue("description"))
	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"

	if id <= 0 || dest == "" || gw == "" {
		http.Redirect(w, r, "/sstp?err=missing_route_fields", http.StatusFound)
		return
	}

	sr := &models.SSTPStaticRoute{
		ID:              id,
		DestinationCIDR: dest,
		GatewayIP:       gw,
		ClientID:        clientID,
		Description:     desc,
		Enabled:         enabled,
	}

	if err := bp.db.SaveSSTPStaticRoute(sr); err != nil {
		http.Redirect(w, r, "/sstp?err=save_route_failed", http.StatusFound)
		return
	}

	bp.SyncAllRoutes()
	http.Redirect(w, r, "/sstp?msg=route_updated", http.StatusFound)
}

func (bp *Blueprint) DeleteRouteHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id > 0 {
		sr, err := bp.db.DeleteSSTPStaticRoute(id)
		if err == nil && sr != nil {
			_ = exec.Command("ip", "route", "del", sr.DestinationCIDR).Run()
		}
	}
	bp.SyncAllRoutes()
	http.Redirect(w, r, "/sstp?msg=route_deleted", http.StatusFound)
}

func (bp *Blueprint) ApplyRoutesHandler(w http.ResponseWriter, r *http.Request) {
	bp.SyncAllRoutes()
	http.Redirect(w, r, "/sstp?msg=routes_applied", http.StatusFound)
}

func (bp *Blueprint) HookUpHandler(w http.ResponseWriter, r *http.Request) {
	bp.SyncAllRoutes()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
