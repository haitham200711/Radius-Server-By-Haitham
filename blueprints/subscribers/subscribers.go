package subscribers

import (
	"net/http"

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

type SubscriberItem struct {
	Username  string
	Password  string
	Profile   string
	IPAddress string
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("q")
	query := `SELECT rc.username, rc.value, COALESCE(rug.groupname, ''), COALESCE(rr.value, '')
		FROM radcheck rc
		LEFT JOIN radusergroup rug ON rc.username = rug.username
		LEFT JOIN radreply rr ON rc.username = rr.username AND rr.attribute = 'Framed-IP-Address'
		WHERE rc.attribute = 'Cleartext-Password'`

	var args []interface{}
	if search != "" {
		query += " AND rc.username LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY rc.id DESC LIMIT 100"

	rows, err := bp.db.Query(query, args...)
	var list []SubscriberItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s SubscriberItem
			if err := rows.Scan(&s.Username, &s.Password, &s.Profile, &s.IPAddress); err == nil {
				list = append(list, s)
			}
		}
	}

	_ = bp.renderer.Render(w, "subscribers/list", map[string]interface{}{
		"Title":       "إدارة المشتركين",
		"ActiveMenu":  "subscribers",
		"CurrentUser": middleware.GetUser(r),
		"Subscribers": list,
		"SearchQuery": search,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pRows, _ := bp.db.Query("SELECT id, name, display_name FROM profiles ORDER BY id ASC")
		var profiles []models.Profile
		if pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p models.Profile
				_ = pRows.Scan(&p.ID, &p.Name, &p.DisplayName)
				profiles = append(profiles, p)
			}
		}

		_ = bp.renderer.Render(w, "subscribers/add", map[string]interface{}{
			"Title":       "إضافة مشترك جديد",
			"ActiveMenu":  "subscribers",
			"CurrentUser": middleware.GetUser(r),
			"Profiles":    profiles,
		})
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	profile := r.FormValue("profile")
	staticIP := r.FormValue("static_ip")
	callingMAC := r.FormValue("calling_mac")

	if username != "" && password != "" {
		_ = bp.db.AddSubscriber(username, password, profile, staticIP, callingMAC)
	}

	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username != "" {
		_ = bp.db.DeleteSubscriber(username)
	}
	http.Redirect(w, r, "/subscribers", http.StatusFound)
}
