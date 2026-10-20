package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/q-controller/qcontroller/src/pkg/auth/identity"
)

// Requests that reach it without an identity in the context get 401,
// unless the path is exempt.
func requireIdentity(exemptPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if identity.FromContext(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}
			for _, p := range exemptPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
		})
	}
}

type Gate struct {
	auth   *Authenticator
	exempt []string
}

func NewGate(a *Authenticator, exemptPrefixes ...string) *Gate {
	return &Gate{
		auth:   a,
		exempt: append(publicPaths(), exemptPrefixes...),
	}
}

func (g *Gate) Protect(next http.Handler) http.Handler {
	h := requireCSRFHeader(next)
	h = requireIdentity(g.exempt...)(h)
	h = g.browser(h)
	return g.bearer(h)
}

// Bearer turns an Authorization: Bearer JWT into an identity in the request
// context. Requests without the header pass through untouched. A header that
// fails verification against every issuer is a hard 401.
func (g *Gate) bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}

		token := strings.TrimPrefix(h, "Bearer ")
		var lastErr error
		for _, v := range g.auth.verifiers {
			id, _, err := v.identify(r.Context(), token)
			if err != nil {
				lastErr = err
				continue
			}
			next.ServeHTTP(w, r.WithContext(identity.WithContext(r.Context(), id)))
			return
		}
		slog.WarnContext(r.Context(), "invalid bearer token", "error", lastErr)
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
	})
}

// browser turns a valid session cookie into an identity in the request
// context and re-issues the cookie with a fresh TTL (sliding session). If the
// ID token inside has expired and a refresh token is available it is refreshed
// against the IdP first; when that fails the cookie is cleared and the request
// continues without an identity, so requireIdentity rejects it. Requests
// without a cookie, or with one that doesn't verify, pass through untouched.
func (g *Gate) browser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Already authenticated
		if identity.FromContext(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}

		var s session
		if rd, err := g.auth.session.Reader(r); err != nil || json.NewDecoder(rd).Decode(&s) != nil || s.Identity == nil {
			next.ServeHTTP(w, r)
			return
		}

		p, ok := g.auth.providers[s.Provider]
		if ok && s.RefreshToken != "" && !s.TokenExpiry.IsZero() && time.Now().After(s.TokenExpiry) {
			refreshed, rerr := p.renew(r.Context(), s.RefreshToken)
			if rerr != nil {
				slog.WarnContext(r.Context(), "session refresh failed", "provider", s.Provider, "error", rerr)
				http.SetCookie(w, g.auth.session.Clear())
				next.ServeHTTP(w, r)
				return
			}
			s = refreshed
		}

		_ = writeJSONCookie(g.auth.session.Writer(w), s)
		next.ServeHTTP(w, r.WithContext(identity.WithContext(r.Context(), s.Identity)))
	})
}

// strictCSP is applied to every response. 'unsafe-inline'
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
func SecurityHeaders(externalURL string) func(http.Handler) http.Handler {
	u, err := url.Parse(externalURL)
	hsts := err == nil && u.Scheme == "https"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			h.Set("Content-Security-Policy", strictCSP)
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
func requireCSRFHeader(next http.Handler) http.Handler {
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
