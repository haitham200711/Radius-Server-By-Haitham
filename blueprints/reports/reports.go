package reports

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

	currentUser := middleware.GetUser(r)
	var resellerID int64
	if currentUser != nil && currentUser.Role == "reseller" {
		resellerID = currentUser.ID
	}

	// 1. Handle CSV Export for Logins
	if tab == "logins" && r.URL.Query().Get("export") == "csv" {
		sessions, _, err := bp.db.GetSubscriberLoginSessions(search, statusFilter, nasFilter, fromDate, toDate, resellerID, 5000, 0)
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

	// 1.1 Handle CSV Export for Reseller Subscriber Debts
	resellerFilter, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("reseller_id")), 10, 64)
	currUser := middleware.GetUser(r)
	if currUser != nil && currUser.Role == "reseller" {
		resellerFilter = currUser.ID
	}

	if tab == "reseller_debts" && r.URL.Query().Get("export") == "csv" {
		debts, _, _, err := bp.db.GetResellerIndebtedSubscribers(resellerFilter, search, 5000, 0)
		if err == nil {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=reseller_subscribers_debt_%s.csv", now.Format("20060102_1504")))

			// UTF-8 BOM for Excel Arabic compatibility
			_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
			writer := csv.NewWriter(w)

			_ = writer.Write([]string{
				"رقم المشترك",
				"اسم العميل",
				"اسم المستخدم",
				"رقم الهاتف",
				"رقم العقد",
				"الموزع التابع له",
				"الباقة",
				"قيمة المديونية (ج.م)",
				"الرصيد المتاح (ج.م)",
				"تاريخ الانتهاء",
				"الحالة",
			})

			for _, d := range debts {
				exp := ""
				if d.ExpirationDate.Valid {
					exp = d.ExpirationDate.Time.Format("2006-01-02 15:04")
				}
				_ = writer.Write([]string{
					fmt.Sprintf("%d", d.ID),
					d.FullName,
					d.Username,
					d.Phone,
					d.ContractNo,
					d.ResellerName,
					d.ProfileDisplay,
					fmt.Sprintf("%.2f", d.Debt),
					fmt.Sprintf("%.2f", d.Balance),
					exp,
					d.Status,
				})
			}
			writer.Flush()
			return
		}
	}

	// 1.2 Handle Traffic Report Export & JSON Live Search
	trafficUser := strings.TrimSpace(r.URL.Query().Get("user"))
	trafficYear, _ := strconv.Atoi(r.URL.Query().Get("year"))
	var trafficMonth int
	if r.URL.Query().Has("month") {
		m, err := strconv.Atoi(r.URL.Query().Get("month"))
		if err == nil && m >= 0 && m <= 12 {
			trafficMonth = m
		} else {
			trafficMonth = 0
		}
	} else {
		// When no month specified, default to 0 (Full Year Mode)
		trafficMonth = 0
	}
	if trafficYear <= 0 {
		trafficYear = now.Year()
	}

	if tab == "traffic" && r.URL.Query().Get("format") == "json" {
		subs, _ := bp.db.SearchSubscribersForTraffic(search, 15)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(subs)
		return
	}

	if tab == "traffic" && r.URL.Query().Get("export") == "csv" && trafficUser != "" {
		rep, err := bp.db.GetSubscriberTrafficReport(trafficUser, trafficYear, trafficMonth)
		if err == nil && rep != nil {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			if trafficMonth == 0 {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=traffic_%s_annual_%d.csv", trafficUser, trafficYear))
				_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
				writer := csv.NewWriter(w)
				_ = writer.Write([]string{
					"رقم الشهر",
					"اسم الشهر",
					"الرفع (Upload)",
					"التحميل (Download)",
					"إجمالي الاستهلاك",
					"نسبة الاستهلاك من السنة",
					"عدد الجلسات",
					"مدة الاتصال",
				})
				for _, m := range rep.MonthlyList {
					_ = writer.Write([]string{
						fmt.Sprintf("%02d", m.Month),
						m.MonthNameAr,
						m.UploadDisplay,
						m.DownloadDisplay,
						m.TotalDisplay,
						fmt.Sprintf("%.1f%%", m.PercentOfYear),
						fmt.Sprintf("%d", m.SessionCount),
						m.DurationString,
					})
				}
				writer.Flush()
				return
			} else {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=traffic_%s_%d_%02d.csv", trafficUser, trafficYear, trafficMonth))
				_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
				writer := csv.NewWriter(w)
				_ = writer.Write([]string{
					"اليوم",
					"التاريخ",
					"اليوم من الأسبوع",
					"الرفع (Upload)",
					"التحميل (Download)",
					"إجمالي الاستهلاك (Total)",
					"عدد الجلسات",
					"مدة الاتصال",
				})
				for _, d := range rep.DailyList {
					_ = writer.Write([]string{
						fmt.Sprintf("%02d", d.DayNum),
						d.Date,
						d.DayNameAr,
						d.UploadDisplay,
						d.DownloadDisplay,
						d.TotalDisplay,
						fmt.Sprintf("%d", d.SessionCount),
						d.DurationString,
					})
				}
				writer.Flush()
				return
			}
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
		loginSessions, totalLogins, _ = bp.db.GetSubscriberLoginSessions(search, statusFilter, nasFilter, fromDate, toDate, resellerID, limit, offset)
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

	totalPages := 1
	fromIndex := 0
	toIndex := 0

	if tab == "logins" {
		totalPages = (totalLogins + limit - 1) / limit
		if totalPages < 1 {
			totalPages = 1
		}
		if totalLogins > 0 {
			fromIndex = offset + 1
			toIndex = offset + len(loginSessions)
		}
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

	// 4. Reseller Indebted Subscribers Tab Data (Smart Debt Settlement)
	var resellerDebts []models.ResellerSubscriberDebt
	var totalDebtCount int
	var totalDebtSum float64
	var resellersDebtSummary []models.ResellerDebtSummary
	var grandTotalDebt float64
	var grandTotalDebtors int
	var allResellers []models.Reseller

	if tab == "reseller_debts" {
		resellerDebts, totalDebtCount, totalDebtSum, _ = bp.db.GetResellerIndebtedSubscribers(resellerFilter, search, limit, offset)
		resellersDebtSummary, grandTotalDebt, grandTotalDebtors, _ = bp.db.GetResellersDebtSummary()
		allResellers, _ = bp.db.GetResellers()

		totalPages = (totalDebtCount + limit - 1) / limit
		if totalPages < 1 {
			totalPages = 1
		}
		fromIndex = offset + 1
		toIndex = offset + len(resellerDebts)
		if totalDebtCount == 0 {
			fromIndex = 0
			toIndex = 0
		}
	}

	// 5. Subscriber Traffic Consumption Tab Data (استهلاك المشترك شهري ويومي)
	var trafficReport *models.SubscriberTrafficReport
	var topConsumers []models.TopConsumerItem
	var matchedSubs []models.Subscriber

	if tab == "traffic" {
		if trafficUser != "" {
			trafficReport, _ = bp.db.GetSubscriberTrafficReport(trafficUser, trafficYear, trafficMonth)
		} else if search != "" {
			matchedSubs, _ = bp.db.SearchSubscribersForTraffic(search, 20)
			if len(matchedSubs) == 1 {
				trafficUser = matchedSubs[0].Username
				trafficReport, _ = bp.db.GetSubscriberTrafficReport(trafficUser, trafficYear, trafficMonth)
			}
		}
		topConsumers, _ = bp.db.GetTopConsumers(trafficYear, trafficMonth, 10)
	}

	// 6. Raw RADIUS Logs Tab Data
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

	pageTitle := "تقارير وسجلات النظام"
	if tab == "reseller_debts" {
		pageTitle = "تقرير ديون مشتركي الموزعين والتسوية الذكية"
	} else if tab == "billing" {
		pageTitle = "سجل المعاملات المالية والديون"
	} else if tab == "traffic" {
		pageTitle = "تقرير استهلاك المشترك الشهري واليومي"
	}

	_ = bp.renderer.Render(w, r, "reports/index", map[string]interface{}{
		"Title":                pageTitle,
		"ActiveMenu":           "reports",
		"ActiveTab":            tab,
		"CurrentUser":          middleware.GetUser(r),
		"LoginSessions":        loginSessions,
		"TotalLogins":          totalLogins,
		"LoginSummary":         loginSummary,
		"RoutersList":          routersList,
		"BillingList":          billingList,
		"TotalBilling":         totalBilling,
		"BillingSummary":       billingSummary,
		"ResellerDebts":        resellerDebts,
		"TotalDebtCount":       totalDebtCount,
		"TotalDebtSum":         totalDebtSum,
		"ResellersDebtSummary": resellersDebtSummary,
		"GrandTotalDebt":       grandTotalDebt,
		"GrandTotalDebtors":    grandTotalDebtors,
		"AllResellers":         allResellers,
		"ResellerFilter":       resellerFilter,
		"TrafficUser":          trafficUser,
		"TrafficYear":          trafficYear,
		"TrafficMonth":         trafficMonth,
		"TrafficReport":        trafficReport,
		"TopConsumers":         topConsumers,
		"MatchedSubs":          matchedSubs,
		"RawLogs":              rawLogs,
		"SearchQuery":          search,
		"StatusFilter":         statusFilter,
		"NASFilter":            nasFilter,
		"FromDate":             fromDate,
		"ToDate":               toDate,
		"DatePreset":           datePreset,
		"TypeFilter":           typeFilter,
		"Page":                 page,
		"Limit":                limit,
		"TotalPages":           totalPages,
		"HasPrev":              page > 1,
		"HasNext":              page < totalPages,
		"PrevPage":             page - 1,
		"NextPage":             page + 1,
		"FromIndex":            fromIndex,
		"ToIndex":              toIndex,
		"PageNumbers":          getPageNumbers(page, totalPages),
		"Success":              r.URL.Query().Get("success"),
		"Error":                r.URL.Query().Get("error"),
	})
}

func (bp *Blueprint) LoginsRedirectHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/reports?tab=logins", http.StatusFound)
}

// ClearHistoryHandler resets historical report data for admins while preserving
// subscribers, balances, active sessions, and active loans.
func (bp *Blueprint) ClearHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := bp.db.ClearReportHistory(); err != nil {
		http.Redirect(w, r, "/reports?error="+url.QueryEscape("فشل تنظيف تاريخ التقارير: "+err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/reports?success="+url.QueryEscape("تم تنظيف تاريخ التقارير مع الإبقاء على الحسابات والجلسات الحالية"), http.StatusFound)
}
