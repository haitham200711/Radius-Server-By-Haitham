package dashboard

import (
	"fmt"
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
	user := middleware.GetUser(r)
	metrics, _ := bp.db.GetMetrics()

	rows, err := bp.db.Query(`SELECT radacctid, acctsessionid, username, nasipaddress, acctstarttime, acctsessiontime,
		acctinputoctets, acctoutputoctets, framedipaddress, callingstationid
		FROM radacct WHERE acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 10`)

	var recentSessions []models.RadAcct
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s models.RadAcct
			if err := rows.Scan(&s.RadAcctID, &s.AcctSessionID, &s.UserName, &s.NASIPAddress, &s.AcctStartTime,
				&s.AcctSessionTime, &s.AcctInputOctets, &s.AcctOutputOctets, &s.FramedIPAddress, &s.CallingStationID); err == nil {
				recentSessions = append(recentSessions, s)
			}
		}
	}

	bRows, err := bp.db.Query(`SELECT b.id, b.batch_code, p.display_name, b.count, b.total_price, b.created_at
		FROM batches b LEFT JOIN profiles p ON b.profile_id = p.id ORDER BY b.id DESC LIMIT 5`)

	type BatchSummary struct {
		ID          int64
		BatchCode   string
		ProfileName string
		Count       int
		TotalPrice  float64
		CreatedAt   string
	}
	var recentBatches []BatchSummary
	if err == nil {
		defer bRows.Close()
		for bRows.Next() {
			var b BatchSummary
			var createdAt interface{}
			if err := bRows.Scan(&b.ID, &b.BatchCode, &b.ProfileName, &b.Count, &b.TotalPrice, &createdAt); err == nil {
				b.CreatedAt = fmt.Sprintf("%v", createdAt)
				recentBatches = append(recentBatches, b)
			}
		}
	}

	_ = bp.renderer.Render(w, "dashboard/index", map[string]interface{}{
		"Title":          "لوحة التحكم الإحصائية",
		"ActiveMenu":     "dashboard",
		"CurrentUser":    user,
		"Metrics":        metrics,
		"RecentSessions": recentSessions,
		"RecentBatches":  recentBatches,
	})
}
