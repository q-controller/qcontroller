package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Middleware tries each verifier in order and attaches the first identity
// that matches to the request context. If no verifier has an opinion and
// auth is disabled (no verifiers configured), requests pass as anonymous.
// Otherwise unauthenticated requests get 401.
//
// Paths in exemptPrefixes skip verification entirely — used for /auth/*
// (login flow can't require auth) and /ui/* (static assets, the SPA itself
// gates access via /auth/me).
func Middleware(verifiers []Verifier, exemptPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(verifiers) == 0 {
				next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), Anonymous())))
				return
			}

			for _, p := range exemptPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			for _, v := range verifiers {
				id, err := v.Verify(r)
				if err == nil {
					next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
					return
				}
				if !errors.Is(err, ErrUnauthenticated) {
					slog.WarnContext(r.Context(), "auth verifier error", "error", err)
					http.Error(w, "unauthenticated", http.StatusUnauthorized)
					return
				}
			}

			http.Error(w, "unauthenticated", http.StatusUnauthorized)
		})
	}
}

// strictCSP is applied to every response except Swagger UI. 'unsafe-inline'
// for styles is required by Mantine's runtime CSS-in-JS; scripts stay strict.
// WebSocket falls under connect-src 'self' (browsers treat same-origin ws:/wss:
// as 'self').
const strictCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"connect-src 'self'; " +
	"font-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// SecurityHeaders sets a strict CSP and other XSS/clickjacking-related
// response headers on every request. HSTS is only sent when externalURL is
// HTTPS so HTTP dev setups don't get locked into HTTPS by their own browser.
// /v1/swagger/* is exempt from CSP because Swagger UI inlines its init script.
func SecurityHeaders(externalURL string) func(http.Handler) http.Handler {
	hsts := strings.HasPrefix(externalURL, "https://")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			if !strings.HasPrefix(r.URL.Path, "/v1/swagger/") {
				h.Set("Content-Security-Policy", strictCSP)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCSRFHeader rejects mutating requests that don't carry an
// X-Requested-With header, unless authenticated via Authorization: Bearer
// (CI/programmatic clients can't be CSRF'd). The header is unforgeable from
// cross-origin contexts: HTML forms can't set custom headers, and cross-origin
// fetch() with custom headers triggers a CORS preflight that the orchestrator
// doesn't grant. Layered defense on top of SameSite=Lax cookies.
func RequireCSRFHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-Requested-With") == "" {
				http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
