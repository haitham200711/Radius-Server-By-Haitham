package subscribers

import (
	"database/sql"
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

type SubscriberItem struct {
	ID                int64
	ContractNo        string
	FullName          string
	Username          string
	Password          string
	Phone             string
	Address           string
	Debt              float64
	Profile           string
	IPAddress         string
	QuotaBytes        int64
	QuotaGB           float64
	QuotaDisplay      string
	UsedBytes         int64
	UsedGB            float64
	UsedDisplay       string
	RemainingBytes    int64
	RemainingGB       float64
	RemainingDisplay  string
	UsagePercent      float64
	ExpirationDate    string
	Status            string
	StatusCode        string // "active", "disabled", "expired_time", "expired_quota", "expired_both"
	StatusText        string
	IsTimeExpired     bool
	IsQuotaExpired    bool
	IsOnline          bool
	OnlineIP          string
	ResellerID        int64
	ResellerName      string
	LoanActive        bool
	LoanGrantedAt     string
	LoanHours         int
	LoanQuotaBytes    int64
	LoanQuotaGB       float64
	LoanDeductionType string
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

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status")) // "online", "offline", or ""
	resellerFilter, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("reseller_id")), 10, 64)

	currentUser := middleware.GetUser(r)
	var resellerPerms *models.ResellerPermissions
	if currentUser != nil && currentUser.Role == "reseller" {
		resellerFilter = currentUser.ID
		if currentRes, err := bp.db.GetReseller(currentUser.ID); err == nil && currentRes != nil {
			resellerPerms = &currentRes.Permissions
		}
	}

	// 1. Pagination & Limits (default 10, max 500)
	limit := 10
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
		if limit > 500 {
			limit = 500
		}
	}

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	// 2. Fetch live connected sessions from radacct (acctstoptime IS NULL)
	onlineMap := make(map[string]string)
	activeRows, err := bp.db.Query(`SELECT username, framedipaddress FROM radacct WHERE acctstoptime IS NULL ORDER BY radacctid DESC`)
	if err == nil {
		defer activeRows.Close()
		for activeRows.Next() {
			var u, ip string
			if err := activeRows.Scan(&u, &ip); err == nil && u != "" {
				if _, exists := onlineMap[u]; !exists {
					onlineMap[u] = ip
				}
			}
		}
	}

	var onlineCount int
	_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT username) FROM radacct WHERE acctstoptime IS NULL`).Scan(&onlineCount)

	// 3. Count total matching subscribers
	countQuery := `SELECT COUNT(DISTINCT rc.username)
	FROM radcheck rc
	LEFT JOIN subscribers s ON rc.username = s.username
	WHERE rc.attribute = 'Cleartext-Password'`

	var countArgs []interface{}
	if search != "" {
		countQuery += " AND (rc.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)"
		searchArg := "%" + search + "%"
		countArgs = append(countArgs, searchArg, searchArg, searchArg, searchArg)
	}

	if statusFilter == "online" {
		countQuery += " AND rc.username IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)"
	} else if statusFilter == "offline" {
		countQuery += " AND rc.username NOT IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)"
	} else if statusFilter == "expired_time" {
		countQuery += " AND ((s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1)"
	} else if statusFilter == "expired_quota" {
		countQuery += " AND s.quota_bytes > 0 AND (SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = rc.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes"
	} else if statusFilter == "loan" {
		countQuery += " AND s.loan_active = 1"
	}

	if resellerFilter > 0 {
		countQuery += " AND s.reseller_id = ?"
		countArgs = append(countArgs, resellerFilter)
	}

	var totalCount int
	_ = bp.db.QueryRow(countQuery, countArgs...).Scan(&totalCount)

	totalPages := (totalCount + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// 4. Query paginated subscribers
	query := `SELECT 
		COALESCE(s.id, 0),
		COALESCE(s.contract_no, '-'),
		COALESCE(s.full_name, rc.username),
		rc.username,
		COALESCE(rc.value, ''),
		COALESCE(s.phone, '-'),
		COALESCE(s.address, '-'),
		COALESCE(s.debt, 0.00),
		COALESCE(s.profile_name, COALESCE(rug.groupname, '')),
		COALESCE(s.static_ip, COALESCE(rr.value, '')),
		COALESCE(s.quota_bytes, 0),
		s.expiration_date,
		COALESCE(s.status, 'active'),
		COALESCE(s.last_renewed_at, s.created_at),
		COALESCE(s.reseller_id, 0),
		COALESCE(res.name, ''),
		COALESCE(s.loan_active, 0),
		s.loan_granted_at,
		COALESCE(s.loan_hours, 0),
		COALESCE(s.loan_quota_bytes, 0),
		COALESCE(s.loan_deduction_type, 'full')
	FROM radcheck rc
	LEFT JOIN subscribers s ON rc.username = s.username
	LEFT JOIN resellers res ON s.reseller_id = res.id
	LEFT JOIN radusergroup rug ON rc.username = rug.username
	LEFT JOIN radreply rr ON rc.username = rr.username AND rr.attribute = 'Framed-IP-Address'
	WHERE rc.attribute = 'Cleartext-Password'`

	var args []interface{}
	if search != "" {
		query += " AND (rc.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)"
		searchArg := "%" + search + "%"
		args = append(args, searchArg, searchArg, searchArg, searchArg)
	}

	if statusFilter == "online" {
		query += " AND rc.username IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)"
	} else if statusFilter == "offline" {
		query += " AND rc.username NOT IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)"
	} else if statusFilter == "expired_time" {
		query += " AND ((s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1)"
	} else if statusFilter == "expired_quota" {
		query += " AND s.quota_bytes > 0 AND (SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = rc.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes"
	} else if statusFilter == "loan" {
		query += " AND s.loan_active = 1"
	}

	if resellerFilter > 0 {
		query += " AND s.reseller_id = ?"
		args = append(args, resellerFilter)
	}

	query += " GROUP BY rc.username ORDER BY COALESCE(s.id, rc.id) DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := bp.db.Query(query, args...)
	var list []SubscriberItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s SubscriberItem
			var exp sql.NullTime
			var lastRenewed sql.NullTime
			var loanGrantedAt sql.NullTime
			if err := rows.Scan(
				&s.ID,
				&s.ContractNo,
				&s.FullName,
				&s.Username,
				&s.Password,
				&s.Phone,
				&s.Address,
				&s.Debt,
				&s.Profile,
				&s.IPAddress,
				&s.QuotaBytes,
				&exp,
				&s.Status,
				&lastRenewed,
				&s.ResellerID,
				&s.ResellerName,
				&s.LoanActive,
				&loanGrantedAt,
				&s.LoanHours,
				&s.LoanQuotaBytes,
				&s.LoanDeductionType,
			); err == nil {
				if loanGrantedAt.Valid {
					s.LoanGrantedAt = loanGrantedAt.Time.Format("2006-01-02 15:04")
				}
				if s.LoanQuotaBytes > 0 {
					s.LoanQuotaGB = float64(s.LoanQuotaBytes) / (1024 * 1024 * 1024)
				}
				if resellerPerms != nil && !resellerPerms.CanViewPasswords {
					s.Password = "••••••"
				}

				if s.QuotaBytes > 0 {
					s.QuotaGB = float64(s.QuotaBytes) / (1024 * 1024 * 1024)
					s.QuotaDisplay = database.FormatBytesHuman(s.QuotaBytes)
				} else {
					s.QuotaGB = 0
					s.QuotaDisplay = "مفتوح ∞"
				}

				// Query traffic consumed from radacct
				var usedBytes int64
				if lastRenewed.Valid {
					_ = bp.db.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = ? AND acctstarttime >= ?`, s.Username, lastRenewed.Time).Scan(&usedBytes)
				} else {
					_ = bp.db.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = ?`, s.Username).Scan(&usedBytes)
				}
				s.UsedBytes = usedBytes
				s.UsedGB = float64(usedBytes) / (1024 * 1024 * 1024)
				s.UsedDisplay = database.FormatBytesHuman(usedBytes)

				if s.QuotaBytes > 0 {
					remBytes := s.QuotaBytes - usedBytes
					if remBytes < 0 {
						remBytes = 0
					}
					s.RemainingBytes = remBytes
					s.RemainingGB = float64(remBytes) / (1024 * 1024 * 1024)
					s.RemainingDisplay = database.FormatBytesHuman(remBytes)
					s.UsagePercent = (float64(usedBytes) / float64(s.QuotaBytes)) * 100.0
					if s.UsagePercent > 100.0 {
						s.UsagePercent = 100.0
					}
				} else {
					s.RemainingBytes = -1
					s.RemainingGB = -1
					s.RemainingDisplay = "مفتوح ∞"
					s.UsagePercent = 0.0
				}

				if exp.Valid {
					s.ExpirationDate = exp.Time.Format("2006-01-02 15:04")
				} else {
					s.ExpirationDate = "غير محدد"
				}

				// Check active connection status & live IP
				if liveIP, online := onlineMap[s.Username]; online {
					s.IsOnline = true
					s.OnlineIP = liveIP
				}

				// Calculate exact real-time status: active, expired_time, expired_quota, expired_both, disabled
				now := time.Now()
				isTimeExpired := (exp.Valid && now.After(exp.Time)) || s.LoanActive
				isQuotaExpired := s.QuotaBytes > 0 && s.RemainingBytes <= 0

				s.IsTimeExpired = isTimeExpired
				s.IsQuotaExpired = isQuotaExpired

				if s.Status == "disabled" || s.Status == "suspended" {
					s.StatusCode = "disabled"
					s.StatusText = "معطل"
				} else if s.LoanActive {
					// Subscribers on active loan are expired subscribers granted emergency grace period
					s.StatusCode = "expired_time"
					s.StatusText = "منتهي وقت"
				} else if isTimeExpired && isQuotaExpired {
					s.StatusCode = "expired_both"
					s.StatusText = "منتهي وقت وكوتة"
				} else if isTimeExpired {
					s.StatusCode = "expired_time"
					s.StatusText = "منتهي وقت"
				} else if isQuotaExpired {
					s.StatusCode = "expired_quota"
					s.StatusText = "منتهي كوتة"
				} else if s.Status == "expired" {
					s.StatusCode = "expired_time"
					s.StatusText = "منتهي وقت"
				} else {
					s.StatusCode = "active"
					s.StatusText = "نشط"
				}

				list = append(list, s)
			}
		}
	}

	fromIndex := offset + 1
	toIndex := offset + len(list)
	if totalCount == 0 {
		fromIndex = 0
		toIndex = 0
	}

	profiles, _ := bp.db.GetProfiles()
	resellersList, _ := bp.db.GetResellers()

	var expiredTimeCount, expiredQuotaCount, loanActiveCount int
	if resellerFilter > 0 {
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.reseller_id = ? AND ((s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1)`, resellerFilter).Scan(&expiredTimeCount)
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.reseller_id = ? AND s.quota_bytes > 0 AND (SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = s.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes`, resellerFilter).Scan(&expiredQuotaCount)
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.reseller_id = ? AND s.loan_active = 1`, resellerFilter).Scan(&loanActiveCount)
	} else {
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE (s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1`).Scan(&expiredTimeCount)
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.quota_bytes > 0 AND (SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = s.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes`).Scan(&expiredQuotaCount)
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.loan_active = 1`).Scan(&loanActiveCount)
	}

	_ = bp.renderer.Render(w, r, "subscribers/list", map[string]interface{}{
		"Title":             "إدارة المشتركين والعقود",
		"ActiveMenu":        "subscribers",
		"CurrentUser":       middleware.GetUser(r),
		"Subscribers":       list,
		"SearchQuery":       search,
		"FilterStatus":      statusFilter,
		"SelectedReseller":  resellerFilter,
		"Resellers":         resellersList,
		"ResellerPerms":     resellerPerms,
		"OnlineCount":       onlineCount,
		"ExpiredTimeCount":  expiredTimeCount,
		"ExpiredQuotaCount": expiredQuotaCount,
		"LoanActiveCount":   loanActiveCount,
		"Page":             page,
		"Limit":            limit,
		"TotalCount":       totalCount,
		"TotalPages":       totalPages,
		"HasPrev":          page > 1,
		"HasNext":          page < totalPages,
		"PrevPage":         page - 1,
		"NextPage":         page + 1,
		"FromIndex":        fromIndex,
		"ToIndex":          toIndex,
		"PageNumbers":      getPageNumbers(page, totalPages),
		"PageSizes":        []int{10, 25, 50, 100, 200, 500},
		"Profiles":         profiles,
	})
}

func (bp *Blueprint) BulkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	_ = r.ParseForm()
	action := r.FormValue("action")
	usernames := r.Form["usernames"]
	if len(usernames) == 0 {
		raw := r.FormValue("usernames_csv")
		if raw != "" {
			parts := strings.Split(raw, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					usernames = append(usernames, p)
				}
			}
		}
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")

	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s", limit, page, q)

	if len(usernames) == 0 {
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	switch action {
	case "delete":
		_ = bp.db.BulkDeleteSubscribers(usernames)
	case "renew":
		profile := strings.TrimSpace(r.FormValue("profile"))
		isPaid := r.FormValue("payment_status") != "unpaid"
		adminUser := "admin"
		if u := middleware.GetUser(r); u != nil {
			adminUser = u.Username
		}
		for _, u := range usernames {
			_ = bp.db.RenewSubscriberBilling(u, profile, 0, isPaid, "bulk", "تجديد اشتراك جماعي", adminUser)
		}
	case "add_quota":
		quotaGB, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("quota_gb")), 64)
		price, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price")), 64)
		isPaid := r.FormValue("payment_status") != "unpaid"
		adminUser := "admin"
		if u := middleware.GetUser(r); u != nil {
			adminUser = u.Username
		}
		if quotaGB > 0 {
			for _, u := range usernames {
				_ = bp.db.AddQuotaBilling(u, quotaGB, price, isPaid, "bulk", "إضافة كوتة جماعية", adminUser)
			}
		}
	case "add_debt":
		amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("debt_amount")), 64)
		_ = bp.db.BulkAddDebt(usernames, amount)
	case "settle_loan":
		settleType := r.FormValue("settle_type") // "renew" or "close"
		isPaid := r.FormValue("payment_status") != "unpaid"
		paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
		if paymentMethod == "" {
			paymentMethod = "cash"
		}
		adminUser := "admin"
		if u := middleware.GetUser(r); u != nil {
			adminUser = u.Username
		}
		for _, u := range usernames {
			if settleType == "close" {
				_ = bp.db.CloseSubscriberLoan(u, adminUser)
			} else {
				_ = bp.db.RenewSubscriberBilling(u, "", 0, isPaid, paymentMethod, "سداد سلفة وتجديد جماعي", adminUser)
			}
		}
	case "terminate":
		adminUser := "admin"
		if u := middleware.GetUser(r); u != nil {
			adminUser = u.Username
		}
		for _, u := range usernames {
			_ = bp.db.TerminateSubscription(u, adminUser)
		}
	case "change_profile":
		newProfile := strings.TrimSpace(r.FormValue("new_profile"))
		adminUser := "admin"
		isAdmin := false
		if u := middleware.GetUser(r); u != nil {
			adminUser = u.Username
			isAdmin = (u.Role == "admin")
		}
		for _, u := range usernames {
			_ = bp.db.ChangeSubscriberProfile(u, newProfile, adminUser, isAdmin)
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) TerminateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	statusFilter := r.FormValue("status")
	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, url.QueryEscape(q), url.QueryEscape(statusFilter))

	if username == "" {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("اسم المشترك مطلوب"), http.StatusFound)
		return
	}

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	err := bp.db.TerminateSubscription(username, adminUser)
	if err != nil {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("تعذر إنهاء الاشتراك: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تم إنهاء اشتراك المشترك (%s) وتصفير كوتته وفصل اتصاله بنجاح", username)), http.StatusFound)
}

func (bp *Blueprint) ChangeProfileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	newProfile := strings.TrimSpace(r.FormValue("new_profile"))
	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	statusFilter := r.FormValue("status")
	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, url.QueryEscape(q), url.QueryEscape(statusFilter))

	if username == "" || newProfile == "" {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("اسم المشترك والبروفايل الجديد مطلوبان"), http.StatusFound)
		return
	}

	adminUser := "admin"
	isAdmin := false
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
		isAdmin = (u.Role == "admin")
	}

	err := bp.db.ChangeSubscriberProfile(username, newProfile, adminUser, isAdmin)
	if err != nil {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تم تحويل بروفايل المشترك (%s) إلى (%s) مع إنهاء الاشتراك وتصفير الكوتة بنجاح", username, newProfile)), http.StatusFound)
}

func (bp *Blueprint) SettleLoanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	settleType := r.FormValue("settle_type") // "renew" or "close"
	isPaid := r.FormValue("payment_status") != "unpaid"
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	if paymentMethod == "" {
		paymentMethod = "cash"
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	statusFilter := r.FormValue("status")
	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, url.QueryEscape(q), url.QueryEscape(statusFilter))

	if username == "" {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("اسم المشترك مطلوب"), http.StatusFound)
		return
	}

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	if settleType == "close" {
		err := bp.db.CloseSubscriberLoan(username, adminUser)
		if err != nil {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("تعذر إغلاق السلفة: "+err.Error()), http.StatusFound)
			return
		}
		http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تم إنهاء وإغلاق السلفة للمشترك (%s) بنجاح", username)), http.StatusFound)
	} else {
		err := bp.db.RenewSubscriberBilling(username, "", 0, isPaid, paymentMethod, "سداد سلفة وتجديد باقة", adminUser)
		if err != nil {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("تعذر تسوية السلفة والتجديد: "+err.Error()), http.StatusFound)
			return
		}
		http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تم سداد السلفة وتجديد باقة المشترك (%s) بنجاح وتطبيق الخصم", username)), http.StatusFound)
	}
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		profiles, _ := bp.db.GetProfiles()
		resellersList, _ := bp.db.GetResellers()

		_ = bp.renderer.Render(w, r, "subscribers/add", map[string]interface{}{
			"Title":       "إضافة مشترك جديد",
			"ActiveMenu":  "subscribers",
			"CurrentUser": middleware.GetUser(r),
			"Profiles":    profiles,
			"Resellers":   resellersList,
		})
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	address := strings.TrimSpace(r.FormValue("address"))
	contractNo := strings.TrimSpace(r.FormValue("contract_no"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	staticIP := strings.TrimSpace(r.FormValue("static_ip"))
	callingMAC := strings.TrimSpace(r.FormValue("calling_mac"))
	debtStr := strings.TrimSpace(r.FormValue("debt"))
	quotaGBStr := strings.TrimSpace(r.FormValue("quota_gb"))
	expStr := strings.TrimSpace(r.FormValue("expiration_date"))
	resellerID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("reseller_id")), 10, 64)

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.Role == "reseller" {
		resellerID = currentUser.ID
	}

	// Check if subscriber exists and if loan is active, block profile modification for non-admins
	currentSub, err := bp.db.GetSubscriber(username)
	if err == nil && currentSub != nil {
		if currentSub.LoanActive && profile != currentSub.ProfileName {
			if currentUser == nil || currentUser.Role != "admin" {
				http.Redirect(w, r, "/subscribers?error="+url.QueryEscape("لا يمكن تغيير باقة المشترك أثناء فترة السلفة النشطة إلا بواسطة مدير النظام (Admin)"), http.StatusFound)
				return
			}
		}
	}

	if username == "" || password == "" {
		http.Redirect(w, r, "/subscribers/add?error=missing_fields", http.StatusFound)
		return
	}

	if fullName == "" {
		fullName = username
	}

	debt, _ := strconv.ParseFloat(debtStr, 64)
	quotaGB, _ := strconv.ParseFloat(quotaGBStr, 64)
	var quotaBytes int64
	if quotaGB > 0 {
		quotaBytes = int64(quotaGB * 1024 * 1024 * 1024)
	}

	var expNullTime sql.NullTime
	if expStr != "" {
		// Supports YYYY-MM-DDTHH:MM or YYYY-MM-DD
		t, err := time.Parse("2006-01-02T15:04", expStr)
		if err != nil {
			t, err = time.Parse("2006-01-02", expStr)
		}
		if err == nil {
			expNullTime = sql.NullTime{Time: t, Valid: true}
		}
	}

	sub := &models.Subscriber{
		Username:       username,
		ContractNo:     contractNo,
		FullName:       fullName,
		Phone:          phone,
		Address:        address,
		Debt:           debt,
		ProfileName:    profile,
		StaticIP:       staticIP,
		CallingMAC:     callingMAC,
		QuotaBytes:     quotaBytes,
		ExpirationDate: expNullTime,
		Status:         "active",
		ResellerID:     resellerID,
	}

	if err := bp.db.AddSubscriberCRM(sub, password); err != nil {
		fmt.Printf("[Subscribers] Error creating subscriber: %v\n", err)
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

func (bp *Blueprint) RenewHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	if username == "" {
		username = strings.TrimSpace(r.URL.Query().Get("username"))
	}
	if profile == "" {
		profile = strings.TrimSpace(r.URL.Query().Get("profile"))
	}

	priceStr := strings.TrimSpace(r.FormValue("price"))
	price, _ := strconv.ParseFloat(priceStr, 64)
	isPaid := r.FormValue("payment_status") != "unpaid"
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	currentUser := middleware.GetUser(r)
	adminUser := "admin"
	if currentUser != nil {
		adminUser = currentUser.Username
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")

	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)

	if username != "" {
		if currentUser != nil && currentUser.Role == "reseller" {
			if err := bp.db.ResellerRenewSubscriber(currentUser.ID, username, profile, isPaid, currentUser.Username); err != nil {
				fmt.Printf("[Subscribers] Reseller Renew Error: %v\n", err)
				http.Redirect(w, r, redirectURL+"&error="+err.Error(), http.StatusFound)
				return
			}
		} else {
			if err := bp.db.RenewSubscriberBilling(username, profile, price, isPaid, paymentMethod, notes, adminUser); err != nil {
				fmt.Printf("[Subscribers] Error renewing subscriber %s: %v\n", username, err)
			}
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) AddQuotaHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	quotaGB, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("quota_gb")), 64)
	price, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price")), 64)
	isPaid := r.FormValue("payment_status") != "unpaid"
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")

	if username != "" && quotaGB > 0 {
		_ = bp.db.AddQuotaBilling(username, quotaGB, price, isPaid, paymentMethod, notes, adminUser)
	}

	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) PayDebtHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")

	if username != "" && amount > 0 {
		_ = bp.db.PaySubscriberDebt(username, amount, paymentMethod, notes, adminUser)
	}

	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		username = strings.TrimSpace(r.FormValue("username"))
	}

	if r.Method == http.MethodGet {
		sub, err := bp.db.GetSubscriber(username)
		if err != nil {
			http.Redirect(w, r, "/subscribers", http.StatusFound)
			return
		}

		profiles, _ := bp.db.GetProfiles()
		resellersList, _ := bp.db.GetResellers()

		var quotaGB float64
		if sub.QuotaBytes > 0 {
			quotaGB = float64(sub.QuotaBytes) / (1024 * 1024 * 1024)
		}

		var expStr string
		if sub.ExpirationDate.Valid {
			expStr = sub.ExpirationDate.Time.Format("2006-01-02T15:04")
		}

		_ = bp.renderer.Render(w, r, "subscribers/edit", map[string]interface{}{
			"Title":          "تعديل بيانات المشترك: " + sub.FullName,
			"ActiveMenu":     "subscribers",
			"CurrentUser":    middleware.GetUser(r),
			"Sub":            sub,
			"Profiles":       profiles,
			"Resellers":      resellersList,
			"QuotaGB":        quotaGB,
			"ExpirationDate": expStr,
		})
		return
	}

	password := strings.TrimSpace(r.FormValue("password"))
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	address := strings.TrimSpace(r.FormValue("address"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	staticIP := strings.TrimSpace(r.FormValue("static_ip"))
	callingMAC := strings.TrimSpace(r.FormValue("calling_mac"))
	debtStr := strings.TrimSpace(r.FormValue("debt"))
	quotaGBStr := strings.TrimSpace(r.FormValue("quota_gb"))
	expStr := strings.TrimSpace(r.FormValue("expiration_date"))
	status := strings.TrimSpace(r.FormValue("status"))
	resellerID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("reseller_id")), 10, 64)

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.Role == "reseller" {
		resellerID = currentUser.ID
	}

	// Check if subscriber exists and if loan is active, block profile modification for non-admins
	currentSub, err := bp.db.GetSubscriber(username)
	if err == nil && currentSub != nil {
		if currentSub.LoanActive && profile != currentSub.ProfileName {
			if currentUser == nil || currentUser.Role != "admin" {
				http.Redirect(w, r, "/subscribers?error="+url.QueryEscape("لا يمكن تغيير باقة المشترك أثناء فترة السلفة النشطة إلا بواسطة مدير النظام (Admin)"), http.StatusFound)
				return
			}
		}
	}

	if status == "" {
		status = "active"
	}

	debt, _ := strconv.ParseFloat(debtStr, 64)
	quotaGB, _ := strconv.ParseFloat(quotaGBStr, 64)
	var quotaBytes int64
	if quotaGB > 0 {
		quotaBytes = int64(quotaGB * 1024 * 1024 * 1024)
	}

	var expNullTime sql.NullTime
	if expStr != "" {
		t, err := time.Parse("2006-01-02T15:04", expStr)
		if err != nil {
			t, err = time.Parse("2006-01-02", expStr)
		}
		if err == nil {
			expNullTime = sql.NullTime{Time: t, Valid: true}
		}
	}

	sub := &models.Subscriber{
		Username:       username,
		FullName:       fullName,
		Phone:          phone,
		Address:        address,
		Debt:           debt,
		ProfileName:    profile,
		StaticIP:       staticIP,
		CallingMAC:     callingMAC,
		QuotaBytes:     quotaBytes,
		ExpirationDate: expNullTime,
		Status:         status,
		ResellerID:     resellerID,
	}

	_ = bp.db.UpdateSubscriberCRM(sub, password)
	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) GrantLoanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	currentUser := middleware.GetUser(r)
	adminUser := "admin"
	if currentUser != nil {
		adminUser = currentUser.Username
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")
	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)

	if username == "" {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("اسم المشترك مطلوب"), http.StatusFound)
		return
	}

	if currentUser != nil && currentUser.Role == "reseller" {
		sub, err := bp.db.GetSubscriber(username)
		if err != nil || sub.ResellerID != currentUser.ID {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("ليس لديك صلاحية منح سلفة لهذا المشترك"), http.StatusFound)
			return
		}
	}

	_, err := bp.db.GrantSubscriberLoan(username, adminUser)
	if err != nil {
		http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تم منح السلفة للمشترك (%s) بنجاح وتفعيل الخدمة وفك الحظر فوراً", username)), http.StatusFound)
}

func (bp *Blueprint) LiveTrafficHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// If list=1 is requested, return lightweight array of all subscribers for the selection dropdown
	if r.URL.Query().Get("list") == "1" {
		list, err := bp.db.GetAllSubscribersForLiveMonitor()
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": err.Error(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "success",
			"subscribers": list,
		})
		return
	}

	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "اسم المستخدم مطلوب",
		})
		return
	}

	traffic, err := bp.db.GetSubscriberLiveTraffic(username)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data":   traffic,
	})
}

func (bp *Blueprint) DisconnectSessionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		username = strings.TrimSpace(r.FormValue("username"))
	}
	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "اسم المستخدم مطلوب",
		})
		return
	}

	err := bp.db.DisconnectSubscriberSession(username)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "تم قطع اتصال الجلسة وتجديد الاتصال بنجاح",
	})
}
