package portal

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
	secret   string
}

func New(db *database.DB, r *web.Renderer, secret string) *Blueprint {
	return &Blueprint{db: db, renderer: r, secret: secret}
}

// extractClientIP retrieves the real client IP address from proxy headers or RemoteAddr
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip := strings.TrimSpace(xri)
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// LoginHandler renders and handles subscriber portal login
func (bp *Blueprint) LoginHandler(w http.ResponseWriter, r *http.Request) {
	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings != nil && !settings.PortalEnabled {
		http.Error(w, "صفحة المشتركين معطلة حالياً من قبل إدارة الشبكة", http.StatusServiceUnavailable)
		return
	}

	u := middleware.GetUser(r)
	if u != nil {
		if u.Role == "subscriber" || u.Role == "admin" {
			http.Redirect(w, r, "/portal", http.StatusFound)
			return
		}
	}

	clientIP := extractClientIP(r)
	isLogout := (r.URL.Query().Get("logout") == "1")
	forceAuto := (r.URL.Query().Get("autologin") == "1")

	var detectedSub *models.Subscriber
	if settings != nil && settings.AllowIPAutoLogin && clientIP != "" {
		sub, err := bp.db.FindActiveSubscriberByIP(clientIP)
		if err == nil && sub != nil {
			prof, _ := bp.db.GetProfileByName(sub.ProfileName)
			if prof == nil || prof.PortalEnabled {
				detectedSub = sub
			}
		}
	}

	// Auto-login: If not an explicit logout action, and Auto Login is enabled with active detected user
	if detectedSub != nil && (forceAuto || (!isLogout && r.Method == http.MethodGet)) {
		token := middleware.GenerateToken(middleware.SessionUser{
			ID:       detectedSub.ID,
			Username: detectedSub.Username,
			Role:     "subscriber",
		}, bp.secret)

		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Expires:  time.Now().Add(7 * 24 * time.Hour),
		})

		http.Redirect(w, r, "/portal?autologin=1", http.StatusFound)
		return
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		errorMsg := r.URL.Query().Get("error")
		_ = bp.renderer.Render(w, r, "portal/login", map[string]interface{}{
			"Title":        "تسجيل دخول المشترك",
			"Settings":     settings,
			"Error":        errorMsg,
			"ClientIP":     clientIP,
			"DetectedUser": detectedSub,
			"IsLogout":     isLogout,
		})
		return
	}

	loginIdentifier := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))

	if loginIdentifier == "" || password == "" {
		http.Redirect(w, r, "/portal/login?error="+url.QueryEscape("يرجى إدخال اسم المستخدم وكلمة المرور"), http.StatusFound)
		return
	}

	username := loginIdentifier
	if settings != nil && settings.AllowPhoneLogin {
		var uByPhone string
		errP := bp.db.QueryRow("SELECT username FROM subscribers WHERE phone = ? AND phone != '' LIMIT 1", loginIdentifier).Scan(&uByPhone)
		if errP == nil && uByPhone != "" {
			username = uByPhone
		}
	}

	// Verify password in FreeRADIUS radcheck
	var subPass string
	err := bp.db.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", username).Scan(&subPass)
	if err != nil || subPass != password {
		errMsg := "اسم المستخدم أو كلمة المرور غير صحيحة"
		if settings != nil && settings.AllowPhoneLogin {
			errMsg = "اسم المستخدم / رقم الهاتف أو كلمة المرور غير صحيحة"
		}
		http.Redirect(w, r, "/portal/login?error="+url.QueryEscape(errMsg), http.StatusFound)
		return
	}

	// Verify subscriber exists in subscribers table
	sub, err := bp.db.GetSubscriber(username)
	if err != nil || sub == nil {
		http.Redirect(w, r, "/portal/login?error="+url.QueryEscape("بيانات المشترك غير مسجلة بالنظام"), http.StatusFound)
		return
	}

	// Check if the subscriber's profile permits subscriber portal access
	if sub.ProfileName != "" {
		prof, _ := bp.db.GetProfileByName(sub.ProfileName)
		if prof != nil && !prof.PortalEnabled {
			http.Redirect(w, r, "/portal/login?error="+url.QueryEscape("عفواً، صفحة المشترك غير متاحة لمشتركي باقة ("+prof.DisplayName+")"), http.StatusFound)
			return
		}
	}

	token := middleware.GenerateToken(middleware.SessionUser{
		ID:       sub.ID,
		Username: sub.Username,
		Role:     "subscriber",
	}, bp.secret)

	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
	})

	http.Redirect(w, r, "/portal", http.StatusFound)
}

// LogoutHandler logs out subscriber
func (bp *Blueprint) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/portal/login?logout=1", http.StatusFound)
}

// IndexHandler renders the full self-care portal
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	clientIP := extractClientIP(r)

	// If no active session, check if Auto Login by active IP is permitted
	if user == nil {
		settings, _ := bp.db.GetSubscriberPortalSettings()
		if settings != nil && settings.PortalEnabled && settings.AllowIPAutoLogin && clientIP != "" {
			sub, err := bp.db.FindActiveSubscriberByIP(clientIP)
			if err == nil && sub != nil {
				prof, _ := bp.db.GetProfileByName(sub.ProfileName)
				if prof == nil || prof.PortalEnabled {
					token := middleware.GenerateToken(middleware.SessionUser{
						ID:       sub.ID,
						Username: sub.Username,
						Role:     "subscriber",
					}, bp.secret)
					http.SetCookie(w, &http.Cookie{
						Name:     middleware.CookieName,
						Value:    token,
						Path:     "/",
						HttpOnly: true,
						Expires:  time.Now().Add(7 * 24 * time.Hour),
					})
					user = &middleware.SessionUser{
						ID:       sub.ID,
						Username: sub.Username,
						Role:     "subscriber",
					}
				}
			}
		}
	}

	if user == nil {
		http.Redirect(w, r, "/portal/login", http.StatusFound)
		return
	}

	username := user.Username
	// If admin is previewing another subscriber
	if user.Role == "admin" && r.URL.Query().Get("user") != "" {
		username = strings.TrimSpace(r.URL.Query().Get("user"))
	}

	data, err := bp.db.GetSubscriberPortalData(username)
	if err != nil {
		http.Error(w, "تعذر تحميل بيانات المشترك: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Check if non-admin subscriber's profile has portal access disabled
	if user.Role != "admin" && data.Profile.Name != "" && !data.Profile.PortalEnabled {
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})
		http.Redirect(w, r, "/portal/login?error="+url.QueryEscape("عفواً، صفحة المشترك غير متاحة لمشتركي باقة ("+data.Profile.DisplayName+")"), http.StatusFound)
		return
	}

	flashSuccess := r.URL.Query().Get("success")
	if r.URL.Query().Get("autologin") == "1" && flashSuccess == "" {
		flashSuccess = "تم تسجيل الدخول التلقائي بنجاح بحساب (" + username + ") بناءً على عنوان الآيبي الخاص بك (" + clientIP + ")"
	}
	flashError := r.URL.Query().Get("error")

	_ = bp.renderer.Render(w, r, "portal/index", map[string]interface{}{
		"Title":        "صفحة المشترك - " + data.Subscriber.FullName,
		"Data":         data,
		"Settings":     data.Settings,
		"CurrentUser":  user,
		"ClientIP":     clientIP,
		"FlashSuccess": flashSuccess,
		"FlashError":   flashError,
	})
}

// RequestLoanHandler handles loan request by the subscriber
func (bp *Blueprint) RequestLoanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/portal", http.StatusFound)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/portal/login", http.StatusFound)
		return
	}

	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings != nil && !settings.AllowLoanRequest {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("خدمة طلب السلفة معطلة حالياً من قبل الإدارة"), http.StatusFound)
		return
	}

	username := user.Username

	if user.Role != "admin" {
		sub, _ := bp.db.GetSubscriber(username)
		if sub != nil && sub.ProfileName != "" {
			prof, _ := bp.db.GetProfileByName(sub.ProfileName)
			if prof != nil && !prof.PortalEnabled {
				http.Redirect(w, r, "/portal?error="+url.QueryEscape("صفحة المشترك غير متاحة لهذه الباقة"), http.StatusFound)
				return
			}
		}
	}

	_, err := bp.db.GrantSubscriberLoan(username, "self-service")
	if err != nil {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/portal?success="+url.QueryEscape("تم تفعيل السلفة وفك الحظر وعودة الإنترنت بنجاح!"), http.StatusFound)
}

// DisconnectHandler disconnects active session to force reconnection
func (bp *Blueprint) DisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/portal", http.StatusFound)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/portal/login", http.StatusFound)
		return
	}

	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings != nil && !settings.AllowSessionDisconnect {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("إعادة تشغيل الاتصال غير مسموح بها"), http.StatusFound)
		return
	}

	_ = bp.db.DisconnectSubscriberSession(user.Username)
	http.Redirect(w, r, "/portal?success="+url.QueryEscape("تم إرسال أمر إعادة تشغيل الجلسة بنجاح، جاري إعادة الاتصال..."), http.StatusFound)
}

// ChangePasswordHandler updates subscriber password
func (bp *Blueprint) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/portal", http.StatusFound)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/portal/login", http.StatusFound)
		return
	}

	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings != nil && !settings.AllowPasswordChange {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("تغيير كلمة المرور غير مسموح به من صفحة المشترك"), http.StatusFound)
		return
	}

	oldPass := strings.TrimSpace(r.FormValue("old_password"))
	newPass := strings.TrimSpace(r.FormValue("new_password"))
	confirmPass := strings.TrimSpace(r.FormValue("confirm_password"))

	if newPass == "" || len(newPass) < 4 {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("يجب أن تتكون كلمة المرور الجديدة من 4 خانات على الأقل"), http.StatusFound)
		return
	}

	if newPass != confirmPass {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("كلمة المرور الجديدة وتأكيدها غير متطابقين"), http.StatusFound)
		return
	}

	// Verify current password
	var currentPass string
	_ = bp.db.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", user.Username).Scan(&currentPass)
	if currentPass != oldPass {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("كلمة المرور الحالية غير صحيحة"), http.StatusFound)
		return
	}

	if err := bp.db.UpdateSubscriberPassword(user.Username, newPass); err != nil {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("حدث خطأ أثناء تحديث كلمة المرور: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/portal?success="+url.QueryEscape("تم تغيير كلمة المرور بنجاح!"), http.StatusFound)
}

// RenewFromBalanceHandler handles subscriber self-service renewal using wallet balance
func (bp *Blueprint) RenewFromBalanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/portal", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/portal/login", http.StatusFound)
		return
	}

	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings != nil && !settings.AllowBalanceRenewal {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("التجديد الذاتي من الرصيد غير مفعل حالياً"), http.StatusFound)
		return
	}

	sub, err := bp.db.GetSubscriber(user.Username)
	if err != nil || sub == nil {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("تعذر العثور على بيانات المشترك"), http.StatusFound)
		return
	}

	prof, err := bp.db.GetProfileByName(sub.ProfileName)
	if err != nil || prof == nil {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("تعذر العثور على الباقة المحددة"), http.StatusFound)
		return
	}

	if sub.Balance < prof.Price {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape(fmt.Sprintf("رصيد المحفظة الحالي (%.2f ج.م) غير كافٍ لتجديد الباقة (سعر الباقة: %.2f ج.م)", sub.Balance, prof.Price)), http.StatusFound)
		return
	}

	err = bp.db.RenewSubscriberWithPaymentOption(sub.Username, prof.Name, prof.Price, "balance", "balance", "تجديد ذاتي من رصيد المحفظة عبر بوابة المشترك", user.Username)
	if err != nil {
		http.Redirect(w, r, "/portal?error="+url.QueryEscape("فشل تجديد الاشتراك: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/portal?success="+url.QueryEscape("تم تجديد باقتك بنجاح وخصم قيمة الاشتراك من رصيد محفظتك!"), http.StatusFound)
}

// ExpiredPageViewData holds all rendering context for the 4 expired portals
type ExpiredPageViewData struct {
	Title               string
	PageType            string // "quota", "time", "quota_loan", "time_loan"
	IsQuotaMode         bool
	IsTimeMode          bool
	ShowLoanOption      bool
	ClientIP            string
	Subscriber          *models.Subscriber
	Profile             *models.Profile
	Reseller            *models.Reseller
	Settings            *models.SubscriberPortalSettings
	IsIdentified        bool

	// FreeRADIUS / Database verified flags:
	IsQuotaExpired      bool
	IsTimeExpired       bool
	IsLoanEligible      bool
	IneligibilityReason string

	// Loan Parameters:
	LoanHours           int
	LoanQuotaGB         float64
	LoanDeductionText   string

	// Wallet & Balance:
	CanRenewFromBalance bool
	WalletBalance       float64
	ProfilePrice        float64

	// Usage Statistics:
	UsedQuotaGB         float64
	TotalQuotaGB        float64
	ExpiryDateFormatted string

	SuccessMsg          string
	ErrorMsg            string
	IsPreview           bool
}

// buildExpiredViewData prepares comprehensive context for expired pages with strict IP and quota/time verification
func (bp *Blueprint) buildExpiredViewData(r *http.Request, pageType string, allowLoan bool) *ExpiredPageViewData {
	clientIP := extractClientIP(r)
	settings, _ := bp.db.GetSubscriberPortalSettings()
	if settings == nil {
		settings = &models.SubscriberPortalSettings{
			PortalEnabled: true,
			CompanyName:   "شبكة خدمات الإنترنت",
		}
	}

	data := &ExpiredPageViewData{
		PageType:       pageType,
		IsQuotaMode:    strings.HasPrefix(pageType, "quota"),
		IsTimeMode:     strings.HasPrefix(pageType, "time"),
		ShowLoanOption: allowLoan,
		ClientIP:       clientIP,
		Settings:       settings,
		SuccessMsg:     r.URL.Query().Get("success"),
		ErrorMsg:       r.URL.Query().Get("error"),
	}

	if r.URL.Query().Get("loan_success") == "1" && data.SuccessMsg == "" {
		data.SuccessMsg = "تم منح وتفعيل سلفة الطوارئ بنجاح على حسابك، وإرسال أمر استعادة الاتصال للراوتر عبر خادم الراديوس!"
	}

	// 1. Identify Subscriber: Check admin preview or active IP lookup
	var sub *models.Subscriber

	previewParam := r.URL.Query().Get("preview")
	targetUser := strings.TrimSpace(r.URL.Query().Get("user"))
	currentUser := middleware.GetUser(r)

	// If admin is previewing another user
	if targetUser != "" && (currentUser != nil && (currentUser.Role == "admin" || currentUser.Role == "reseller")) {
		sub, _ = bp.db.GetSubscriber(targetUser)
	} else if currentUser != nil && currentUser.Role == "subscriber" {
		sub, _ = bp.db.GetSubscriber(currentUser.Username)
	} else if clientIP != "" {
		sub, _ = bp.db.FindSubscriberForExpiredPortal(clientIP)
	}

	// Mock preview mode for admin testing
	if previewParam != "" && sub == nil {
		data.IsPreview = true
		sub = &models.Subscriber{
			Username:   "demo_subscriber",
			FullName:   "مشترك تجريبي (معاينة الإدارة)",
			ContractNo: "TEST-9988",
			Status:     "expired",
			Balance:    150.0,
			QuotaBytes: 100 * 1024 * 1024 * 1024, // 100 GB
		}
	}

	if sub != nil {
		data.Subscriber = sub
		data.IsIdentified = true
		data.WalletBalance = sub.Balance

		// Fetch Profile
		var prof *models.Profile
		if sub.ProfileName != "" {
			prof, _ = bp.db.GetProfileByName(sub.ProfileName)
		}
		if prof == nil {
			prof = &models.Profile{
				Name:              sub.ProfileName,
				DisplayName:       sub.ProfileName,
				LoanEnabled:       true,
				LoanHours:         48,
				LoanQuotaBytes:    5 * 1024 * 1024 * 1024,
				LoanDeductionType: "full",
				Price:             200.0,
			}
		}
		data.Profile = prof
		data.ProfilePrice = prof.Price

		// Fetch Reseller
		if sub.ResellerID > 0 {
			reseller, _ := bp.db.GetReseller(sub.ResellerID)
			data.Reseller = reseller
		}
		if data.Reseller == nil {
			data.Reseller = &models.Reseller{
				Name:  settings.CompanyName,
				Phone: settings.CompanyPhone,
			}
		}

		// Calculate Usage and Quotas
		totalQuotaBytes := sub.QuotaBytes
		if totalQuotaBytes <= 0 && prof.QuotaBytes > 0 {
			totalQuotaBytes = prof.QuotaBytes
		}
		data.TotalQuotaGB = float64(totalQuotaBytes) / (1024 * 1024 * 1024)

		cycleStart := sub.CreatedAt
		var lastRenewed sql.NullTime
		_ = bp.db.QueryRow("SELECT last_renewed_at FROM subscribers WHERE username = ?", sub.Username).Scan(&lastRenewed)
		if lastRenewed.Valid {
			cycleStart = lastRenewed.Time
		}

		var usedBytes int64
		_ = bp.db.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)
			FROM radacct WHERE username = ? AND acctstarttime >= ?`, sub.Username, cycleStart).Scan(&usedBytes)
		data.UsedQuotaGB = float64(usedBytes) / (1024 * 1024 * 1024)

		now := time.Now()
		data.IsQuotaExpired = (totalQuotaBytes > 0 && usedBytes >= totalQuotaBytes)
		data.IsTimeExpired = (sub.ExpirationDate.Valid && now.After(sub.ExpirationDate.Time))

		if sub.ExpirationDate.Valid {
			data.ExpiryDateFormatted = sub.ExpirationDate.Time.Format("2006-01-02 15:04")
		} else {
			data.ExpiryDateFormatted = "منتهي الصلاحية"
		}

		// Check wallet balance renewal
		if sub.Balance >= prof.Price && prof.Price > 0 {
			data.CanRenewFromBalance = true
		}

		// Loan details
		loanHours := prof.LoanHours
		if loanHours <= 0 {
			loanHours = 48
		}
		data.LoanHours = loanHours
		data.LoanQuotaGB = float64(prof.LoanQuotaBytes) / (1024 * 1024 * 1024)
		if data.LoanQuotaGB <= 0 {
			data.LoanQuotaGB = 5.0 // default 5 GB
		}
		data.LoanDeductionText = "سداد كلي كامل عند التجديد"
		if prof.LoanDeductionType == "partial" {
			data.LoanDeductionText = "سداد جزئي حسب الاستهلاك الفعلي"
		}

		// Strict RADIUS & DB Verification: Is Loan Eligible?
		if allowLoan {
			if previewParam == "eligible" {
				data.IsLoanEligible = true
			} else if previewParam == "ineligible" {
				data.IsLoanEligible = false
				data.IneligibilityReason = "تم استخدام سلفة الطوارئ مسبقاً لهذا الشهر وما زالت نشطة على حسابك."
			} else {
				// Real strict verification
				if sub.LoanActive {
					data.IsLoanEligible = false
					data.IneligibilityReason = "لقد قمت بتفعيل سلفة الطوارئ مسبقاً لهذا الشهر وما زالت نشطة على حسابك."
				} else if !prof.LoanEnabled {
					data.IsLoanEligible = false
					data.IneligibilityReason = fmt.Sprintf("عفواً، نظام السلفة غير مفعل على باقة اشتراكك الحالية (%s).", prof.DisplayName)
				} else if pageType == "quota_loan" && !data.IsQuotaExpired {
					data.IsLoanEligible = false
					data.IneligibilityReason = "لم تنفد الكوتة الشهرية لحسابك بالكامل بعد، ولا يمكن طلب سلفة طوارئ قبل نفاد الكوتة."
				} else if pageType == "time_loan" && !data.IsTimeExpired {
					data.IsLoanEligible = false
					data.IneligibilityReason = "فترة صلاحية اشتراكك لم تنتهِ بعد، ولا يمكن تفعيل سلفة فترة السماح قبل حلول موعد الانتهاء."
				} else {
					data.IsLoanEligible = true
				}
			}
		}
	} else {
		// Not identified by IP
		if allowLoan {
			data.IsLoanEligible = false
			data.IneligibilityReason = "تعذر التحقق من رقم الآيبي الخاص بجهازك تلقائياً. يرجى تسجيل الدخول إلى بوابة المشترك للتأكد من حسابك وأهليتك للسلفة."
		}
		data.Reseller = &models.Reseller{
			Name:  settings.CompanyName,
			Phone: settings.CompanyPhone,
		}
	}

	return data
}

// ExpiredQuotaHandler renders the Quota Expired page (Notice only, without loan)
func (bp *Blueprint) ExpiredQuotaHandler(w http.ResponseWriter, r *http.Request) {
	data := bp.buildExpiredViewData(r, "quota", false)
	_ = bp.renderer.Render(w, r, "portal/expired_quota", data)
}

// ExpiredTimeHandler renders the Time Expired page (Notice only, without loan)
func (bp *Blueprint) ExpiredTimeHandler(w http.ResponseWriter, r *http.Request) {
	data := bp.buildExpiredViewData(r, "time", false)
	_ = bp.renderer.Render(w, r, "portal/expired_time", data)
}

// ExpiredQuotaLoanHandler renders the Quota Expired page with smart loan button strictly verified by IP
func (bp *Blueprint) ExpiredQuotaLoanHandler(w http.ResponseWriter, r *http.Request) {
	data := bp.buildExpiredViewData(r, "quota_loan", true)
	_ = bp.renderer.Render(w, r, "portal/expired_quota_loan", data)
}

// ExpiredTimeLoanHandler renders the Time Expired page with smart loan button strictly verified by IP
func (bp *Blueprint) ExpiredTimeLoanHandler(w http.ResponseWriter, r *http.Request) {
	data := bp.buildExpiredViewData(r, "time_loan", true)
	_ = bp.renderer.Render(w, r, "portal/expired_time_loan", data)
}

// ApplyExpiredLoanHandler handles smart loan activation from the expired portals
func (bp *Blueprint) ApplyExpiredLoanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/portal", http.StatusSeeOther)
		return
	}

	clientIP := extractClientIP(r)
	loanType := r.FormValue("type") // "quota" or "time"

	redirectPath := "/expired/quota-loan"
	if loanType == "time" {
		redirectPath = "/expired/time-loan"
	}

	// 1. Identify Subscriber strictly by IP (or authenticated subscriber session)
	var sub *models.Subscriber
	var err error

	user := middleware.GetUser(r)
	if user != nil && user.Role == "subscriber" {
		sub, err = bp.db.GetSubscriber(user.Username)
	} else {
		sub, err = bp.db.FindSubscriberForExpiredPortal(clientIP)
	}

	if err != nil || sub == nil {
		http.Redirect(w, r, redirectPath+"?error="+url.QueryEscape("تعذر التحقق من حسابك عبر عنوان الآيبي الخاص بك ("+clientIP+"). يرجى تسجيل الدخول أو التواصل مع الموزع."), http.StatusFound)
		return
	}

	// 2. Strict FreeRADIUS & DB Eligibility Verification
	prof, _ := bp.db.GetProfileByName(sub.ProfileName)
	if prof == nil || !prof.LoanEnabled {
		http.Redirect(w, r, redirectPath+"?error="+url.QueryEscape("نظام السلفة غير مفعل على باقة اشتراكك الحالية"), http.StatusFound)
		return
	}

	if sub.LoanActive {
		http.Redirect(w, r, redirectPath+"?error="+url.QueryEscape("لديك سلفة نشطة بالفعل لهذا الشهر ولا يمكن طلب سلفة أخرى قبل التجديد"), http.StatusFound)
		return
	}

	// 3. Grant Loan (updates DB, FreeRADIUS radcheck, radreply, adds billing transaction, triggers WhatsApp & CoA)
	_, err = bp.db.GrantSubscriberLoan(sub.Username, "self-service-portal")
	if err != nil {
		http.Redirect(w, r, redirectPath+"?error="+url.QueryEscape("تعذر تفعيل السلفة: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectPath+"?loan_success=1", http.StatusFound)
}
