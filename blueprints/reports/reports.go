package reports

import (
	"net/http"
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

func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		tab = "billing" // Default to Financial/Billing Reports & Debts
	}

	search := strings.TrimSpace(r.URL.Query().Get("q"))
	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	// 1. Financial & Billing Transactions
	billingList, totalBilling, _ := bp.db.GetBillingTransactions(search, typeFilter, statusFilter, 200, 0)
	billingSummary, _ := bp.db.GetBillingFinancialSummary()

	// 2. RADIUS Accounting Logs
	var logs []models.RadAcct
	if tab == "radius" {
		query := `SELECT radacctid, acctsessionid, username, nasipaddress, acctstarttime, acctstoptime,
			acctsessiontime, acctinputoctets, acctoutputoctets, framedipaddress, acctterminatecause
			FROM radacct`
		var args []interface{}
		if search != "" {
			query += " WHERE username LIKE ?"
			args = append(args, "%"+search+"%")
		}
		query += " ORDER BY radacctid DESC LIMIT 150"
		rows, err := bp.db.Query(query, args...)
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
	}

	_ = bp.renderer.Render(w, "reports/index", map[string]interface{}{
		"Title":          "التقارير المالية وسجلات المحاسبة",
		"ActiveMenu":     "reports",
		"ActiveTab":      tab,
		"CurrentUser":    middleware.GetUser(r),
		"BillingList":    billingList,
		"TotalBilling":   totalBilling,
		"BillingSummary": billingSummary,
		"Logs":           logs,
		"SearchQuery":    search,
		"TypeFilter":     typeFilter,
		"StatusFilter":   statusFilter,
	})
}
