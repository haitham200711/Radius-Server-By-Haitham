package auth

import (
	"net/http"
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

func (bp *Blueprint) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if middleware.GetUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	if r.Method == http.MethodGet {
		_ = bp.renderer.Render(w, r, "auth/login", map[string]interface{}{
			"Title": "تسجيل الدخول",
		})
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	// 1. Check Admins table
	var admin models.Admin
	err := bp.db.QueryRow("SELECT id, username, password_hash, role, is_active FROM admins WHERE username = ?", username).Scan(
		&admin.ID, &admin.Username, &admin.PasswordHash, &admin.Role, &admin.IsActive)

	if err == nil && admin.IsActive && admin.CheckPassword(password) {
		token := middleware.GenerateToken(middleware.SessionUser{
			ID:       admin.ID,
			Username: admin.Username,
			Role:     admin.Role,
		}, bp.secret)

		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Expires:  time.Now().Add(7 * 24 * time.Hour),
		})

		next := r.URL.Query().Get("next")
		if next == "" {
			next = "/dashboard"
		}
		http.Redirect(w, r, next, http.StatusFound)
		return
	}

	// 2. Check Resellers table
	var reseller models.Reseller
	err = bp.db.QueryRow("SELECT id, username, password_hash, is_active FROM resellers WHERE username = ?", username).Scan(
		&reseller.ID, &reseller.Username, &reseller.PasswordHash, &reseller.IsActive)

	if err == nil && reseller.IsActive && reseller.CheckPassword(password) {
		token := middleware.GenerateToken(middleware.SessionUser{
			ID:       reseller.ID,
			Username: reseller.Username,
			Role:     "reseller",
		}, bp.secret)

		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Expires:  time.Now().Add(7 * 24 * time.Hour),
		})

		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	// 3. Check Subscribers table / FreeRADIUS radcheck
	var subPass string
	err = bp.db.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", username).Scan(&subPass)
	if err == nil && subPass == password {
		portalSettings, _ := bp.db.GetSubscriberPortalSettings()
		if portalSettings == nil || portalSettings.PortalEnabled {
			token := middleware.GenerateToken(middleware.SessionUser{
				ID:       0,
				Username: username,
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
			return
		}
	}

	// Invalid credentials
	_ = bp.renderer.Render(w, r, "auth/login", map[string]interface{}{
		"Title": "تسجيل الدخول",
		"Error": "اسم المستخدم أو كلمة المرور غير صحيحة",
	})
}

func (bp *Blueprint) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}
