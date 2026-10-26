package middleware

import (
	"net/http"
)

// SecurityHeadersMiddleware injects defense-in-depth HTTP security headers
func SecurityHeadersMiddleware(isHTTPS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prevent MIME-sniffing
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// Prevent clickjacking
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")

			// XSS Protection for legacy browsers
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Strict Referrer Policy
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// Disable sensitive browser permissions by default
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

			// Modern Content Security Policy
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; font-src 'self' https://cdn.jsdelivr.net data:; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'self';")

			// HTTP Strict Transport Security (HSTS) when TLS is enabled
			if isHTTPS || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}
