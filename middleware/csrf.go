package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/crypto"
)

const (
	CSRFCookieName           = "radius_csrf"
	CSRFFormField            = "csrf_token"
	CSRFHeader               = "X-CSRF-Token"
	csrfContextKey stringKey = "csrf_token"
)

type stringKey string

// CSRFMiddleware enforces Cross-Site Request Forgery protection on state-changing requests
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Retrieve or generate CSRF token
		cookieToken := ""
		if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
			cookieToken = c.Value
		}

		if cookieToken == "" || len(cookieToken) < 32 {
			cookieToken = crypto.GenerateRandomSecret(16)
			isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
			http.SetCookie(w, &http.Cookie{
				Name:     CSRFCookieName,
				Value:    cookieToken,
				Path:     "/",
				HttpOnly: false, // Must be readable by client JS for AJAX X-CSRF-Token header
				Secure:   isSecure,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   86400,
			})
		}

		ctx := context.WithValue(r.Context(), csrfContextKey, cookieToken)
		r = r.WithContext(ctx)

		// 2. Safe methods do not modify state
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Method == "TRACE" {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Exemption for machine-to-machine internal API calls using Authorization header
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}

		// Exemption for SSTP hook-up if coming from localhost
		if r.URL.Path == "/sstp/routes/hook-up" {
			remote := r.RemoteAddr
			if strings.HasPrefix(remote, "127.0.0.1") || strings.HasPrefix(remote, "[::1]") || strings.HasPrefix(remote, "localhost") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 4. Origin & Referer Verification
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err == nil {
				if !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "Cross-Origin request blocked (Origin mismatch)", http.StatusForbidden)
					return
				}
			}
		} else if referer := r.Header.Get("Referer"); referer != "" {
			if u, err := url.Parse(referer); err == nil {
				if !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "Cross-Origin request blocked (Referer mismatch)", http.StatusForbidden)
					return
				}
			}
		}

		// 5. CSRF Token Validation
		submittedToken := r.Header.Get(CSRFHeader)
		if submittedToken == "" {
			submittedToken = r.FormValue(CSRFFormField)
		}

		if submittedToken == "" || subtle.ConstantTimeCompare([]byte(submittedToken), []byte(cookieToken)) != 1 {
			http.Error(w, "طلب غير مصرح به: رمز التحقق من الأمان مفقود أو غير صالح (CSRF Token Mismatch)", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GetCSRFToken retrieves the CSRF token from request context or cookie
func GetCSRFToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tok, ok := r.Context().Value(csrfContextKey).(string); ok && tok != "" {
		return tok
	}
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}
