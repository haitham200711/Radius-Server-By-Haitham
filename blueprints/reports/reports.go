package reports

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

func getPageNumbers(current, total int) []int {
	if total <= 7 {
		var pages []int
		for i := 1; i <= total; i++ {
			pages = append(pages, i)
		}
		return pages
	}
	start := current - 2
	end := current + 2
	if start < 1 {
		start = 1
		end = 5
	}
	if end > total {
		end = total
		start = total - 4
		if start < 1 {
			start = 1
		}
	}
	var pages []int
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return pages
}

func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		if strings.HasPrefix(r.URL.Path, "/reports/logins") {
			tab = "logins"
		} else {
			tab = "logins" // Default to customer logins report as requested!
		}
	}

	search := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	nasFilter := strings.TrimSpace(r.URL.Query().Get("nas"))
	fromDate := strings.TrimSpace(r.URL.Query().Get("from"))
	toDate := strings.TrimSpace(r.URL.Query().Get("to"))
	datePreset := strings.TrimSpace(r.URL.Query().Get("preset"))

	// Quick Date Presets
	now := time.Now()
	switch datePreset {
	case "today":
		fromDate = now.Format("2006-01-02")
		toDate = now.Format("2006-01-02")
	case "yesterday":
		y := now.AddDate(0, 0, -1)
		fromDate = y.Format("2006-01-02")
		toDate = y.Format("2006-01-02")
	case "week":
		w := now.AddDate(0, 0, -7)
		fromDate = w.Format("2006-01-02")
		toDate = now.Format("2006-01-02")
	case "month":
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		fromDate = firstOfMonth.Format("2006-01-02")
		toDate = now.Format("2006-01-02")
	}

	// 1. Handle CSV Export for Logins
	if tab == "logins" && r.URL.Query().Get("export") == "csv" {
		sessions, _, err := bp.db.GetSubscriberLoginSessions(search, statusFilter, nasFilter, fromDate, toDate, 5000, 0)
		if err == nil {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=subscriber_logins_%s.csv", now.Format("20060102_1504")))

			// UTF-8 BOM for Excel Arabic compatibility
			_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
			writer := csv.NewWriter(w)

			_ = writer.Write([]string{
				"رقم الجلسة",
				"اسم المستخدم",
				"اسم العميل",
				"رقم العقد",
				"حالة الاتصال",
				"عنوان IP",
				"عنوان الماك (MAC)",
				"الراوتر / NAS",
				"وقت بدء الاتصال",
				"وقت انتهاء الاتصال",
				"مدة الجلسة",
				"التحميل (Download)",
				"الرفع (Upload)",
				"إجمالي الاستهلاك",
				"سبب انتهاء الجلسة",
			})

			for _, s := range sessions {
				statusStr := "متصل الآن"
				if !s.IsOnline {
					statusStr = "منتهية"
				}
				_ = writer.Write([]string{
					s.AcctSessionID,
					s.UserName,
					s.FullName,
					s.ContractNo,
					statusStr,
					s.FramedIPAddress,
					s.CallingStationID,
					s.NASName,
					s.StartTimeFormatted,
					s.StopTimeFormatted,
					s.DurationFormatted,
					s.DownloadFormatted,
					s.UploadFormatted,
					s.TotalBytesFormatted,
					s.TerminateCauseText,
				})
			}
			writer.Flush()
			return
		}
	}

	// 2. Pagination for Logins Tab
	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
		if limit > 200 {
			limit = 200
		}
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	var loginSessions []models.SubscriberLoginSession
	var totalLogins int
	var loginSummary *models.SubscriberLoginSummary
	var routersList []models.NAS

	if tab == "logins" {
		loginSessions, totalLogins, _ = bp.db.GetSubscriberLoginSessions(search, statusFilter, nasFilter, fromDate, toDate, limit, offset)
		loginSummary, _ = bp.db.GetSubscriberLoginSummary(search, statusFilter, nasFilter, fromDate, toDate)

		// Fetch Routers for NAS filter dropdown
		rows, err := bp.db.Query("SELECT nasname, COALESCE(shortname, nasname) FROM nas ORDER BY shortname ASC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var n models.NAS
				if err := rows.Scan(&n.NASName, &n.ShortName); err == nil {
					routersList = append(routersList, n)
				}
			}
		}
	}

	totalPages := (totalLogins + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	// 3. Billing & Financial Transactions Tab Data
	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
	var billingList []models.BillingTransaction
	var totalBilling int
	var billingSummary models.BillingSummary
	if tab == "billing" {
		billingList, totalBilling, _ = bp.db.GetBillingTransactions(search, typeFilter, statusFilter, 200, 0)
		billingSummary, _ = bp.db.GetBillingFinancialSummary()
	}

	// 4. Raw RADIUS Logs Tab Data
	var rawLogs []models.RadAcct
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
					rawLogs = append(rawLogs, l)
				}
			}
		}
	}

	fromIndex := offset + 1
	toIndex := offset + len(loginSessions)
	if totalLogins == 0 {
		fromIndex = 0
		toIndex = 0
	}

	_ = bp.renderer.Render(w, r, "reports/index", map[string]interface{}{
		"Title":          "تقرير حالات تسجيل دخول وجلسات المشتركين",
		"ActiveMenu":     "reports",
		"ActiveTab":      tab,
		"CurrentUser":    middleware.GetUser(r),
		"LoginSessions":  loginSessions,
		"TotalLogins":    totalLogins,
		"LoginSummary":   loginSummary,
		"RoutersList":    routersList,
		"BillingList":    billingList,
		"TotalBilling":   totalBilling,
		"BillingSummary": billingSummary,
		"RawLogs":        rawLogs,
		"SearchQuery":    search,
		"StatusFilter":   statusFilter,
		"NASFilter":      nasFilter,
		"FromDate":       fromDate,
		"ToDate":         toDate,
		"DatePreset":     datePreset,
		"TypeFilter":     typeFilter,
		"Page":           page,
		"Limit":          limit,
		"TotalPages":     totalPages,
		"HasPrev":        page > 1,
		"HasNext":        page < totalPages,
		"PrevPage":       page - 1,
		"NextPage":       page + 1,
		"FromIndex":      fromIndex,
		"ToIndex":        toIndex,
		"PageNumbers":    getPageNumbers(page, totalPages),
	})
}

func (bp *Blueprint) LoginsRedirectHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/reports?tab=logins", http.StatusFound)
}
