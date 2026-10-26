package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeadersMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "https://example.com/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected nosniff header")
	}
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("expected SAMEORIGIN header")
	}
	if !strings.Contains(resp.Header.Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("expected HSTS header when HTTPS enabled")
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Errorf("expected CSP header")
	}
}

func TestCSRFProtection(t *testing.T) {
	testSecret := "0123456789abcdef0123456789abcdef"

	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	protectedHandler := CSRFMiddleware(innerHandler)

	// 1. GET request should succeed and return CSRF cookie
	getReq := httptest.NewRequest("GET", "http://example.com/form", nil)
	getRec := httptest.NewRecorder()
	protectedHandler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", getRec.Code)
	}

	cookie := getRec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookie {
		if c.Name == CSRFCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("expected CSRF cookie set on GET")
	}

	// 2. POST without CSRF token should be rejected (403)
	postReqBad := httptest.NewRequest("POST", "http://example.com/action", strings.NewReader("name=test"))
	postReqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReqBad.AddCookie(csrfCookie)
	postRecBad := httptest.NewRecorder()
	protectedHandler.ServeHTTP(postRecBad, postReqBad)

	if postRecBad.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF token should be 403, got %d", postRecBad.Code)
	}

	// 3. POST with mismatched Origin header should be rejected (403)
	postReqEvilOrigin := httptest.NewRequest("POST", "http://example.com/action", strings.NewReader("name=test&csrf_token="+csrfCookie.Value))
	postReqEvilOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReqEvilOrigin.Header.Set("Origin", "http://evil.com")
	postReqEvilOrigin.AddCookie(csrfCookie)
	postRecEvil := httptest.NewRecorder()
	protectedHandler.ServeHTTP(postRecEvil, postReqEvilOrigin)

	if postRecEvil.Code != http.StatusForbidden {
		t.Fatalf("POST with evil Origin should be 403, got %d", postRecEvil.Code)
	}

	// 4. POST with matching token in form should succeed (200)
	form := url.Values{}
	form.Set("name", "test")
	form.Set("csrf_token", csrfCookie.Value)
	postReqGood := httptest.NewRequest("POST", "http://example.com/action", strings.NewReader(form.Encode()))
	postReqGood.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReqGood.Header.Set("Origin", "http://example.com")
	postReqGood.AddCookie(csrfCookie)
	postRecGood := httptest.NewRecorder()
	protectedHandler.ServeHTTP(postRecGood, postReqGood)

	if postRecGood.Code != http.StatusOK {
		t.Fatalf("valid POST should succeed with 200, got %d", postRecGood.Code)
	}

	// 5. POST with matching token in X-CSRF-Token header should succeed (200)
	postReqHeader := httptest.NewRequest("POST", "http://example.com/action", strings.NewReader(`{"name":"test"}`))
	postReqHeader.Header.Set("Content-Type", "application/json")
	postReqHeader.Header.Set("X-CSRF-Token", csrfCookie.Value)
	postReqHeader.Header.Set("Origin", "http://example.com")
	postReqHeader.AddCookie(csrfCookie)
	postRecHeader := httptest.NewRecorder()
	protectedHandler.ServeHTTP(postRecHeader, postReqHeader)

	if postRecHeader.Code != http.StatusOK {
		t.Fatalf("valid POST with X-CSRF-Token header should succeed with 200, got %d", postRecHeader.Code)
	}

	_ = testSecret
}

func TestSessionRevocation(t *testing.T) {
	secret := "test-secret-key-1234567890abcdef"
	user := SessionUser{
		ID:       1,
		Username: "admin_test",
		Role:     "admin",
	}

	token := GenerateToken(user, secret)
	parsedUser, err := ParseToken(token, secret)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if parsedUser.Username != user.Username {
		t.Fatalf("expected username %s, got %s", user.Username, parsedUser.Username)
	}

	// Revoke sessions for user
	time.Sleep(10 * time.Millisecond)
	RevokeUserSessions("admin_test")

	// Attempting to parse the old token must now fail with revocation error
	_, err = ParseToken(token, secret)
	if err == nil {
		t.Fatalf("expected error parsing revoked session token, but succeeded")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revoked error message, got: %v", err)
	}

	// Generating a fresh token AFTER revocation must succeed
	time.Sleep(10 * time.Millisecond)
	freshToken := GenerateToken(user, secret)
	freshUser, err := ParseToken(freshToken, secret)
	if err != nil {
		t.Fatalf("fresh token after revocation should be valid, got: %v", err)
	}
	if freshUser.Username != user.Username {
		t.Fatalf("expected %s, got %s", user.Username, freshUser.Username)
	}
}

func TestRBACMiddlewares(t *testing.T) {
	adminOnlyHandler := RequireAdmin(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("admin-success"))
	})

	// 1. Unauthenticated request to admin endpoint redirects to login
	reqUnauth := httptest.NewRequest("GET", "/admin-route", nil)
	recUnauth := httptest.NewRecorder()
	adminOnlyHandler(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusFound || !strings.Contains(recUnauth.Header().Get("Location"), "/auth/login") {
		t.Fatalf("unauthenticated user should be redirected to login, got code %d", recUnauth.Code)
	}

	// 2. Reseller accessing admin endpoint gets 403 Forbidden
	secret := "my-secret-key-32bytes-for-tests!"
	resellerToken := GenerateToken(SessionUser{ID: 2, Username: "reseller1", Role: "reseller"}, secret)
	authMiddleware := AuthMiddleware(secret)

	recReseller := httptest.NewRecorder()
	reqReseller := httptest.NewRequest("GET", "/admin-route", nil)
	reqReseller.AddCookie(&http.Cookie{Name: CookieName, Value: resellerToken})

	authMiddleware(adminOnlyHandler).ServeHTTP(recReseller, reqReseller)
	if recReseller.Code != http.StatusForbidden {
		t.Fatalf("reseller accessing admin route must be 403 Forbidden, got %d", recReseller.Code)
	}

	// 3. Subscriber accessing admin endpoint gets 403 Forbidden
	subscriberToken := GenerateToken(SessionUser{ID: 3, Username: "sub1", Role: "subscriber"}, secret)
	recSub := httptest.NewRecorder()
	reqSub := httptest.NewRequest("GET", "/admin-route", nil)
	reqSub.AddCookie(&http.Cookie{Name: CookieName, Value: subscriberToken})

	authMiddleware(adminOnlyHandler).ServeHTTP(recSub, reqSub)
	if recSub.Code != http.StatusForbidden {
		t.Fatalf("subscriber accessing admin route must be 403 Forbidden, got %d", recSub.Code)
	}

	// 4. Admin accessing admin route succeeds with 200 OK
	adminToken := GenerateToken(SessionUser{ID: 1, Username: "admin", Role: "admin"}, secret)
	recAdmin := httptest.NewRecorder()
	reqAdmin := httptest.NewRequest("GET", "/admin-route", nil)
	reqAdmin.AddCookie(&http.Cookie{Name: CookieName, Value: adminToken})

	authMiddleware(adminOnlyHandler).ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusOK {
		t.Fatalf("admin accessing admin route must be 200 OK, got %d", recAdmin.Code)
	}
}
