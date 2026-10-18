package settings

import (
	"fmt"
	"net/http"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

func New(db *database.DB, r *web.Renderer) *Blueprint {
	return &Blueprint{db: db, renderer: r}
}

func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	serverIP := r.URL.Query().Get("server_ip")
	if serverIP == "" {
		serverIP = "192.168.2.90"
	}
	secret := r.URL.Query().Get("secret")
	if secret == "" {
		secret = "radius123"
	}

	mikrotikScript := fmt.Sprintf(`# ========================================================
# MikroTik RouterOS RADIUS Setup Script
# Server: %s | Secret: %s
# ========================================================

# 1. Enable RADIUS for PPP (PPPoE / PPTP / L2TP / SSTP / OVPN)
/ppp aaa set use-radius=yes interim-update=2m accounting=yes

# 2. Enable RADIUS for Hotspot
/ip hotspot profile set [find default=yes] use-radius=yes radius-interim-update=2m

# 3. Enable CoA (Incoming Disconnect & Rate-Limit modification)
/radius incoming set accept=yes port=3799

# 4. Remove previous Radius configuration if exists
/radius remove [find address="%s"]

# 5. Add RADIUS Server
/radius add address=%s secret="%s" service=ppp,hotspot,login authentication-port=1812 accounting-port=1813 timeout=3s comment="Radius-Server-By-Haitham"

:put ">>> RADIUS Configuration Applied Successfully! <<<"
`, serverIP, secret, serverIP, serverIP, secret)

	_ = bp.renderer.Render(w, "settings/index", map[string]interface{}{
		"Title":          "إعدادات النظام والربط",
		"ActiveMenu":     "settings",
		"CurrentUser":    middleware.GetUser(r),
		"ServerIP":       serverIP,
		"Secret":         secret,
		"MikrotikScript": mikrotikScript,
	})
}
