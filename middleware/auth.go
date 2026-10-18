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
	"time"
)

type contextKey string

const (
	UserContextKey contextKey = "user"
	CookieName                = "radius_session"
)

type SessionUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"` // "admin", "staff", "reseller"
}

// GenerateToken creates signed session string: ID:Username:Role:Timestamp:Signature
func GenerateToken(u SessionUser, secret string) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	payload := fmt.Sprintf("%d:%s:%s:%s", u.ID, u.Username, u.Role, ts)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s:%s", payload, sig)
}

// ParseToken verifies and extracts SessionUser
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

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}

	return &SessionUser{
		ID:       id,
		Username: parts[1],
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
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetUser retrieves SessionUser from context
func GetUser(r *http.Request) *SessionUser {
	if val := r.Context().Value(UserContextKey); val != nil {
		if u, ok := val.(*SessionUser); ok {
			return u
		}
	}
	return nil
}

// RequireAuth redirects to login if unauthenticated
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}

// RequireAdmin ensures role is "admin"
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := GetUser(r)
		if u == nil || u.Role != "admin" {
			http.Error(w, "Access Denied: Admins Only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
