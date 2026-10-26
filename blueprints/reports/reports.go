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

// Blueprint represents the centralized enterprise reporting module
type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

// New creates a new reports blueprint instance
func New(db *database.DB, renderer *web.Renderer) *Blueprint {
	return &Blueprint{db: db, renderer: renderer}
}

// IndexHandler renders the tabbed enterprise reports dashboard or streams CSV exports
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		tab = "sessions"
	}

	export := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("export")))

	// Determine authorized reseller scope:
	// If current user is a reseller, they can ONLY view their assigned subscribers and own ledger.
	var effectiveResellerID int64
	if currentUser.Role == "reseller" {
		effectiveResellerID = currentUser.ID
	} else if r.URL.Query().Get("reseller_id") != "" {
		if rID, err := strconv.ParseInt(r.URL.Query().Get("reseller_id"), 10, 64); err == nil && rID > 0 {
			effectiveResellerID = rID
		}
	}

	// Pagination params
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	offset := (page - 1) * limit

	// Common Filters
	searchQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	nasFilter := strings.TrimSpace(r.URL.Query().Get("nas"))
	fromDate := strings.TrimSpace(r.URL.Query().Get("from"))
	toDate := strings.TrimSpace(r.URL.Query().Get("to"))
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortBy == "" {
		sortBy = "newest"
	}

	// Reseller list for admin dropdown
	var resellersList []map[string]interface{}
	if currentUser.Role == "admin" {
		resList, _ := bp.db.GetResellers()
		for _, res := range resList {
			resellersList = append(resellersList, map[string]interface{}{
				"ID":   res.ID,
				"Name": res.Name,
			})
		}
	}

	// NAS list for router dropdown
	nasRouters, _ := bp.db.GetNASList()

	// Data container
	data := map[string]interface{}{
		"Title":                "تقارير النظام والتحليلات الشاملة",
		"ActiveMenu":           "reports",
		"ActiveTab":            tab,
		"CurrentUser":          currentUser,
		"SearchQuery":          searchQuery,
		"StatusFilter":         statusFilter,
		"NASFilter":            nasFilter,
		"FromDate":             fromDate,
		"ToDate":               toDate,
		"SortBy":               sortBy,
		"ResellerFilter":       effectiveResellerID,
		"ResellersList":        resellersList,
		"NASRouters":           nasRouters,
		"Page":                 page,
		"Limit":                limit,
		"CurrentTimeFormatted": time.Now().Format("2006-01-02 15:04:05"),
	}

	switch tab {
	// =========================================================================
	// 1. تقرير حالات تسجيل الدخول والجلسات (Sessions Report)
	// =========================================================================
	case "sessions":
		if export == "csv" {
			sessions, _, err := bp.db.GetSubscriberLoginSessionsReport(searchQuery, statusFilter, nasFilter, fromDate, toDate, effectiveResellerID, sortBy, 5000, 0)
			if err != nil {
				http.Error(w, "فشل تصدير التقرير: "+err.Error(), http.StatusInternalServerError)
				return
			}
			bp.exportSessionsCSV(w, sessions)
			return
		}

		sessions, total, err := bp.db.GetSubscriberLoginSessionsReport(searchQuery, statusFilter, nasFilter, fromDate, toDate, effectiveResellerID, sortBy, limit, offset)
		if err != nil {
			data["ErrorMessage"] = "فشل تحميل سجل الجلسات: " + err.Error()
		}
		summary, _ := bp.db.GetSubscriberLoginSummary(searchQuery, statusFilter, nasFilter, fromDate, toDate)

		data["Sessions"] = sessions
		data["SessionsSummary"] = summary
		data["TotalRecords"] = total
		bp.populatePagination(data, total, page, limit)

	// =========================================================================
	// 2. تقرير استهلاك العملاء (Traffic Report)
	// =========================================================================
	case "traffic":
		trafficUser := strings.TrimSpace(r.URL.Query().Get("user"))
		now := time.Now()
		trafficYear := now.Year()
		if y, err := strconv.Atoi(r.URL.Query().Get("year")); err == nil && y > 2000 {
			trafficYear = y
		}
		trafficMonth := 0 // 0 = Full year
		if m, err := strconv.Atoi(r.URL.Query().Get("month")); err == nil && m >= 0 && m <= 12 {
			trafficMonth = m
		}

		data["TrafficUser"] = trafficUser
		data["TrafficYear"] = trafficYear
		data["TrafficMonth"] = trafficMonth

		// Top consumers list
		topConsumers, _ := bp.db.GetTopConsumersReport(effectiveResellerID, trafficYear, trafficMonth, 15)
		data["TopConsumers"] = topConsumers

		// Detailed single subscriber traffic report if requested
		if trafficUser != "" {
			trafficReport, err := bp.db.GetSubscriberTrafficReport(trafficUser, trafficYear, trafficMonth)
			if err != nil {
				data["ErrorMessage"] = "تعذر تحميل استهلاك المشترك: " + err.Error()
			} else {
				data["TrafficReport"] = trafficReport
				if export == "csv" {
					bp.exportTrafficCSV(w, trafficReport)
					return
				}
			}
		}

	// =========================================================================
	// 3. تقرير ديون العملاء (Subscriber Debts Report)
	// =========================================================================
	case "debts":
		debtStatusFilter := strings.TrimSpace(r.URL.Query().Get("debt_status"))
		data["DebtStatusFilter"] = debtStatusFilter

		if export == "csv" {
			debts, _, _, err := bp.db.GetSubscriberDebtsReport(searchQuery, debtStatusFilter, effectiveResellerID, 5000, 0)
			if err != nil {
				http.Error(w, "فشل تصدير كشف الديون: "+err.Error(), http.StatusInternalServerError)
				return
			}
			bp.exportDebtsCSV(w, debts)
			return
		}

		debts, total, debtsSummary, err := bp.db.GetSubscriberDebtsReport(searchQuery, debtStatusFilter, effectiveResellerID, limit, offset)
		if err != nil {
			data["ErrorMessage"] = "فشل تحميل تقرير ديون العملاء: " + err.Error()
		}
		data["Debts"] = debts
		data["DebtsSummary"] = debtsSummary
		data["TotalRecords"] = total
		bp.populatePagination(data, total, page, limit)

	// =========================================================================
	// 4. تقرير أرصدة الموزعين والمديرين (Reseller & Admin Balances Report)
	// =========================================================================
	case "resellers":
		targetReseller := effectiveResellerID
		if currentUser.Role == "admin" && r.URL.Query().Get("target_reseller") != "" {
			if tID, err := strconv.ParseInt(r.URL.Query().Get("target_reseller"), 10, 64); err == nil && tID > 0 {
				targetReseller = tID
			}
		}
		data["TargetReseller"] = targetReseller

		if export == "csv" {
			resellers, _, ledgerItems, _, err := bp.db.GetResellerBalancesReport(targetReseller, 5000, 0)
			if err != nil {
				http.Error(w, "فشل تصدير كشف الموزعين: "+err.Error(), http.StatusInternalServerError)
				return
			}
			bp.exportResellersCSV(w, resellers, ledgerItems)
			return
		}

		resellers, resSummary, ledgerItems, totalLedger, err := bp.db.GetResellerBalancesReport(targetReseller, limit, offset)
		if err != nil {
			data["ErrorMessage"] = "فشل تحميل تقرير أرصدة الموزعين: " + err.Error()
		}
		data["ResellersBalances"] = resellers
		data["ResellerSummary"] = resSummary
		data["ResellerLedger"] = ledgerItems
		data["TotalLedgerRecords"] = totalLedger
		bp.populatePagination(data, totalLedger, page, limit)

	// =========================================================================
	// 5. تقرير أرصدة العملاء والعمليات المالية (Subscriber Balances & Ledger)
	// =========================================================================
	case "subscribers":
		balanceState := strings.TrimSpace(r.URL.Query().Get("balance_state"))
		data["BalanceState"] = balanceState

		if export == "csv" {
			subs, _, _, _, _, err := bp.db.GetSubscriberBalancesReport(searchQuery, balanceState, effectiveResellerID, 5000, 0)
			if err != nil {
				http.Error(w, "فشل تصدير أرصدة العملاء: "+err.Error(), http.StatusInternalServerError)
				return
			}
			bp.exportSubscriberBalancesCSV(w, subs)
			return
		}

		subs, total, subSummary, ledgerItems, totalLedger, err := bp.db.GetSubscriberBalancesReport(searchQuery, balanceState, effectiveResellerID, limit, offset)
		if err != nil {
			data["ErrorMessage"] = "فشل تحميل تقرير أرصدة العملاء: " + err.Error()
		}
		data["SubscribersBalances"] = subs
		data["SubscriberSummary"] = subSummary
		data["SubscriberLedger"] = ledgerItems
		data["TotalRecords"] = total
		data["TotalLedgerRecords"] = totalLedger
		bp.populatePagination(data, total, page, limit)
	}

	_ = bp.renderer.Render(w, r, "reports/index", data)
}

// -----------------------------------------------------------------------------
// Pagination Helper
// -----------------------------------------------------------------------------

func (bp *Blueprint) populatePagination(data map[string]interface{}, total, page, limit int) {
	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	data["TotalRecords"] = total
	data["TotalPages"] = totalPages
	data["HasPrev"] = page > 1
	data["HasNext"] = page < totalPages
	data["PrevPage"] = page - 1
	data["NextPage"] = page + 1

	// Visible page window (up to 7 items)
	var visiblePages []int
	startPage := page - 3
	if startPage < 1 {
		startPage = 1
	}
	endPage := startPage + 6
	if endPage > totalPages {
		endPage = totalPages
		startPage = endPage - 6
		if startPage < 1 {
			startPage = 1
		}
	}
	for i := startPage; i <= endPage; i++ {
		visiblePages = append(visiblePages, i)
	}
	data["VisiblePages"] = visiblePages
}

// -----------------------------------------------------------------------------
// CSV Exporters with UTF-8 BOM for flawless Excel compatibility in Arabic
// -----------------------------------------------------------------------------

func (bp *Blueprint) exportSessionsCSV(w http.ResponseWriter, sessions []models.SubscriberLoginSession) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=sessions_report_%s.csv", time.Now().Format("20060102_150405")))
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // UTF-8 BOM

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"رقم الجلسة (ID)",
		"اسم المستخدم",
		"اسم العميل",
		"رقم العقد",
		"الموزع",
		"حالة الجلسة",
		"عنوان IP",
		"عنوان MAC",
		"الراوتر / NAS",
		"وقت البداية",
		"وقت النهاية",
		"مدة الجلسة",
		"حجم التحميل",
		"حجم الرفع",
		"إجمالي الاستهلاك",
		"سبب انتهاء الجلسة",
		"Acct Session ID",
	})

	for _, s := range sessions {
		_ = writer.Write([]string{
			strconv.FormatInt(s.RadAcctID, 10),
			s.UserName,
			s.FullName,
			s.ContractNo,
			s.ResellerName,
			s.StatusText,
			s.FramedIPAddress,
			s.CallingStationID,
			s.NASName + " (" + s.NASIPAddress + ")",
			s.StartTimeFormatted,
			s.StopTimeFormatted,
			s.DurationFormatted,
			s.DownloadFormatted,
			s.UploadFormatted,
			s.TotalBytesFormatted,
			s.TerminateCauseText,
			s.AcctSessionID,
		})
	}
}

func (bp *Blueprint) exportTrafficCSV(w http.ResponseWriter, r *models.SubscriberTrafficReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=traffic_%s_%d_%d.csv", r.Username, r.SelectedYear, r.SelectedMonth))
	_, _ = w.Write([]byte("\xef\xbb\xbf"))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{"تقرير استهلاك المشترك", r.Username, r.FullName, r.ContractNo, r.ResellerName})
	_ = writer.Write([]string{""})

	if r.SelectedMonth == 0 {
		_ = writer.Write([]string{"الشهر", "التحميل (GB)", "الرفع (GB)", "إجمالي الاستهلاك (GB)", "عدد الجلسات", "مدة الاتصال"})
		for _, m := range r.MonthlyList {
			_ = writer.Write([]string{
				m.MonthName,
				fmt.Sprintf("%.2f", m.DownloadGB),
				fmt.Sprintf("%.2f", m.UploadGB),
				fmt.Sprintf("%.2f", m.TotalGB),
				strconv.Itoa(m.SessionCount),
				m.DurationString,
			})
		}
	} else {
		_ = writer.Write([]string{"اليوم", "التاريخ", "التحميل (GB)", "الرفع (GB)", "إجمالي الاستهلاك (GB)", "عدد الجلسات", "مدة الاتصال"})
		for _, d := range r.DailyList {
			_ = writer.Write([]string{
				d.DayNameAr,
				d.Date,
				fmt.Sprintf("%.2f", d.DownloadGB),
				fmt.Sprintf("%.2f", d.UploadGB),
				fmt.Sprintf("%.2f", d.TotalGB),
				strconv.Itoa(d.SessionCount),
				d.DurationString,
			})
		}
	}
}

func (bp *Blueprint) exportDebtsCSV(w http.ResponseWriter, debts []models.SubscriberDebtItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=debts_report_%s.csv", time.Now().Format("20060102_150405")))
	_, _ = w.Write([]byte("\xef\xbb\xbf"))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"اسم المستخدم",
		"اسم العميل",
		"رقم العقد",
		"الهاتف",
		"الموزع",
		"الرصيد الحالي",
		"قيمة الدين",
		"عمر الدين (أيام)",
		"حالة الدين",
		"قيمة آخر سداد",
		"تاريخ آخر سداد",
		"تاريخ آخر تجديد",
	})

	for _, d := range debts {
		_ = writer.Write([]string{
			d.Username,
			d.FullName,
			d.ContractNo,
			d.Phone,
			d.ResellerName,
			fmt.Sprintf("%.2f", d.Balance),
			fmt.Sprintf("%.2f", d.Debt),
			strconv.Itoa(d.DebtAgeDays),
			d.DebtStatusText,
			fmt.Sprintf("%.2f", d.LastPaymentAmount),
			d.LastPaymentDate,
			d.LastRenewalDate,
		})
	}
}

func (bp *Blueprint) exportResellersCSV(w http.ResponseWriter, resellers []models.ResellerBalanceItem, ledger []models.ResellerLedgerItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=resellers_report_%s.csv", time.Now().Format("20060102_150405")))
	_, _ = w.Write([]byte("\xef\xbb\xbf"))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{"--- جدول أرصدة الموزعين والمديرين ---"})
	_ = writer.Write([]string{
		"اسم الموزع",
		"اسم المستخدم",
		"الدور",
		"الهاتف",
		"الرصيد الحالي",
		"الحد الائتماني",
		"الدين الحالي",
		"الرصيد المتاح فعلياً",
		"نسبة العمولة %",
		"إجمالي الإيداعات",
		"تكلفة التجديدات",
		"تكلفة الكوتة",
		"تكلفة الكروت",
		"عدد المشتركين",
		"تاريخ آخر حركة",
	})

	for _, r := range resellers {
		_ = writer.Write([]string{
			r.Name,
			r.Username,
			r.Role,
			r.Phone,
			fmt.Sprintf("%.2f", r.Balance),
			fmt.Sprintf("%.2f", r.CreditLimit),
			fmt.Sprintf("%.2f", r.Debt),
			fmt.Sprintf("%.2f", r.AvailableCapacity),
			fmt.Sprintf("%.2f", r.CommissionPercent),
			fmt.Sprintf("%.2f", r.TotalDeposits),
			fmt.Sprintf("%.2f", r.TotalRenewalsCost),
			fmt.Sprintf("%.2f", r.TotalQuotaCost),
			fmt.Sprintf("%.2f", r.TotalCardsCost),
			strconv.Itoa(r.SubscribersCount),
			r.LastTxDate,
		})
	}

	_ = writer.Write([]string{""})
	_ = writer.Write([]string{"--- سجل العمليات المالية الأخيرة ---"})
	_ = writer.Write([]string{
		"رقم المرجع",
		"الموزع",
		"نوع العملية",
		"المبلغ",
		"الرصيد قبل",
		"الرصيد بعد",
		"الدين قبل",
		"الدين بعد",
		"المشترك المرتبط",
		"البيان / الملاحظات",
		"المستخدم المنفذ",
		"التاريخ والوقت",
	})

	for _, l := range ledger {
		_ = writer.Write([]string{
			l.ReferenceID,
			l.ResellerName,
			l.OperationTypeText,
			fmt.Sprintf("%.2f", l.Amount),
			fmt.Sprintf("%.2f", l.BalanceBefore),
			fmt.Sprintf("%.2f", l.BalanceAfter),
			fmt.Sprintf("%.2f", l.DebtBefore),
			fmt.Sprintf("%.2f", l.DebtAfter),
			l.SubscriberName,
			l.Description,
			l.CreatedBy,
			l.CreatedAtFormatted,
		})
	}
}

func (bp *Blueprint) exportSubscriberBalancesCSV(w http.ResponseWriter, subs []models.SubscriberBalanceItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=subscriber_balances_%s.csv", time.Now().Format("20060102_150405")))
	_, _ = w.Write([]byte("\xef\xbb\xbf"))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"اسم المستخدم",
		"اسم العميل",
		"رقم العقد",
		"الهاتف",
		"الموزع",
		"الباقة",
		"الرصيد الحالي",
		"الدين الحالي",
		"الرصيد الصافي",
		"حالة الحساب",
		"سلفة نشطة",
		"قيمة آخر شحن",
		"تاريخ آخر شحن",
		"إجمالي الشحنات",
		"إجمالي المشتريات",
		"آخر حركة",
	})

	for _, s := range subs {
		loanText := "لا"
		if s.LoanActive {
			loanText = "نعم (سلفة نشطة)"
		}
		_ = writer.Write([]string{
			s.Username,
			s.FullName,
			s.ContractNo,
			s.Phone,
			s.ResellerName,
			s.ProfileName,
			fmt.Sprintf("%.2f", s.Balance),
			fmt.Sprintf("%.2f", s.Debt),
			fmt.Sprintf("%.2f", s.NetBalance),
			s.Status,
			loanText,
			fmt.Sprintf("%.2f", s.LastDepositAmount),
			s.LastDepositDate,
			fmt.Sprintf("%.2f", s.TotalDeposits),
			fmt.Sprintf("%.2f", s.TotalPurchases),
			s.LastTxType + " (" + s.LastTxDate + ")",
		})
	}
}
