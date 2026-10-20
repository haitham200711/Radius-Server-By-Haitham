package settings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	serverIP := r.URL.Query().Get("server_ip")
	if serverIP == "" {
		serverIP = "192.168.2.90"
	}
	secret := r.URL.Query().Get("secret")
	if secret == "" {
		secret = "radius123"
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

	_ = bp.renderer.Render(w, "settings/index", map[string]interface{}{
		"Title":          "إعدادات النظام وتغيير كلمة المرور",
		"ActiveMenu":     "settings",
		"CurrentUser":    middleware.GetUser(r),
		"ServerIP":       serverIP,
		"Secret":         secret,
		"MikrotikScript": mikrotikScript,
		"FlashStatus":    status,
		"FlashMsg":       msg,
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
		if r.Header.Get("X-Requested-With") == "XMLHttpRequest" || strings.Contains(r.Header.Get("Accept"), "application/json") {
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

		respond("success", "تم تغيير كلمة المرور بنجاح!", http.StatusOK)
		return
	}

	respond("error", "صلاحية الحساب غير مصرح لها بتغيير كلمة المرور هنا", http.StatusBadRequest)
}
