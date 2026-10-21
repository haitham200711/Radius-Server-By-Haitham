package portal

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
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

	if r.Method == http.MethodGet {
		errorMsg := r.URL.Query().Get("error")
		_ = bp.renderer.Render(w, "portal/login", map[string]interface{}{
			"Title":    "تسجيل دخول المشترك",
			"Settings": settings,
			"Error":    errorMsg,
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
	http.Redirect(w, r, "/portal/login", http.StatusFound)
}

// IndexHandler renders the full self-care portal
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
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
	flashError := r.URL.Query().Get("error")

	_ = bp.renderer.Render(w, "portal/index", map[string]interface{}{
		"Title":        "صفحة المشترك - " + data.Subscriber.FullName,
		"Data":         data,
		"Settings":     data.Settings,
		"CurrentUser":  user,
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
