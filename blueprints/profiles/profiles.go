package profiles

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
	rows, err := bp.db.Query("SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds, price, created_at FROM profiles ORDER BY id ASC")
	var profiles []models.Profile
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p models.Profile
			if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds, &p.Price, &p.CreatedAt); err == nil {
				profiles = append(profiles, p)
			}
		}
	}

	_ = bp.renderer.Render(w, "profiles/list", map[string]interface{}{
		"Title":       "باقات السرعات والخدمات",
		"ActiveMenu":  "profiles",
		"CurrentUser": middleware.GetUser(r),
		"Profiles":    profiles,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		displayName := r.FormValue("display_name")
		rateLimit := r.FormValue("rate_limit")
		poolName := r.FormValue("pool_name")
		price, _ := strconv.ParseFloat(r.FormValue("price"), 64)

		if name != "" && rateLimit != "" {
			_, err := bp.db.Exec(`INSERT INTO profiles (name, display_name, rate_limit, pool_name, price)
				VALUES (?, ?, ?, ?, ?)`, name, displayName, rateLimit, poolName, price)
			if err == nil {
				_, _ = bp.db.Exec(`INSERT INTO radgroupreply (groupname, attribute, op, value)
					VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)`, name, rateLimit)
			}
		}
	}
	http.Redirect(w, r, "/profiles", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var name string
	_ = bp.db.QueryRow("SELECT name FROM profiles WHERE id = ?", id).Scan(&name)
	if name != "" {
		_, _ = bp.db.Exec("DELETE FROM profiles WHERE id = ?", id)
		_, _ = bp.db.Exec("DELETE FROM radgroupreply WHERE groupname = ?", name)
	}
	http.Redirect(w, r, "/profiles", http.StatusFound)
}
