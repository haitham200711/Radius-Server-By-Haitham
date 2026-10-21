package routers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/mikrotik"
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
	rows, err := bp.db.Query(`SELECT id, nasname, COALESCE(shortname, nasname), COALESCE(type, 'mikrotik'), secret,
		COALESCE(description, ''), COALESCE(coa_port, 3799),
		COALESCE(api_port, 8728), COALESCE(api_user, 'admin'), COALESCE(api_password, '')
		FROM nas ORDER BY id ASC`)

	var routers []models.NAS
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n models.NAS
			var shortName, nasType, desc, apiUser, apiPass sql.NullString
			var coaPort, apiPort sql.NullInt64
			if err := rows.Scan(&n.ID, &n.NASName, &shortName, &nasType, &n.Secret, &desc, &coaPort, &apiPort, &apiUser, &apiPass); err == nil {
				if shortName.Valid && shortName.String != "" {
					n.ShortName = shortName.String
				} else {
					n.ShortName = n.NASName
				}
				if nasType.Valid && nasType.String != "" {
					n.Type = nasType.String
				} else {
					n.Type = "mikrotik"
				}
				if desc.Valid {
					n.Description = desc.String
				}
				if coaPort.Valid && coaPort.Int64 > 0 {
					n.CoAPort = int(coaPort.Int64)
				} else {
					n.CoAPort = 3799
				}
				if apiPort.Valid && apiPort.Int64 > 0 {
					n.APIPort = int(apiPort.Int64)
				} else {
					n.APIPort = 8728
				}
				if apiUser.Valid && apiUser.String != "" {
					n.APIUser = apiUser.String
				} else {
					n.APIUser = "admin"
				}
				if apiPass.Valid {
					n.APIPassword = apiPass.String
				}
				routers = append(routers, n)
			} else {
				log.Printf("[Routers] Scan error: %v", err)
			}
		}
	} else {
		log.Printf("[Routers] Query error: %v, attempting fallback query", err)
		// Fallback query without api fields
		fallbackRows, fErr := bp.db.Query("SELECT id, nasname, COALESCE(shortname, nasname), COALESCE(type, 'mikrotik'), secret, COALESCE(description, ''), COALESCE(coa_port, 3799) FROM nas ORDER BY id ASC")
		if fErr == nil {
			defer fallbackRows.Close()
			for fallbackRows.Next() {
				var n models.NAS
				var shortName, nasType, desc sql.NullString
				var coaPort sql.NullInt64
				if err := fallbackRows.Scan(&n.ID, &n.NASName, &shortName, &nasType, &n.Secret, &desc, &coaPort); err == nil {
					n.ShortName = shortName.String
					n.Type = nasType.String
					n.Description = desc.String
					n.CoAPort = 3799
					n.APIPort = 8728
					n.APIUser = "admin"
					routers = append(routers, n)
				}
			}
		}
	}

	_ = bp.renderer.Render(w, r, "routers/list", map[string]interface{}{
		"Title":       "راوترات وسيرفرات المايكروتك (NAS)",
		"ActiveMenu":  "routers",
		"CurrentUser": middleware.GetUser(r),
		"Routers":     routers,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		nasname := strings.TrimSpace(r.FormValue("nasname"))
		shortname := strings.TrimSpace(r.FormValue("shortname"))
		secret := strings.TrimSpace(r.FormValue("secret"))
		coaPort, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("coa_port")))
		apiPort, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("api_port")))
		apiUser := strings.TrimSpace(r.FormValue("api_user"))
		apiPassword := strings.TrimSpace(r.FormValue("api_password"))
		description := strings.TrimSpace(r.FormValue("description"))

		if coaPort <= 0 {
			coaPort = 3799
		}
		if apiPort <= 0 {
			apiPort = 8728
		}
		if apiUser == "" {
			apiUser = "admin"
		}
		if shortname == "" {
			shortname = nasname
		}
		if secret == "" {
			secret = "radius123"
		}

		if nasname != "" {
			// First try inserting with full API & CoA fields
			_, err := bp.db.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port, api_port, api_user, api_password)
				VALUES (?, ?, 'mikrotik', ?, ?, ?, ?, ?, ?)`, nasname, shortname, secret, description, coaPort, apiPort, apiUser, apiPassword)
			if err != nil {
				log.Printf("[Routers] Insert with api fields failed: %v, attempting standard insert", err)
				_, err2 := bp.db.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port)
					VALUES (?, ?, 'mikrotik', ?, ?, ?)`, nasname, shortname, secret, description, coaPort)
				if err2 != nil {
					log.Printf("[Routers] Fallback insert failed: %v", err2)
				}
			}
		}
	}
	http.Redirect(w, r, "/routers", http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
		nasname := strings.TrimSpace(r.FormValue("nasname"))
		shortname := strings.TrimSpace(r.FormValue("shortname"))
		secret := strings.TrimSpace(r.FormValue("secret"))
		coaPort, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("coa_port")))
		apiPort, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("api_port")))
		apiUser := strings.TrimSpace(r.FormValue("api_user"))
		apiPassword := strings.TrimSpace(r.FormValue("api_password"))
		description := strings.TrimSpace(r.FormValue("description"))

		if coaPort <= 0 {
			coaPort = 3799
		}
		if apiPort <= 0 {
			apiPort = 8728
		}
		if apiUser == "" {
			apiUser = "admin"
		}
		if shortname == "" {
			shortname = nasname
		}
		if secret == "" {
			secret = "radius123"
		}

		if id > 0 && nasname != "" {
			_, err := bp.db.Exec(`UPDATE nas SET
				nasname = ?, shortname = ?, secret = ?, description = ?,
				coa_port = ?, api_port = ?, api_user = ?, api_password = ?
				WHERE id = ?`, nasname, shortname, secret, description, coaPort, apiPort, apiUser, apiPassword, id)
			if err != nil {
				log.Printf("[Routers] Update NAS id=%d error: %v", id, err)
			}
		}
	}
	http.Redirect(w, r, "/routers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id != "" {
		_, err := bp.db.Exec("DELETE FROM nas WHERE id = ?", id)
		if err != nil {
			log.Printf("[Routers] Delete NAS id=%s error: %v", id, err)
		}
	}
	http.Redirect(w, r, "/routers", http.StatusFound)
}

// TestAPIHandler tests connection to MikroTik RouterOS API and returns router diagnostics as JSON
func (bp *Blueprint) TestAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	var (
		nasIP   string
		apiPort int    = 8728
		apiUser string = "admin"
		apiPass string
	)

	// Check if router ID passed
	idStr := strings.TrimSpace(r.FormValue("id"))
	if idStr == "" {
		idStr = strings.TrimSpace(r.URL.Query().Get("id"))
	}

	if idStr != "" {
		var port sql.NullInt64
		var u, p sql.NullString
		err := bp.db.QueryRow("SELECT nasname, api_port, api_user, api_password FROM nas WHERE id = ?", idStr).Scan(&nasIP, &port, &u, &p)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "لم يتم العثور على الراوتر المحدد",
			})
			return
		}
		if port.Valid && port.Int64 > 0 {
			apiPort = int(port.Int64)
		}
		if u.Valid && u.String != "" {
			apiUser = u.String
		}
		if p.Valid {
			apiPass = p.String
		}
	} else {
		// Use directly submitted form parameters
		nasIP = strings.TrimSpace(r.FormValue("nasname"))
		if portVal, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("api_port"))); portVal > 0 {
			apiPort = portVal
		}
		if userVal := strings.TrimSpace(r.FormValue("api_user")); userVal != "" {
			apiUser = userVal
		}
		apiPass = strings.TrimSpace(r.FormValue("api_password"))
	}

	if nasIP == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "عنوان الـ IP للراوتر مطلوب",
		})
		return
	}

	addr := fmt.Sprintf("%s:%d", nasIP, apiPort)
	client, err := mikrotik.Connect(addr, apiUser, apiPass, 2500*time.Millisecond)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("فشل الاتصال بـ API المايكروتك (%s): %v", addr, err),
		})
		return
	}
	defer client.Close()

	version, board, cpu, uptime, err := client.TestConnection()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("الاتصال نجح ولكن تعذر قراءة معلومات النظام: %v", err),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "تم الاتصال بـ RouterOS API بنجاح تام!",
		"data": map[string]interface{}{
			"target":   addr,
			"version":  version,
			"board":    board,
			"cpu_load": cpu,
			"uptime":   uptime,
		},
	})
}
