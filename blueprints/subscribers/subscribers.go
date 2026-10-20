package subscribers

import (
	"database/sql"
	"encoding/json"
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

type SubscriberItem struct {
	ID             int64
	ContractNo     string
	FullName       string
	Username       string
	Password       string
	Phone          string
	Address        string
	Debt           float64
	Profile        string
	IPAddress      string
	QuotaBytes     int64
	QuotaGB        float64
	ExpirationDate string
	Status         string
	IsOnline       bool
	OnlineIP       string
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
		COALESCE(s.status, 'active')
	FROM radcheck rc
	LEFT JOIN subscribers s ON rc.username = s.username
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
			); err == nil {
				if s.QuotaBytes > 0 {
					s.QuotaGB = float64(s.QuotaBytes) / (1024 * 1024 * 1024)
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

	_ = bp.renderer.Render(w, "subscribers/list", map[string]interface{}{
		"Title":        "إدارة المشتركين والعقود",
		"ActiveMenu":   "subscribers",
		"CurrentUser":  middleware.GetUser(r),
		"Subscribers":  list,
		"SearchQuery":  search,
		"FilterStatus": statusFilter,
		"OnlineCount":  onlineCount,
		"Page":         page,
		"Limit":        limit,
		"TotalCount":   totalCount,
		"TotalPages":   totalPages,
		"HasPrev":      page > 1,
		"HasNext":      page < totalPages,
		"PrevPage":     page - 1,
		"NextPage":     page + 1,
		"FromIndex":    fromIndex,
		"ToIndex":      toIndex,
		"PageNumbers":  getPageNumbers(page, totalPages),
		"PageSizes":    []int{10, 25, 50, 100, 200, 500},
		"Profiles":     profiles,
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
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		profiles, _ := bp.db.GetProfiles()

		_ = bp.renderer.Render(w, "subscribers/add", map[string]interface{}{
			"Title":       "إضافة مشترك جديد",
			"ActiveMenu":  "subscribers",
			"CurrentUser": middleware.GetUser(r),
			"Profiles":    profiles,
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

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	limit := r.FormValue("limit")
	page := r.FormValue("page")
	q := r.FormValue("q")
	status := r.FormValue("status")

	if username != "" {
		if err := bp.db.RenewSubscriberBilling(username, profile, price, isPaid, paymentMethod, notes, adminUser); err != nil {
			fmt.Printf("[Subscribers] Error renewing subscriber %s: %v\n", username, err)
		}
	}

	redirectURL := fmt.Sprintf("/subscribers?limit=%s&page=%s&q=%s&status=%s", limit, page, q, status)
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

		var quotaGB float64
		if sub.QuotaBytes > 0 {
			quotaGB = float64(sub.QuotaBytes) / (1024 * 1024 * 1024)
		}

		var expStr string
		if sub.ExpirationDate.Valid {
			expStr = sub.ExpirationDate.Time.Format("2006-01-02T15:04")
		}

		_ = bp.renderer.Render(w, "subscribers/edit", map[string]interface{}{
			"Title":          "تعديل بيانات المشترك: " + sub.FullName,
			"ActiveMenu":     "subscribers",
			"CurrentUser":    middleware.GetUser(r),
			"Sub":            sub,
			"Profiles":       profiles,
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
	}

	_ = bp.db.UpdateSubscriberCRM(sub, password)
	http.Redirect(w, r, "/subscribers", http.StatusFound)
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
