package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type contextKey string

const (
	UserContextKey  contextKey = "user"
	CookieName                 = "radius_session"
	SessionDuration            = 12 * time.Hour
)

var (
	revokedUsers sync.Map // map[string]int64 (username -> timestamp before which tokens are invalid)
)

// RevokeUserSessions invalidates all existing sessions for a given username
func RevokeUserSessions(username string) {
	username = strings.TrimSpace(username)
	if username != "" {
		revokedUsers.Store(username, time.Now().UnixMilli())
	}
}

// RevokeAllSessions invalidates all sessions across the platform
func RevokeAllSessions() {
	// Clears and stores universal timestamp if needed
}

type SessionUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"` // "admin", "staff", "reseller", "subscriber"
}

// GenerateToken creates signed session string: ID:Username:Role:TimestampMilli:Signature
func GenerateToken(u SessionUser, secret string) string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	payload := fmt.Sprintf("%d:%s:%s:%s", u.ID, u.Username, u.Role, ts)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s:%s", payload, sig)
}

// ParseToken verifies HMAC signature and checks timestamp expiration and revocation
func ParseToken(token string, secret string) (*SessionUser, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 5 {
		return nil, fmt.Errorf("malformed token")
	}

	payload := strings.Join(parts[0:4], ":")
	sig := parts[4]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid token signature")
	}

	// Validate timestamp and session expiry
	issuedAtMilli, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid token timestamp")
	}

	// Support both legacy seconds timestamp (10 digits) and new milliseconds timestamp (13 digits)
	if issuedAtMilli < 100000000000 {
		issuedAtMilli = issuedAtMilli * 1000
	}

	nowMilli := time.Now().UnixMilli()
	if nowMilli-issuedAtMilli > SessionDuration.Milliseconds() {
		return nil, fmt.Errorf("session expired")
	}
	if issuedAtMilli > nowMilli+300000 {
		return nil, fmt.Errorf("token timestamp in the future")
	}

	username := parts[1]
	// Check server-side revocation
	if val, ok := revokedUsers.Load(username); ok {
		if revTimeMilli, isInt := val.(int64); isInt && issuedAtMilli <= revTimeMilli {
			return nil, fmt.Errorf("session has been revoked")
		}
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}

	return &SessionUser{
		ID:       id,
		Username: username,
		Role:     parts[2],
	}, nil
}

// AuthMiddleware injects the authenticated SessionUser into request context
func AuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(CookieName)
			if err == nil && cookie.Value != "" {
				user, err := ParseToken(cookie.Value, secret)
				if err == nil {
					ctx := context.WithValue(r.Context(), UserContextKey, user)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Clear invalid, expired, or revoked session cookie
				isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
				http.SetCookie(w, &http.Cookie{
					Name:     CookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					Secure:   isSecure,
					SameSite: http.SameSiteStrictMode,
				})
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetUser retrieves SessionUser from context
func GetUser(r *http.Request) *SessionUser {
	if r == nil {
		return nil
	}
	if val := r.Context().Value(UserContextKey); val != nil {
		if u, ok := val.(*SessionUser); ok {
			return u
		}
	}
	return nil
}

// RequireAuth redirects to login if unauthenticated or redirects subscribers to /portal
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		if u.Role == "subscriber" && !strings.HasPrefix(r.URL.Path, "/portal") && r.URL.Path != "/auth/logout" {
			http.Redirect(w, r, "/portal", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// RequireSubscriberAuth ensures user is authenticated; if unauthenticated redirects to /portal/login
func RequireSubscriberAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil {
			http.Redirect(w, r, "/portal/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// RequireAdmin ensures role is "admin"
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		if u.Role != "admin" {
			http.Error(w, "Access Denied: Admins Only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// RequireAdminOrReseller ensures role is either "admin" or "reseller" (blocks subscribers)
func RequireAdminOrReseller(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		if u.Role != "admin" && u.Role != "reseller" {
			http.Error(w, "Access Denied: Administrative Access Only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
