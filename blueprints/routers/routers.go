package routers

import (
	"net/http"
	"strconv"

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
	rows, err := bp.db.Query("SELECT id, nasname, shortname, type, secret, description, coa_port FROM nas ORDER BY id ASC")
	var routers []models.NAS
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n models.NAS
			if err := rows.Scan(&n.ID, &n.NASName, &n.ShortName, &n.Type, &n.Secret, &n.Description, &n.CoAPort); err == nil {
				routers = append(routers, n)
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
		nasname := r.FormValue("nasname")
		shortname := r.FormValue("shortname")
		secret := r.FormValue("secret")
		coaPort, _ := strconv.Atoi(r.FormValue("coa_port"))
		description := r.FormValue("description")

		if coaPort <= 0 {
			coaPort = 3799
		}

		if nasname != "" && secret != "" {
			_, _ = bp.db.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port)
				VALUES (?, ?, 'mikrotik', ?, ?, ?)`, nasname, shortname, secret, description, coaPort)
		}
	}
	http.Redirect(w, r, "/routers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id != "" {
		_, _ = bp.db.Exec("DELETE FROM nas WHERE id = ?", id)
	}
	http.Redirect(w, r, "/routers", http.StatusFound)
}
