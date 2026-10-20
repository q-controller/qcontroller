package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"

	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
)

const (
	loginPath    = "/v1/auth/login"
	callbackPath = "/v1/auth/callback"
	logoutPath   = "/v1/auth/logout"
	configPath   = "/v1/auth/config"
)

// publicPaths returns the auth paths that must remain reachable without
// authentication: the login/callback/logout cookie handlers plus the config
// RPC. /v1/auth/me is intentionally NOT here.
func publicPaths() []string {
	return []string{loginPath, callbackPath, logoutPath, configPath}
}

type loginState struct {
	State    string `json:"s"`
	Provider string `json:"p"`
}

func RegisterLoginFlow(mux *http.ServeMux, a *Authenticator) {
	_ = authv1.HandlerFromMux(&loginFlow{auth: a}, mux)
}

type loginFlow struct {
	auth *Authenticator
}

var _ authv1.ServerInterface = (*loginFlow)(nil)

func (f *loginFlow) AuthServiceLogin(w http.ResponseWriter, r *http.Request, params authv1.AuthServiceLoginParams) {
	p, ok := f.auth.providers[params.Provider]
	if !ok {
		http.Error(w, "unknown or non-login provider", http.StatusBadRequest)
		return
	}

	var stateBytes [16]byte
	if _, err := rand.Read(stateBytes[:]); err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes[:])

	if err := writeJSONCookie(f.auth.state.Writer(w), loginState{
		State:    state,
		Provider: params.Provider,
	}); err != nil {
		http.Error(w, "encode state cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, p.start(state), http.StatusFound)
}

func (f *loginFlow) AuthServiceCallback(w http.ResponseWriter, r *http.Request, params authv1.AuthServiceCallbackParams) {
	var sc loginState
	if rd, err := f.auth.state.Reader(r); err != nil || json.NewDecoder(rd).Decode(&sc) != nil {
		http.Error(w, "missing or invalid state cookie", http.StatusBadRequest)
		return
	}
	if sc.State != params.State {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	p, ok := f.auth.providers[sc.Provider]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	s, err := p.finish(r.Context(), params.Code)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Del("Set-Cookie")
	if err := writeJSONCookie(f.auth.session.Writer(w), s); err != nil {
		http.Error(w, "encode session cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, f.auth.state.Clear())

	http.Redirect(w, r, f.auth.returnTo, http.StatusFound)
}

func (f *loginFlow) AuthServiceLogout(w http.ResponseWriter, r *http.Request) {
	var sess session
	if rd, err := f.auth.session.Reader(r); err == nil {
		_ = json.NewDecoder(rd).Decode(&sess)
	}

	w.Header().Del("Set-Cookie")
	http.SetCookie(w, f.auth.session.Clear())

	var resp authv1.ServicesAuthV1LogoutResponse
	if p, ok := f.auth.providers[sess.Provider]; ok {
		if u := p.logoutURL(sess.IDToken, f.auth.returnTo); u != "" {
			resp.LogoutUrl = &u
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
