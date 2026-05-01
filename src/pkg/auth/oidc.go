package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"golang.org/x/oauth2"
)

const (
	sessionCookieName = "qcontroller_session"
	stateCookieName   = "qcontroller_oauth_state"
	sessionTTL        = 8 * time.Hour
	stateTTL          = 10 * time.Minute
	loginPath         = "/auth/login"
	callbackPath      = "/auth/callback"
	logoutPath        = "/auth/logout"
	configPath        = "/auth/config"
)

type configuredIssuer struct {
	name               string
	clientID           string
	verifier           *oidc.IDTokenVerifier
	oauth2             *oauth2.Config // nil for bearer-only issuers
	endSessionEndpoint string         // OIDC RP-initiated logout, empty if not advertised
}

// sessionPayload is what we pack into the session cookie. Identity is the
// only thing exposed to handlers via Verify; IDToken/Provider are kept for
// federated logout (passing id_token_hint to the IdP); RefreshToken is used
// by the Renewal middleware to refresh the ID token when it expires.
type sessionPayload struct {
	Identity     *Identity `json:"id"`
	IDToken      string    `json:"idt,omitempty"`
	RefreshToken string    `json:"rt,omitempty"`
	Provider     string    `json:"p,omitempty"`
}

// OIDCVerifier validates incoming requests via either a session cookie set
// after a successful login flow, or a bearer JWT issued by any configured
// issuer. It also serves /auth/login and /auth/callback.
type OIDCVerifier struct {
	issuers     map[string]*configuredIssuer
	secret      []byte
	externalURL string
}

// cookieSecure reports whether session/state cookies should carry the Secure
// attribute. Derived from external_url so it works correctly behind a
// TLS-terminating reverse proxy where r.TLS would be nil.
func (v *OIDCVerifier) cookieSecure() bool {
	return strings.HasPrefix(v.externalURL, "https://")
}

// jwtExp parses the `exp` claim out of a JWT without verifying signature. The
// signature was already verified at issue time; we just need the expiry to
// decide when to refresh. Returns zero time if the token is malformed.
func jwtExp(jwt string) time.Time {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return time.Time{}
	}
	if c.Exp <= 0 {
		// Missing/zero exp would otherwise resolve to the Unix epoch and
		// make Renewal refresh on every request.
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

func NewOIDCVerifier(ctx context.Context, cfg *settingsv1.AuthConfig, secret []byte) (*OIDCVerifier, error) {
	if cfg == nil {
		return nil, errors.New("nil auth config")
	}
	if len(cfg.Issuers) == 0 {
		return nil, errors.New("auth.issuers must not be empty")
	}
	external := strings.TrimRight(cfg.ExternalUrl, "/")

	issuers := make(map[string]*configuredIssuer, len(cfg.Issuers))
	for _, i := range cfg.Issuers {
		if i.Name == "" {
			return nil, errors.New("issuer.name must be set")
		}
		if _, exists := issuers[i.Name]; exists {
			return nil, fmt.Errorf("duplicate issuer name %q", i.Name)
		}
		provider, err := oidc.NewProvider(ctx, i.IssuerUrl)
		if err != nil {
			return nil, fmt.Errorf("oidc discovery for %s: %w", i.Name, err)
		}

		audience := ""
		if len(i.Audiences) > 0 {
			audience = i.Audiences[0]
		} else if i.ClientId != "" {
			audience = i.ClientId
		}

		oidcCfg := &oidc.Config{ClientID: audience}
		if audience == "" {
			oidcCfg.SkipClientIDCheck = true
		}
		verifier := provider.Verifier(oidcCfg)

		var oauth2Cfg *oauth2.Config
		if i.ClientId != "" {
			if external == "" {
				return nil, fmt.Errorf("issuer %s has client_id but auth.external_url is empty", i.Name)
			}
			oauth2Cfg = &oauth2.Config{
				ClientID:     i.ClientId,
				ClientSecret: i.ClientSecret,
				Endpoint:     provider.Endpoint(),
				RedirectURL:  external + callbackPath,
				Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
			}
		}

		var disc struct {
			EndSessionEndpoint string `json:"end_session_endpoint"`
		}
		_ = provider.Claims(&disc) // best-effort; not all IdPs advertise it

		issuers[i.Name] = &configuredIssuer{
			name:               i.Name,
			clientID:           i.ClientId,
			verifier:           verifier,
			oauth2:             oauth2Cfg,
			endSessionEndpoint: disc.EndSessionEndpoint,
		}
	}

	return &OIDCVerifier{
		issuers:     issuers,
		secret:      secret,
		externalURL: external,
	}, nil
}

// LoginProviders returns the names of issuers that participate in the
// browser login flow. Used by /auth/config.
func (v *OIDCVerifier) LoginProviders() []string {
	out := make([]string, 0, len(v.issuers))
	for name, iss := range v.issuers {
		if iss.oauth2 != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (v *OIDCVerifier) Verify(r *http.Request) (*Identity, error) {
	// Bearer token wins over cookie if both present (CI clients).
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimPrefix(h, "Bearer ")
		var lastErr error
		for _, iss := range v.issuers {
			id, err := v.verifyJWT(r.Context(), iss, token)
			if err == nil {
				return id, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("invalid bearer token: %w", lastErr)
	}

	if c, err := r.Cookie(sessionCookieName); err == nil {
		var sp sessionPayload
		if err := decodeSigned(v.secret, c.Value, &sp); err == nil && sp.Identity != nil {
			return sp.Identity, nil
		}
	}

	return nil, ErrUnauthenticated
}

func (v *OIDCVerifier) verifyJWT(ctx context.Context, iss *configuredIssuer, raw string) (*Identity, error) {
	tok, err := iss.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	var claims struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	_ = tok.Claims(&claims)
	return &Identity{
		Subject:  tok.Subject,
		Email:    claims.Email,
		Name:     claims.Name,
		Groups:   claims.Groups,
		IssuedBy: "oidc:" + iss.name,
	}, nil
}

// Renewal is middleware that re-issues the session cookie with a fresh TTL on
// every cookie-authenticated request (sliding session). If the ID token has
// expired and a refresh token is available, it calls the IdP to obtain a new
// one. On refresh failure, the cookie is cleared and the next request requires
// re-login. Revocation latency is bounded by the ID token TTL.
func (v *OIDCVerifier) Renewal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		var sp sessionPayload
		if err := decodeSigned(v.secret, c.Value, &sp); err != nil || sp.Identity == nil {
			next.ServeHTTP(w, r)
			return
		}

		iss, ok := v.issuers[sp.Provider]
		if ok && iss.oauth2 != nil && sp.RefreshToken != "" {
			if exp := jwtExp(sp.IDToken); !exp.IsZero() && time.Now().After(exp) {
				ctx := r.Context()
				ts := iss.oauth2.TokenSource(ctx, &oauth2.Token{RefreshToken: sp.RefreshToken})
				refreshed := false
				if newTok, terr := ts.Token(); terr == nil {
					if newID, ok := newTok.Extra("id_token").(string); ok {
						if newIdentity, verr := v.verifyJWT(ctx, iss, newID); verr == nil {
							sp.Identity = newIdentity
							sp.IDToken = newID
							if newTok.RefreshToken != "" {
								sp.RefreshToken = newTok.RefreshToken
							}
							refreshed = true
						}
					}
				}
				if !refreshed {
					http.SetCookie(w, &http.Cookie{
						Name:     sessionCookieName,
						Path:     "/",
						HttpOnly: true,
						SameSite: http.SameSiteLaxMode,
						Secure:   v.cookieSecure(),
						MaxAge:   -1,
					})
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		if encoded, encErr := encodeSigned(v.secret, sp, sessionTTL); encErr == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    encoded,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   v.cookieSecure(),
				MaxAge:   int(sessionTTL.Seconds()),
			})
		}

		next.ServeHTTP(w, r)
	})
}

// RegisterRoutes wires the HTTP-redirect endpoints (/auth/login,
// /auth/callback) onto mux. /auth/me, /auth/config, /auth/logout are JSON
// RPCs served by AuthServer via the gRPC-gateway runtime.
func (v *OIDCVerifier) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(loginPath, v.handleLogin)
	mux.HandleFunc(callbackPath, v.handleCallback)
}

func (v *OIDCVerifier) handleLogin(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("provider")
	iss, ok := v.issuers[name]
	if !ok || iss.oauth2 == nil {
		http.Error(w, "unknown or non-login provider", http.StatusBadRequest)
		return
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "rand error", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	encoded, err := encodeSigned(v.secret, struct {
		State    string `json:"s"`
		Provider string `json:"p"`
	}{state, name}, stateTTL)
	if err != nil {
		http.Error(w, "encode state cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    encoded,
		Path:     "/auth/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   v.cookieSecure(),
		MaxAge:   int(stateTTL.Seconds()),
	})

	http.Redirect(w, r, iss.oauth2.AuthCodeURL(state), http.StatusFound)
}

func (v *OIDCVerifier) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}
	c, err := r.Cookie(stateCookieName)
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	var sp struct {
		State    string `json:"s"`
		Provider string `json:"p"`
	}
	if err := decodeSigned(v.secret, c.Value, &sp); err != nil {
		http.Error(w, "invalid state cookie", http.StatusBadRequest)
		return
	}
	if sp.State != state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	iss, ok := v.issuers[sp.Provider]
	if !ok || iss.oauth2 == nil {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	tok, err := iss.oauth2.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusBadRequest)
		return
	}
	id, err := v.verifyJWT(r.Context(), iss, rawID)
	if err != nil {
		http.Error(w, "id_token verify failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	sessionEncoded, err := encodeSigned(v.secret, sessionPayload{
		Identity:     id,
		IDToken:      rawID,
		RefreshToken: tok.RefreshToken,
		Provider:     iss.name,
	}, sessionTTL)
	if err != nil {
		http.Error(w, "encode session cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionEncoded,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   v.cookieSecure(),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Path:     "/auth/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/ui/", http.StatusFound)
}
