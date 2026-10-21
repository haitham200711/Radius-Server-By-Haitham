package sessions

import (
	"net"
	"net/http"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

func New(db *database.DB, r *web.Renderer) *Blueprint {
	return &Blueprint{db: db, renderer: r}
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := bp.db.Query(`SELECT radacctid, acctsessionid, username, nasipaddress, acctstarttime, acctsessiontime,
		acctinputoctets, acctoutputoctets, framedipaddress, callingstationid
		FROM radacct WHERE acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 150`)

	var sessions []models.RadAcct
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s models.RadAcct
			if err := rows.Scan(&s.RadAcctID, &s.AcctSessionID, &s.UserName, &s.NASIPAddress, &s.AcctStartTime,
				&s.AcctSessionTime, &s.AcctInputOctets, &s.AcctOutputOctets, &s.FramedIPAddress, &s.CallingStationID); err == nil {
				sessions = append(sessions, s)
			}
		}
	}

	_ = bp.renderer.Render(w, r, "sessions/list", map[string]interface{}{
		"Title":       "المتصلين الآن (Live Sessions & CoA)",
		"ActiveMenu":  "sessions",
		"CurrentUser": middleware.GetUser(r),
		"Sessions":    sessions,
	})
}

func (bp *Blueprint) DisconnectHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	sessionID := r.URL.Query().Get("session")
	nasIP := r.URL.Query().Get("nas")
	framedIPStr := r.URL.Query().Get("ip")

	var secret string
	var coaPort int
	_ = bp.db.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", nasIP).Scan(&secret, &coaPort)
	if secret == "" {
		secret = "radius123"
	}
	if coaPort <= 0 {
		coaPort = 3799
	}

	var framedIP net.IP
	if framedIPStr != "" {
		framedIP = net.ParseIP(framedIPStr)
	}

	_, _ = radius.SendDisconnectRequest(nasIP, coaPort, []byte(secret), username, sessionID, framedIP)

	_, _ = bp.db.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Reset'
		WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, sessionID, nasIP)

	http.Redirect(w, r, "/sessions", http.StatusFound)
}
