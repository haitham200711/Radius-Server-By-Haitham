package settings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/adv"
	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/crypto"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/sysinfo"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
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
	serverIP := strings.Split(r.Host, ":")[0]
	if serverIP == "" || serverIP == "localhost" || serverIP == "127.0.0.1" {
		serverIP = "192.168.2.90"
	}
	var secret string
	_ = bp.db.QueryRow("SELECT secret FROM nas ORDER BY id ASC LIMIT 1").Scan(&secret)
	if secret == "" {
		secret = crypto.GenerateRandomSecret(16)
	}

	status := r.URL.Query().Get("status")
	msg := r.URL.Query().Get("msg")

	mikrotikScript := fmt.Sprintf(`# ========================================================
# MikroTik RouterOS RADIUS Setup Script
# Server: %s | Secret: %s
# ========================================================

# 1. Enable RADIUS for PPP (PPPoE / PPTP / L2TP / SSTP / OVPN)
/ppp aaa set use-radius=yes interim-update=2m accounting=yes

# 2. Enable RADIUS for Hotspot
/ip hotspot profile set [find default=yes] use-radius=yes radius-interim-update=2m

# 3. Enable CoA (Incoming Disconnect & Rate-Limit modification)
/radius incoming set accept=yes port=3799

# 4. Remove previous Radius configuration if exists
/radius remove [find address="%s"]

# 5. Add RADIUS Server
/radius add address=%s secret="%s" service=ppp,hotspot,login authentication-port=1812 accounting-port=1813 timeout=3s comment="Radius-Server-By-Haitham"

:put ">>> RADIUS Configuration Applied Successfully! <<<"
`, serverIP, secret, serverIP, serverIP, secret)

	portalSettings, _ := bp.db.GetSubscriberPortalSettings()

	// System resources for maintenance overview
	systemRes := sysinfo.GetResources()

	// Check FreeRADIUS service status
	freeRadiusStatus := "unknown"
	if out, err := exec.Command("systemctl", "is-active", "freeradius").Output(); err == nil {
		freeRadiusStatus = strings.TrimSpace(string(out))
	}

	// Check radius-server platform service status
	platformStatus := "active"
	if out, err := exec.Command("systemctl", "is-active", "radius-server").Output(); err == nil {
		platformStatus = strings.TrimSpace(string(out))
	}

	// Active online sessions count
	activeSessionsCount := 0
	_ = bp.db.QueryRow("SELECT COUNT(*) FROM radacct WHERE acctstoptime IS NULL").Scan(&activeSessionsCount)

	// Routers / NAS count
	nasCount := 0
	_ = bp.db.QueryRow("SELECT COUNT(*) FROM nas").Scan(&nasCount)

	// Backups data
	backupSettings, err := bp.db.GetBackupSettings()
	if err != nil {
		backupSettings = &models.BackupSettings{
			Enabled:        true,
			IntervalHours:  6,
			RetentionCount: 10,
			SendWhatsApp:   false,
		}
	}

	backupsList, err := bp.db.ListBackups()
	if err != nil {
		backupsList = []models.BackupRecord{}
	}

	var totalBackupBytes int64
	for _, b := range backupsList {
		totalBackupBytes += b.SizeBytes
	}
	waStatus := whatsapp.GetWhatsAppStatus()

	// ADV & SAS data
	advAnalysis := adv.GetCurrentAnalysis()
	advSuccessMsg := r.URL.Query().Get("adv_success")
	if advSuccessMsg == "" {
		advSuccessMsg = r.URL.Query().Get("success")
	}
	advErrorMsg := r.URL.Query().Get("adv_error")
	if advErrorMsg == "" {
		advErrorMsg = r.URL.Query().Get("error")
	}

	_ = bp.renderer.Render(w, r, "settings/index", map[string]interface{}{
		"Title":               "إعدادات النظام والخدمات",
		"ActiveMenu":          "settings",
		"CurrentUser":         middleware.GetUser(r),
		"ServerIP":            serverIP,
		"Secret":              secret,
		"MikrotikScript":      mikrotikScript,
		"PortalSettings":      portalSettings,
		"System":              systemRes,
		"FreeRadiusStatus":    freeRadiusStatus,
		"PlatformStatus":      platformStatus,
		"ActiveSessionsCount": activeSessionsCount,
		"NasCount":            nasCount,
		"FlashStatus":         status,
		"FlashMsg":            msg,
		"BackupSettings":      backupSettings,
		"Backups":             backupsList,
		"TotalBackupCount":    len(backupsList),
		"TotalBackupStorage":  database.FormatBytesHuman(totalBackupBytes),
		"WhatsAppStatus":      waStatus,
		"AdvAnalysis":         advAnalysis,
		"AdvSuccessMsg":       advSuccessMsg,
		"AdvErrorMsg":         advErrorMsg,
		"ImportedSubs":        r.URL.Query().Get("subs"),
		"ImportedProfs":       r.URL.Query().Get("profs"),
		"ImportedResellers":   r.URL.Query().Get("resellers"),
		"ImportedSessions":    r.URL.Query().Get("sessions"),
	})
}

func (bp *Blueprint) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oldPass := strings.TrimSpace(r.FormValue("old_password"))
	newPass := strings.TrimSpace(r.FormValue("new_password"))
	confirmPass := strings.TrimSpace(r.FormValue("confirm_password"))

	respond := func(status, msg string, httpCode int) {
		if isAjax(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(httpCode)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":  status,
				"message": msg,
			})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/settings?status=%s&msg=%s#change-password", status, url.QueryEscape(msg)), http.StatusSeeOther)
	}

	if oldPass == "" || newPass == "" || confirmPass == "" {
		respond("error", "يرجى ملء جميع الحقول المطلوبة (كلمة المرور الحالية، الجديدة، وتأكيدها)", http.StatusBadRequest)
		return
	}

	if len(newPass) < 6 {
		respond("error", "يجب أن تتكون كلمة المرور الجديدة من 6 أحرف أو أرقام على الأقل", http.StatusBadRequest)
		return
	}

	if newPass != confirmPass {
		respond("error", "كلمة المرور الجديدة وتأكيدها غير متطابقين", http.StatusBadRequest)
		return
	}

	if user.Role == "admin" || user.Role == "staff" {
		var admin models.Admin
		err := bp.db.QueryRow("SELECT id, username, password_hash, role, is_active FROM admins WHERE id = ?", user.ID).Scan(
			&admin.ID, &admin.Username, &admin.PasswordHash, &admin.Role, &admin.IsActive)
		if err != nil {
			respond("error", "لم يتم العثور على حساب المشرف في النظام", http.StatusNotFound)
			return
		}

		if !admin.CheckPassword(oldPass) {
			respond("error", "كلمة المرور الحالية غير صحيحة، يرجى المحاولة مرة أخرى", http.StatusBadRequest)
			return
		}

		if err := admin.SetPassword(newPass); err != nil {
			respond("error", "حدث خطأ أثناء تشفير كلمة المرور الجديدة", http.StatusInternalServerError)
			return
		}

		_, err = bp.db.Exec("UPDATE admins SET password_hash = ? WHERE id = ?", admin.PasswordHash, admin.ID)
		if err != nil {
			respond("error", "فشل حفظ كلمة المرور الجديدة في قاعدة البيانات", http.StatusInternalServerError)
			return
		}

		middleware.RevokeUserSessions(admin.Username)

		respond("success", "تم تغيير كلمة المرور بنجاح! يمكنك الآن استخدام كلمة المرور الجديدة لتسجيل الدخول.", http.StatusOK)
		return
	} else if user.Role == "reseller" {
		var reseller models.Reseller
		err := bp.db.QueryRow("SELECT id, username, password_hash, is_active FROM resellers WHERE id = ?", user.ID).Scan(
			&reseller.ID, &reseller.Username, &reseller.PasswordHash, &reseller.IsActive)
		if err != nil {
			respond("error", "لم يتم العثور على حساب الموزع في النظام", http.StatusNotFound)
			return
		}

		if !reseller.CheckPassword(oldPass) {
			respond("error", "كلمة المرور الحالية غير صحيحة، يرجى المحاولة مرة أخرى", http.StatusBadRequest)
			return
		}

		if err := reseller.SetPassword(newPass); err != nil {
			respond("error", "حدث خطأ أثناء تشفير كلمة المرور الجديدة", http.StatusInternalServerError)
			return
		}

		_, err = bp.db.Exec("UPDATE resellers SET password_hash = ? WHERE id = ?", reseller.PasswordHash, reseller.ID)
		if err != nil {
			respond("error", "فشل حفظ كلمة المرور الجديدة في قاعدة البيانات", http.StatusInternalServerError)
			return
		}

		middleware.RevokeUserSessions(reseller.Username)

		respond("success", "تم تغيير كلمة المرور بنجاح!", http.StatusOK)
		return
	}

	respond("error", "صلاحية الحساب غير مصرح لها بتغيير كلمة المرور هنا", http.StatusBadRequest)
}

func (bp *Blueprint) SavePortalSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "Unauthorized: Admins Only", http.StatusForbidden)
		return
	}

	portalEnabled := r.FormValue("portal_enabled") == "1" || r.FormValue("portal_enabled") == "on" || r.FormValue("portal_enabled") == "true"
	allowLoan := r.FormValue("allow_loan_request") == "1" || r.FormValue("allow_loan_request") == "on" || r.FormValue("allow_loan_request") == "true"
	showQuota := r.FormValue("show_quota_usage") == "1" || r.FormValue("show_quota_usage") == "on" || r.FormValue("show_quota_usage") == "true"
	showDebt := r.FormValue("show_debt") == "1" || r.FormValue("show_debt") == "on" || r.FormValue("show_debt") == "true"
	showBalance := r.FormValue("show_balance") == "1" || r.FormValue("show_balance") == "on" || r.FormValue("show_balance") == "true"
	allowBalanceRenewal := r.FormValue("allow_balance_renewal") == "1" || r.FormValue("allow_balance_renewal") == "on" || r.FormValue("allow_balance_renewal") == "true"
	showProfile := r.FormValue("show_profile_details") == "1" || r.FormValue("show_profile_details") == "on" || r.FormValue("show_profile_details") == "true"
	showOnline := r.FormValue("show_online_status") == "1" || r.FormValue("show_online_status") == "on" || r.FormValue("show_online_status") == "true"
	allowPass := r.FormValue("allow_password_change") == "1" || r.FormValue("allow_password_change") == "on" || r.FormValue("allow_password_change") == "true"
	allowPhoneLogin := r.FormValue("allow_phone_login") == "1" || r.FormValue("allow_phone_login") == "on" || r.FormValue("allow_phone_login") == "true"
	allowIPAutoLogin := r.FormValue("allow_ip_auto_login") == "1" || r.FormValue("allow_ip_auto_login") == "on" || r.FormValue("allow_ip_auto_login") == "true"
	allowDisconnect := r.FormValue("allow_session_disconnect") == "1" || r.FormValue("allow_session_disconnect") == "on" || r.FormValue("allow_session_disconnect") == "true"

	companyName := strings.TrimSpace(r.FormValue("company_name"))
	if companyName == "" {
		companyName = "شبكتنا لخدمات الإنترنت"
	}
	companyPhone := strings.TrimSpace(r.FormValue("company_phone"))
	announcement := strings.TrimSpace(r.FormValue("portal_announcement"))

	settings := &models.SubscriberPortalSettings{
		PortalEnabled:          portalEnabled,
		AllowLoanRequest:       allowLoan,
		ShowQuotaUsage:         showQuota,
		ShowDebt:               showDebt,
		ShowBalance:            showBalance,
		AllowBalanceRenewal:    allowBalanceRenewal,
		ShowProfileDetails:     showProfile,
		ShowOnlineStatus:       showOnline,
		AllowPasswordChange:    allowPass,
		AllowPhoneLogin:        allowPhoneLogin,
		AllowIPAutoLogin:       allowIPAutoLogin,
		AllowSessionDisconnect: allowDisconnect,
		CompanyName:            companyName,
		CompanyPhone:           companyPhone,
		PortalAnnouncement:     announcement,
	}

	if err := bp.db.SaveSubscriberPortalSettings(settings); err != nil {
		http.Redirect(w, r, "/settings?status=danger&msg="+url.QueryEscape("حدث خطأ أثناء حفظ إعدادات صفحة المشترك: "+err.Error())+"#portal-settings", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?status=success&msg="+url.QueryEscape("تم حفظ إعدادات وصلاحيات صفحة المشترك بنجاح!")+"#portal-settings", http.StatusSeeOther)
}

// RebootOSHandler executes a full reboot of the host Linux operating system
func (bp *Blueprint) RebootOSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#tab-maintenance", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "Unauthorized: Admins Only", http.StatusForbidden)
		return
	}

	// Schedule reboot shortly to allow HTTP response to flush cleanly to browser
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cmd := exec.Command("systemctl", "reboot")
		if err := cmd.Run(); err != nil {
			_ = exec.Command("shutdown", "-r", "now").Run()
		}
	}()

	msg := "تم إرسال أمر إعادة تشغيل السيرفر ولينكس بالكامل بنجاح! جاري إعادة إقلاع النظام، يرجى الانتظار دقيقة حتى يكتمل الإقلاع..."
	if isAjax(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "warning",
			"message": msg,
		})
		return
	}

	http.Redirect(w, r, "/settings?status=warning&msg="+url.QueryEscape(msg)+"#tab-maintenance", http.StatusSeeOther)
}

// RestartRadiusHandler restarts FreeRADIUS service
func (bp *Blueprint) RestartRadiusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#tab-maintenance", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "Unauthorized: Admins Only", http.StatusForbidden)
		return
	}

	// Restart FreeRADIUS service
	cmd := exec.Command("systemctl", "restart", "freeradius")
	output, err := cmd.CombinedOutput()
	if err != nil {
		cmdSudo := exec.Command("sudo", "systemctl", "restart", "freeradius")
		output, err = cmdSudo.CombinedOutput()
	}

	status := "success"
	msg := "تمت إعادة تشغيل خدمة الراديوس (FreeRADIUS) بنجاح، وتفريغ الكاش وإعادة تهيئة منافذ المصادقة والمحاسبة!"
	if err != nil {
		status = "danger"
		msg = fmt.Sprintf("فشل إعادة تشغيل خدمة FreeRADIUS: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	if isAjax(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  status,
			"message": msg,
		})
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/settings?status=%s&msg=%s#tab-maintenance", status, url.QueryEscape(msg)), http.StatusSeeOther)
}

// DisconnectAllSessionsHandler disconnects all active sessions on NAS routers and resets radacct
func (bp *Blueprint) DisconnectAllSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#tab-maintenance", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "Unauthorized: Admins Only", http.StatusForbidden)
		return
	}

	succ, fail, err := bp.db.DisconnectAllActiveSessions()
	restartRadius := r.FormValue("restart_radius") == "1" || r.FormValue("restart_radius") == "true"

	status := "success"
	msg := fmt.Sprintf("تم إرسال أوامر فصل الجلسات النشطة (نجح %d، تعذر %d) بنجاح!", succ, fail)

	if restartRadius {
		cmd := exec.Command("systemctl", "restart", "freeradius")
		if errR := cmd.Run(); errR != nil {
			_ = exec.Command("sudo", "systemctl", "restart", "freeradius").Run()
		}
		msg += " وتمت إعادة تشغيل خدمة الراديوس بنظافة تامة."
	}

	if err != nil {
		status = "danger"
		msg = fmt.Sprintf("حدث خطأ أثناء فصل الجلسات: %v", err)
	}

	if isAjax(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  status,
			"message": msg,
			"count":   succ,
			"failed":  fail,
		})
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/settings?status=%s&msg=%s#tab-maintenance", status, url.QueryEscape(msg)), http.StatusSeeOther)
}

// CloseAllDatabaseSessionsHandler marks all active sessions in radacct as closed WITHOUT disconnecting subscribers from NAS routers
func (bp *Blueprint) CloseAllDatabaseSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#tab-maintenance", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "Unauthorized: Admins Only", http.StatusForbidden)
		return
	}

	count, err := bp.db.CloseAllDatabaseSessions()
	status := "success"
	msg := fmt.Sprintf("تم إغلاق وتصفير كافة الجلسات العالقة في قاعدة البيانات بنجاح (%d جلسة) دون إخراج المشتركين، وتم حل مشكلة 'User Already Logged In'!", count)

	if err != nil {
		status = "danger"
		msg = fmt.Sprintf("حدث خطأ أثناء إغلاق الجلسات في قاعدة البيانات: %v", err)
	}

	if isAjax(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  status,
			"message": msg,
			"count":   count,
		})
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/settings?status=%s&msg=%s#tab-maintenance", status, url.QueryEscape(msg)), http.StatusSeeOther)
}

func isAjax(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "XMLHttpRequest" || strings.Contains(r.Header.Get("Accept"), "application/json")
}
