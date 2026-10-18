package reports

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

func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("q")
	query := `SELECT radacctid, acctsessionid, username, nasipaddress, acctstarttime, acctstoptime,
		acctsessiontime, acctinputoctets, acctoutputoctets, framedipaddress, acctterminatecause
		FROM radacct`

	var args []interface{}
	if search != "" {
		query += " WHERE username LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY radacctid DESC LIMIT 100"

	rows, err := bp.db.Query(query, args...)
	var logs []models.RadAcct
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var l models.RadAcct
			if err := rows.Scan(&l.RadAcctID, &l.AcctSessionID, &l.UserName, &l.NASIPAddress,
				&l.AcctStartTime, &l.AcctStopTime, &l.AcctSessionTime, &l.AcctInputOctets,
				&l.AcctOutputOctets, &l.FramedIPAddress, &l.AcctTerminateCause); err == nil {
				logs = append(logs, l)
			}
		}
	}

	_ = bp.renderer.Render(w, "reports/index", map[string]interface{}{
		"Title":       "سجلات المحاسبة والاستهلاك (radacct)",
		"ActiveMenu":  "reports",
		"CurrentUser": middleware.GetUser(r),
		"Logs":        logs,
		"SearchQuery": search,
	})
}
