package routers

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"

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

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := bp.db.Query(`SELECT id, nasname, COALESCE(shortname, nasname), COALESCE(type, 'mikrotik'), secret,
		COALESCE(description, ''), COALESCE(coa_port, 3799)
		FROM nas ORDER BY id ASC`)

	var routers []models.NAS
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n models.NAS
			var shortName, nasType, desc sql.NullString
			var coaPort sql.NullInt64
			if err := rows.Scan(&n.ID, &n.NASName, &shortName, &nasType, &n.Secret, &desc, &coaPort); err == nil {
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
				routers = append(routers, n)
			} else {
				log.Printf("[Routers] Scan error: %v", err)
			}
		}
	} else {
		log.Printf("[Routers] Query error: %v", err)
		// Fallback query without coa_port
		fallbackRows, fErr := bp.db.Query("SELECT id, nasname, COALESCE(shortname, nasname), COALESCE(type, 'mikrotik'), secret, COALESCE(description, '') FROM nas ORDER BY id ASC")
		if fErr == nil {
			defer fallbackRows.Close()
			for fallbackRows.Next() {
				var n models.NAS
				var shortName, nasType, desc sql.NullString
				if err := fallbackRows.Scan(&n.ID, &n.NASName, &shortName, &nasType, &n.Secret, &desc); err == nil {
					n.ShortName = shortName.String
					n.Type = nasType.String
					n.Description = desc.String
					n.CoAPort = 3799
					routers = append(routers, n)
				}
			}
		}
	}

	_ = bp.renderer.Render(w, "routers/list", map[string]interface{}{
		"Title":       "راوترات المايكروتك (NAS)",
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
		description := strings.TrimSpace(r.FormValue("description"))

		if coaPort <= 0 {
			coaPort = 3799
		}
		if shortname == "" {
			shortname = nasname
		}
		if secret == "" {
			secret = "radius123"
		}

		if nasname != "" {
			// First try inserting with coa_port
			_, err := bp.db.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port)
				VALUES (?, ?, 'mikrotik', ?, ?, ?)`, nasname, shortname, secret, description, coaPort)
			if err != nil {
				log.Printf("[Routers] Insert with coa_port failed: %v, attempting standard insert", err)
				_, err2 := bp.db.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description)
					VALUES (?, ?, 'mikrotik', ?, ?)`, nasname, shortname, secret, description)
				if err2 != nil {
					log.Printf("[Routers] Fallback insert failed: %v", err2)
				}
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
