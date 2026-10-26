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

// checkSubscriberAccess verifies if the authenticated user has permission to manage the subscriber
func (bp *Blueprint) checkSubscriberAccess(w http.ResponseWriter, r *http.Request, username string) bool {
	u := middleware.GetUser(r)
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return false
	}
	if u.Role == "admin" || u.Role == "staff" {
		return true
	}
	if u.Role == "reseller" {
		if bp.db.OwnsSubscriber(u.ID, username) {
			return true
		}
		http.Error(w, "غير مصرح: هذا المشترك غير مسجل في نطاق حسابك كموزع", http.StatusForbidden)
		return false
	}
	http.Error(w, "غير مصرح", http.StatusForbidden)
	return false
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
	Balance           float64
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

	// 3. Count total matching subscribers (Fast query driving from subscribers table)
	countQuery := `SELECT COUNT(*) FROM subscribers s`
	var countWhere []string
	var countArgs []interface{}

	if search != "" {
		countWhere = append(countWhere, "(s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)")
		searchArg := "%" + search + "%"
		countArgs = append(countArgs, searchArg, searchArg, searchArg, searchArg)
	}

	if statusFilter == "online" {
		countWhere = append(countWhere, "s.username IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)")
	} else if statusFilter == "offline" {
		countWhere = append(countWhere, "s.username NOT IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)")
	} else if statusFilter == "expired_time" {
		countWhere = append(countWhere, "((s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1)")
	} else if statusFilter == "expired_quota" {
		countWhere = append(countWhere, fmt.Sprintf("s.quota_bytes > 0 AND (SELECT %s FROM radacct WHERE username = s.username AND (s.last_renewed_at IS NULL OR acctstarttime >= s.last_renewed_at)) >= s.quota_bytes", database.RadacctSumBytesSQL))
	} else if statusFilter == "loan" {
		countWhere = append(countWhere, "s.loan_active = 1")
	}

	if resellerFilter > 0 {
		countWhere = append(countWhere, "s.reseller_id = ?")
		countArgs = append(countArgs, resellerFilter)
	}

	if len(countWhere) > 0 {
		countQuery += " WHERE " + strings.Join(countWhere, " AND ")
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

	// 4. Query paginated subscribers directly from subscribers table
	query := `SELECT 
		s.id,
		COALESCE(s.contract_no, '-'),
		COALESCE(s.full_name, s.username),
		s.username,
		COALESCE(rc.value, ''),
		COALESCE(s.phone, '-'),
		COALESCE(s.address, '-'),
		COALESCE(s.debt, 0.00),
		COALESCE(s.balance, 0.00),
		COALESCE(s.profile_name, ''),
		COALESCE(s.static_ip, ''),
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
	FROM subscribers s
	LEFT JOIN radcheck rc ON s.username = rc.username AND rc.attribute = 'Cleartext-Password'
	LEFT JOIN resellers res ON s.reseller_id = res.id`

	var whereClauses []string
	var args []interface{}
	if search != "" {
		whereClauses = append(whereClauses, "(s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)")
		searchArg := "%" + search + "%"
		args = append(args, searchArg, searchArg, searchArg, searchArg)
	}

	if statusFilter == "online" {
		whereClauses = append(whereClauses, "s.username IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)")
	} else if statusFilter == "offline" {
		whereClauses = append(whereClauses, "s.username NOT IN (SELECT username FROM radacct WHERE acctstoptime IS NULL)")
	} else if statusFilter == "expired_time" {
		whereClauses = append(whereClauses, "((s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1)")
	} else if statusFilter == "expired_quota" {
		whereClauses = append(whereClauses, fmt.Sprintf("s.quota_bytes > 0 AND (SELECT %s FROM radacct WHERE username = s.username AND (s.last_renewed_at IS NULL OR acctstarttime >= s.last_renewed_at)) >= s.quota_bytes", database.RadacctSumBytesSQL))
	} else if statusFilter == "loan" {
		whereClauses = append(whereClauses, "s.loan_active = 1")
	}

	if resellerFilter > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerFilter)
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	query += " ORDER BY s.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := bp.db.Query(query, args...)
	var list []SubscriberItem
	now := time.Now()
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
				&s.Balance,
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

				s.IsTimeExpired = (exp.Valid && now.After(exp.Time)) || s.LoanActive

				list = append(list, s)
			}
		}
	}

	// 5. BULK FETCH USAGE (1 single bulk query for all subscribers on page - eliminates N+1 latency!)
	if len(list) > 0 {
		uArgs := make([]interface{}, len(list))
		placeholders := make([]string, len(list))
		for i, it := range list {
			uArgs[i] = it.Username
			placeholders[i] = "?"
		}

		bulkQ := fmt.Sprintf(`SELECT r.username, %s
			FROM radacct r
			WHERE r.username IN (%s)
			GROUP BY r.username`, database.RadacctAliasedSumBytesSQL, strings.Join(placeholders, ","))

		uRows, err := bp.db.Query(bulkQ, uArgs...)
		if err == nil {
			usageMap := make(map[string]int64)
			for uRows.Next() {
				var u string
				var b int64
				if err := uRows.Scan(&u, &b); err == nil {
					usageMap[u] = b
				}
			}
			uRows.Close()

			for i := range list {
				usedBytes := usageMap[list[i].Username]
				list[i].UsedBytes = usedBytes
				list[i].UsedGB = float64(usedBytes) / (1024 * 1024 * 1024)
				list[i].UsedDisplay = database.FormatBytesHuman(usedBytes)

				if list[i].QuotaBytes > 0 {
					remBytes := list[i].QuotaBytes - usedBytes
					if remBytes < 0 {
						remBytes = 0
					}
					list[i].RemainingBytes = remBytes
					list[i].RemainingGB = float64(remBytes) / (1024 * 1024 * 1024)
					list[i].RemainingDisplay = database.FormatBytesHuman(remBytes)
					list[i].UsagePercent = (float64(usedBytes) / float64(list[i].QuotaBytes)) * 100.0
					if list[i].UsagePercent > 100.0 {
						list[i].UsagePercent = 100.0
					}
					list[i].IsQuotaExpired = remBytes <= 0
				} else {
					list[i].RemainingBytes = -1
					list[i].RemainingGB = -1
					list[i].RemainingDisplay = "مفتوح ∞"
					list[i].UsagePercent = 0.0
					list[i].IsQuotaExpired = false
				}

				if list[i].Status == "disabled" || list[i].Status == "suspended" {
					list[i].StatusCode = "disabled"
					list[i].StatusText = "معطل"
				} else if list[i].LoanActive {
					list[i].StatusCode = "expired_time"
					list[i].StatusText = "منتهي وقت"
				} else if list[i].IsTimeExpired && list[i].IsQuotaExpired {
					list[i].StatusCode = "expired_both"
					list[i].StatusText = "منتهي وقت وكوتة"
				} else if list[i].IsTimeExpired {
					list[i].StatusCode = "expired_time"
					list[i].StatusText = "منتهي وقت"
				} else if list[i].IsQuotaExpired {
					list[i].StatusCode = "expired_quota"
					list[i].StatusText = "منتهي كوتة"
				} else if list[i].Status == "expired" {
					list[i].StatusCode = "expired_time"
					list[i].StatusText = "منتهي وقت"
				} else {
					list[i].StatusCode = "active"
					list[i].StatusText = "نشط"
				}
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
		_ = bp.db.QueryRow(fmt.Sprintf(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.reseller_id = ? AND s.quota_bytes > 0 AND (SELECT %s FROM radacct WHERE username = s.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes`, database.RadacctSumBytesSQL), resellerFilter).Scan(&expiredQuotaCount)
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.reseller_id = ? AND s.loan_active = 1`, resellerFilter).Scan(&loanActiveCount)
	} else {
		_ = bp.db.QueryRow(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE (s.expiration_date IS NOT NULL AND s.expiration_date < NOW()) OR s.loan_active = 1`).Scan(&expiredTimeCount)
		_ = bp.db.QueryRow(fmt.Sprintf(`SELECT COUNT(DISTINCT s.id) FROM subscribers s WHERE s.quota_bytes > 0 AND (SELECT %s FROM radacct WHERE username = s.username AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)) >= s.quota_bytes`, database.RadacctSumBytesSQL)).Scan(&expiredQuotaCount)
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
		"Page":              page,
		"Limit":             limit,
		"TotalCount":        totalCount,
		"TotalPages":        totalPages,
		"HasPrev":           page > 1,
		"HasNext":           page < totalPages,
		"PrevPage":          page - 1,
		"NextPage":          page + 1,
		"FromIndex":         fromIndex,
		"ToIndex":           toIndex,
		"PageNumbers":       getPageNumbers(page, totalPages),
		"PageSizes":         []int{10, 25, 50, 100, 200, 500},
		"Profiles":          profiles,
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

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.Role == "reseller" {
		var owned []string
		for _, u := range usernames {
			if bp.db.OwnsSubscriber(currentUser.ID, u) {
				owned = append(owned, u)
			}
		}
		if len(owned) == 0 {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape("ليس لديك صلاحية على أي من المشتركين المحددين"), http.StatusFound)
			return
		}
		usernames = owned
	}

	if len(usernames) == 0 {
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	successCount := 0
	failCount := 0
	var lastErr string

	switch action {
	case "delete":
		if err := bp.db.BulkDeleteSubscribers(usernames); err != nil {
			failCount = len(usernames)
			lastErr = err.Error()
		} else {
			successCount = len(usernames)
		}
	case "renew":
		profile := strings.TrimSpace(r.FormValue("profile"))
		isPaid := r.FormValue("payment_status") != "unpaid"
		adminUser := "admin"
		if currentUser != nil {
			adminUser = currentUser.Username
		}
		for _, u := range usernames {
			var err error
			if currentUser != nil && currentUser.Role == "reseller" {
				err = bp.db.ResellerRenewSubscriber(currentUser.ID, u, profile, isPaid, currentUser.Username)
			} else {
				err = bp.db.RenewSubscriberBilling(u, profile, 0, isPaid, "bulk", "تجديد اشتراك جماعي", adminUser)
			}
			if err != nil {
				failCount++
				lastErr = err.Error()
			} else {
				successCount++
			}
		}
	case "add_quota":
		quotaGB, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("quota_gb")), 64)
		price, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price")), 64)
		isPaid := r.FormValue("payment_status") != "unpaid"
		adminUser := "admin"
		if currentUser != nil {
			adminUser = currentUser.Username
		}
		if quotaGB > 0 {
			for _, u := range usernames {
				var err error
				if currentUser != nil && currentUser.Role == "reseller" {
					err = bp.db.ResellerAddQuota(currentUser.ID, u, quotaGB, price, isPaid, currentUser.Username)
				} else {
					err = bp.db.AddQuotaBilling(u, quotaGB, price, isPaid, "bulk", "إضافة كوتة جماعية", adminUser)
				}
				if err != nil {
					failCount++
					lastErr = err.Error()
				} else {
					successCount++
				}
			}
		}
	case "add_debt":
		amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("debt_amount")), 64)
		if amount > 0 {
			summary, err := bp.db.BulkAddDebt(usernames, amount)
			successCount = summary.Successful
			failCount = summary.Failed
			if err != nil {
				lastErr = err.Error()
			}
		}
	case "settle_loan":
		settleType := r.FormValue("settle_type") // "renew" or "close"
		isPaid := r.FormValue("payment_status") != "unpaid"
		paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
		if paymentMethod == "" {
			paymentMethod = "cash"
		}
		adminUser := "admin"
		if currentUser != nil {
			adminUser = currentUser.Username
		}
		for _, u := range usernames {
			var err error
			if settleType == "close" {
				err = bp.db.CloseSubscriberLoan(u, adminUser)
			} else {
				if currentUser != nil && currentUser.Role == "reseller" {
					err = bp.db.ResellerRenewSubscriber(currentUser.ID, u, "", isPaid, currentUser.Username)
				} else {
					err = bp.db.RenewSubscriberBilling(u, "", 0, isPaid, paymentMethod, "سداد سلفة وتجديد جماعي", adminUser)
				}
			}
			if err != nil {
				failCount++
				lastErr = err.Error()
			} else {
				successCount++
			}
		}
	case "terminate":
		adminUser := "admin"
		if currentUser != nil {
			adminUser = currentUser.Username
		}
		for _, u := range usernames {
			if err := bp.db.TerminateSubscription(u, adminUser); err != nil {
				failCount++
				lastErr = err.Error()
			} else {
				successCount++
			}
		}
	case "change_profile":
		newProfile := strings.TrimSpace(r.FormValue("new_profile"))
		adminUser := "admin"
		isAdmin := false
		if currentUser != nil {
			adminUser = currentUser.Username
			isAdmin = (currentUser.Role == "admin")
		}
		for _, u := range usernames {
			if err := bp.db.ChangeSubscriberProfile(u, newProfile, adminUser, isAdmin); err != nil {
				failCount++
				lastErr = err.Error()
			} else {
				successCount++
			}
		}
	}

	if failCount > 0 {
		http.Redirect(w, r, fmt.Sprintf("%s&error=%s", redirectURL, url.QueryEscape(fmt.Sprintf("تم تنفيذ %d وفشل %d: %s", successCount, failCount, lastErr))), http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s&msg=%s", redirectURL, url.QueryEscape(fmt.Sprintf("تم تنفيذ العملية بنجاح لـ %d مشترك", successCount))), http.StatusFound)
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
	if !bp.checkSubscriberAccess(w, r, username) {
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
	if !bp.checkSubscriberAccess(w, r, username) {
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
	if !bp.checkSubscriberAccess(w, r, username) {
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
	if password == "" {
		if existing, _ := bp.db.GetSubscriber(username); existing != nil && existing.Password != "" {
			password = existing.Password
		} else {
			password = "0000"
		}
	}
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	address := strings.TrimSpace(r.FormValue("address"))
	contractNo := strings.TrimSpace(r.FormValue("contract_no"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	staticIP := strings.TrimSpace(r.FormValue("static_ip"))
	callingMAC := strings.TrimSpace(r.FormValue("calling_mac"))
	debtStr := strings.TrimSpace(r.FormValue("debt"))
	balanceStr := strings.TrimSpace(r.FormValue("balance"))
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
	balance, _ := strconv.ParseFloat(balanceStr, 64)
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
		Balance:        balance,
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
		http.Redirect(w, r, "/subscribers/add?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	if username == "" {
		username = strings.TrimSpace(r.URL.Query().Get("username"))
	}
	if username != "" {
		if !bp.checkSubscriberAccess(w, r, username) {
			return
		}
		_ = bp.db.DeleteSubscriber(username)
	}
	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) RenewHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	if username == "" {
		username = strings.TrimSpace(r.URL.Query().Get("username"))
	}
	if username != "" && !bp.checkSubscriberAccess(w, r, username) {
		return
	}
	profile := strings.TrimSpace(r.FormValue("profile"))
	if username == "" {
		username = strings.TrimSpace(r.URL.Query().Get("username"))
	}
	if profile == "" {
		profile = strings.TrimSpace(r.URL.Query().Get("profile"))
	}

	priceStr := strings.TrimSpace(r.FormValue("price"))
	price, _ := strconv.ParseFloat(priceStr, 64)
	paymentOption := strings.TrimSpace(r.FormValue("payment_option"))
	if paymentOption == "" {
		if r.FormValue("payment_status") == "unpaid" {
			paymentOption = "debt"
		} else {
			paymentOption = "cash"
		}
	}
	isPaid := paymentOption != "debt"
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
			if err := bp.db.RenewSubscriberWithPaymentOption(username, profile, price, paymentOption, paymentMethod, notes, adminUser); err != nil {
				fmt.Printf("[Subscribers] Error renewing subscriber %s: %v\n", username, err)
			}
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) AddBalanceHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	settleDebtFirst := r.FormValue("settle_debt_first") == "1" || r.FormValue("settle_debt_first") == "on" || r.FormValue("settle_debt_first") == "true"

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

	if username != "" && amount > 0 {
		if !bp.checkSubscriberAccess(w, r, username) {
			return
		}
		if currentUser != nil && currentUser.Role == "reseller" {
			newBal, newDebt, settled, err := bp.db.ResellerAddSubscriberBalance(currentUser.ID, username, amount, notes, currentUser.Username, settleDebtFirst)
			if err != nil {
				http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
				return
			}
			var msg string
			if settled > 0 {
				msg = fmt.Sprintf("تم شحن رصيد المشترك وخصم المبلغ من محفظتك! تم سداد %.2f ج.م من الدين (متبقي الدين: %.2f ج.م، رصيد المشترك: %.2f ج.م)", settled, newDebt, newBal)
			} else {
				msg = fmt.Sprintf("تم شحن رصيد المشترك بمبلغ %.2f ج.م وخصمها من محفظتك بنجاح!", amount)
			}
			http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(msg), http.StatusFound)
			return
		}

		newBal, newDebt, settled, err := bp.db.AddSubscriberBalance(username, amount, paymentMethod, notes, adminUser, settleDebtFirst)
		if err != nil {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
			return
		}
		var msg string
		if settled > 0 {
			msg = fmt.Sprintf("تم إيداع الرصيد بنجاح! تم سداد %.2f ج.م من المديونية (متبقي الدين: %.2f ج.م، رصيد المحفظة الجديد: %.2f ج.م)", settled, newDebt, newBal)
		} else {
			msg = fmt.Sprintf("تم شحن الرصيد بنجاح! رصيد المحفظة الحالي: %.2f ج.م", newBal)
		}
		http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) SettleDebtBalanceHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)

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

	creditReseller := (currentUser != nil && currentUser.Role == "admin") && (r.FormValue("credit_reseller") == "1" || r.FormValue("credit_reseller") == "true")

	if username != "" {
		if !bp.checkSubscriberAccess(w, r, username) {
			return
		}
		newBal, newDebt, err := bp.db.SettleDebtFromBalance(username, amount, adminUser, creditReseller)
		if err != nil {
			http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
			return
		}
		msg := fmt.Sprintf("تم سداد المديونية من رصيد المحفظة بنجاح! (متبقي الدين: %.2f ج.م، رصيد المحفظة المتبقي: %.2f ج.م)", newDebt, newBal)
		http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(msg), http.StatusFound)
		return
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

	if username != "" && quotaGB > 0 {
		if !bp.checkSubscriberAccess(w, r, username) {
			return
		}
		if currentUser != nil && currentUser.Role == "reseller" {
			if err := bp.db.ResellerAddQuota(currentUser.ID, username, quotaGB, price, isPaid, currentUser.Username); err != nil {
				http.Redirect(w, r, redirectURL+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
				return
			}
			http.Redirect(w, r, redirectURL+"&success="+url.QueryEscape(fmt.Sprintf("تمت إضافة كوتة %.1f GB للمشترك وخصم التكلفة من رصيدك بنجاح", quotaGB)), http.StatusFound)
			return
		}

		_ = bp.db.AddQuotaBilling(username, quotaGB, price, isPaid, paymentMethod, notes, adminUser)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) PayDebtHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	paymentMethod := strings.TrimSpace(r.FormValue("payment_method"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	currentUser := middleware.GetUser(r)
	adminUser := "admin"
	if currentUser != nil {
		adminUser = currentUser.Username
	}

	// Resellers can NEVER credit themselves when collecting debt from their subscriber
	creditReseller := false
	if currentUser != nil && currentUser.Role == "admin" {
		creditReseller = (r.FormValue("credit_reseller") == "1" || r.FormValue("credit_reseller") == "true" || r.FormValue("credit_reseller") == "on")
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")
	redirectURL := strings.TrimSpace(r.FormValue("redirect_url"))

	if redirectURL == "" {
		redirectURL = fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)
	}

	sep := "?"
	if strings.Contains(redirectURL, "?") {
		sep = "&"
	}

	if username != "" && amount > 0 {
		if !bp.checkSubscriberAccess(w, r, username) {
			return
		}
		if paymentMethod == "balance" {
			_, _, err := bp.db.SettleDebtFromBalance(username, amount, adminUser, creditReseller)
			if err != nil {
				http.Redirect(w, r, redirectURL+sep+"error="+url.QueryEscape(err.Error()), http.StatusFound)
				return
			}
			http.Redirect(w, r, redirectURL+sep+"success="+url.QueryEscape(fmt.Sprintf("تم سداد مبلغ %.2f ج.م من مديونية المشترك بنجاح", amount)), http.StatusFound)
			return
		} else {
			_, msg, err := bp.db.PaySubscriberDebt(username, amount, paymentMethod, notes, adminUser, creditReseller)
			if err != nil {
				http.Redirect(w, r, redirectURL+sep+"error="+url.QueryEscape(err.Error()), http.StatusFound)
				return
			}
			if msg != "" {
				http.Redirect(w, r, redirectURL+sep+"success="+url.QueryEscape(msg), http.StatusFound)
				return
			}
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		username = strings.TrimSpace(r.FormValue("username"))
	}
	if username == "" {
		http.Redirect(w, r, "/subscribers", http.StatusFound)
		return
	}
	if !bp.checkSubscriberAccess(w, r, username) {
		return
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
	if password == "" {
		if currentSub, _ := bp.db.GetSubscriber(username); currentSub != nil && currentSub.Password != "" {
			password = currentSub.Password
		}
	}
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	address := strings.TrimSpace(r.FormValue("address"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	staticIP := strings.TrimSpace(r.FormValue("static_ip"))
	callingMAC := strings.TrimSpace(r.FormValue("calling_mac"))
	debtStr := strings.TrimSpace(r.FormValue("debt"))
	balanceStr := strings.TrimSpace(r.FormValue("balance"))
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
	balance, _ := strconv.ParseFloat(balanceStr, 64)
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
		Balance:        balance,
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

	u := middleware.GetUser(r)
	if u != nil && u.Role == "reseller" && !bp.db.OwnsSubscriber(u.ID, username) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "غير مصرح لك بعرض بيانات هذا المشترك",
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

	u := middleware.GetUser(r)
	if u != nil && u.Role == "reseller" && !bp.db.OwnsSubscriber(u.ID, username) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "غير مصرح لك بفصل جلسة هذا المشترك",
		})
		return
	}

	succ, fail, err := bp.db.DisconnectSubscriberSession(username)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}
	if fail > 0 && succ == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "تعذر فصل جلسة المشترك من الراوتر (لم يستجب الراوتر)",
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("تم إرسال أمر فصل الجلسة بنجاح (نجح: %d، تعذر: %d)", succ, fail),
	})
}
