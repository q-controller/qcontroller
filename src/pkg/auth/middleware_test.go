package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	v1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	"github.com/q-controller/qcontroller/src/pkg/auth/identity"
	"github.com/q-controller/qcontroller/src/pkg/auth/signed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const testIssuerURL = "https://idp.test"

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func signJWT(t *testing.T, key *rsa.PrivateKey, subject string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"iss": testIssuerURL,
		"aud": "qcontroller",
		"sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newTestAuth(t *testing.T) (*Authenticator, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}
	reg := &Authenticator{
		verifiers: []*verifier{{
			name: "test",
			oidc: oidc.NewVerifier(testIssuerURL, keySet, &oidc.Config{ClientID: "qcontroller"}),
		}},
		providers: map[string]*provider{},
		session:   testSessionCookie(testSecret),
		state:     signed.Cookie{Name: stateCookieName, Path: callbackPath, TTL: defaultLoginTTL, Key: testSecret},
		returnTo:  "/ui/",
	}
	return reg, key
}

// withTokenEndpoint registers a provider for issuer "test" whose token endpoint
// is served by handler, so login/callback and refresh calls can be driven from a test.
func withTokenEndpoint(t *testing.T, reg *Authenticator, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	reg.providers["test"] = &provider{verifier: reg.verifiers[0], oauth2: oauth2.Config{
		ClientID:     "qcontroller",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams},
	}}
}

func testSessionCookie(secret []byte) signed.Cookie {
	return signed.Cookie{Name: sessionCookieName, Path: "/", TTL: defaultSessionTTL, Key: secret}
}

func signedSessionCookie(t *testing.T, secret []byte, s session) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	w := testSessionCookie(secret).Writer(rec)
	require.NoError(t, json.NewEncoder(w).Encode(s))
	require.NoError(t, w.Close())
	return rec.Result().Cookies()[0]
}

func readSession(t *testing.T, secret []byte, c *http.Cookie) session {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	rd, err := testSessionCookie(secret).Reader(req)
	require.NoError(t, err)
	var s session
	require.NoError(t, json.NewDecoder(rd).Decode(&s))
	return s
}

func capture(captured **v1.Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = identity.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// ---- Auth chain ----

func TestBearerMiddleware_ValidTokenStampsIdentity(t *testing.T) {
	reg, key := newTestAuth(t)

	var got *v1.Identity
	handler := NewGate(reg).bearer(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.Header.Set("Authorization", "Bearer "+signJWT(t, key, "bob"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "bob", got.Subject)
	assert.Equal(t, "test", got.IssuedBy)
}

func TestBearerMiddleware_InvalidTokenReturns401(t *testing.T) {
	reg, _ := newTestAuth(t)
	_, untrusted := newTestAuth(t)

	var got *v1.Identity
	handler := NewGate(reg).bearer(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.Header.Set("Authorization", "Bearer "+signJWT(t, untrusted, "mallory"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthenticated")
	assert.Nil(t, got, "inner handler must not run after an invalid bearer token")
}

func TestBrowserMiddleware_ValidCookieStampsIdentity(t *testing.T) {
	reg, _ := newTestAuth(t)

	var got *v1.Identity
	handler := NewGate(reg).browser(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{Identity: &v1.Identity{Subject: "alice"}}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Subject)
}

func TestBrowserMiddleware_BadCookiePassesThroughWithoutIdentity(t *testing.T) {
	reg, _ := newTestAuth(t)

	var got *v1.Identity
	handler := NewGate(reg).browser(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.AddCookie(signedSessionCookie(t, []byte("wrong-secret-wrong-secret-wrong!"), session{Identity: &v1.Identity{Subject: "mallory"}}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, got)
}

func TestBrowserMiddleware_ExpiredIDTokenRefreshFailsDropsSession(t *testing.T) {
	reg, _ := newTestAuth(t)
	withTokenEndpoint(t, reg, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	})

	var got *v1.Identity
	handler := NewGate(reg).browser(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{
		Identity:     &v1.Identity{Subject: "alice"},
		RefreshToken: "rt",
		Provider:     "test",
		TokenExpiry:  time.Now().Add(-time.Minute),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, got, "stale identity must not reach the handler after a failed refresh")
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, reg.session.Name, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Less(t, cookies[0].MaxAge, 0, "session cookie must be cleared")
}

func TestBrowserMiddleware_ExpiredIDTokenRefreshSucceeds(t *testing.T) {
	reg, key := newTestAuth(t)
	newIDToken := signJWT(t, key, "alice")
	withTokenEndpoint(t, reg, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "rt-old", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "at",
			"token_type":    "bearer",
			"id_token":      newIDToken,
			"refresh_token": "rt-new",
		})
	})

	var got *v1.Identity
	handler := NewGate(reg).browser(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{
		Identity:     &v1.Identity{Subject: "alice", Name: "stale"},
		RefreshToken: "rt-old",
		Provider:     "test",
		TokenExpiry:  time.Now().Add(-time.Minute),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Subject)
	assert.Equal(t, "test", got.IssuedBy)
	assert.Empty(t, got.Name, "identity must come from the refreshed token, not the stale cookie")

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	sc := readSession(t, testSecret, cookies[0])
	assert.Equal(t, newIDToken, sc.IDToken)
	assert.Equal(t, "rt-new", sc.RefreshToken)
	assert.True(t, sc.TokenExpiry.After(time.Now()), "refreshed session must record the new token expiry")
}

func TestBrowserMiddleware_ExpiredIDTokenWithoutRefreshTokenKeepsSession(t *testing.T) {
	reg, _ := newTestAuth(t)
	withTokenEndpoint(t, reg, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("token endpoint must not be called without a refresh token")
	})

	var got *v1.Identity
	handler := NewGate(reg).browser(capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{
		Identity:    &v1.Identity{Subject: "alice"},
		Provider:    "test",
		TokenExpiry: time.Now().Add(-time.Minute),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Subject)
}

func TestLogin_UnknownProviderReturns400(t *testing.T) {
	reg, _ := newTestAuth(t)
	mux := http.NewServeMux()
	RegisterLoginFlow(mux, reg)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, loginPath+"?provider=unknown", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLoginAndCallback_FullFlowMintsSessionAndClearsState(t *testing.T) {
	reg, key := newTestAuth(t)
	idToken := signJWT(t, key, "alice")
	withTokenEndpoint(t, reg, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		assert.Equal(t, "code-123", r.Form.Get("code"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "at",
			"token_type":    "bearer",
			"id_token":      idToken,
			"refresh_token": "rt-fresh",
		})
	})

	mux := http.NewServeMux()
	RegisterLoginFlow(mux, reg)

	// Step 1: GET /v1/auth/login?provider=test
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, loginPath+"?provider=test", nil))
	require.Equal(t, http.StatusFound, loginRec.Code)

	loc, err := url.Parse(loginRec.Header().Get("Location"))
	require.NoError(t, err)
	stateParam := loc.Query().Get("state")
	require.NotEmpty(t, stateParam)

	loginCookies := loginRec.Result().Cookies()
	require.Len(t, loginCookies, 1)
	assert.Equal(t, stateCookieName, loginCookies[0].Name)
	assert.Equal(t, callbackPath, loginCookies[0].Path)

	// Step 2: GET /v1/auth/callback?code=code-123&state=<stateParam>
	cbReq := httptest.NewRequest(http.MethodGet, callbackPath+"?code=code-123&state="+url.QueryEscape(stateParam), nil)
	cbReq.AddCookie(loginCookies[0])
	cbRec := httptest.NewRecorder()
	mux.ServeHTTP(cbRec, cbReq)

	require.Equal(t, http.StatusFound, cbRec.Code)
	assert.Equal(t, "/ui/", cbRec.Header().Get("Location"))

	var sessCookie, clearedState *http.Cookie
	for _, c := range cbRec.Result().Cookies() {
		switch c.Name {
		case sessionCookieName:
			sessCookie = c
		case stateCookieName:
			clearedState = c
		}
	}
	require.NotNil(t, sessCookie, "callback must set session cookie")
	require.NotNil(t, clearedState, "callback must clear state cookie")
	assert.Less(t, clearedState.MaxAge, 0)

	s := readSession(t, testSecret, sessCookie)
	require.NotNil(t, s.Identity)
	assert.Equal(t, "alice", s.Identity.Subject)
	assert.Equal(t, "test", s.Identity.IssuedBy)
	assert.Equal(t, idToken, s.IDToken)
	assert.Equal(t, "rt-fresh", s.RefreshToken)
	assert.Equal(t, "test", s.Provider)
	assert.True(t, s.TokenExpiry.After(time.Now()))
}

func TestCallback_StateMismatchReturns400(t *testing.T) {
	reg, _ := newTestAuth(t)
	withTokenEndpoint(t, reg, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("token endpoint must not be called when state mismatches")
	})

	mux := http.NewServeMux()
	RegisterLoginFlow(mux, reg)

	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, loginPath+"?provider=test", nil))
	require.Equal(t, http.StatusFound, loginRec.Code)

	cbReq := httptest.NewRequest(http.MethodGet, callbackPath+"?code=code-123&state=wrong-state", nil)
	cbReq.AddCookie(loginRec.Result().Cookies()[0])
	cbRec := httptest.NewRecorder()
	mux.ServeHTTP(cbRec, cbReq)

	assert.Equal(t, http.StatusBadRequest, cbRec.Code)
	assert.Contains(t, cbRec.Body.String(), "state mismatch")
}

func TestLogout_ClearsCookieWithSingleSetCookieHeader(t *testing.T) {
	reg, _ := newTestAuth(t)
	reg.returnTo = "https://qcontroller.example.com/ui/"
	reg.providers["test"] = &provider{
		verifier:           reg.verifiers[0],
		oauth2:             oauth2.Config{ClientID: "qcontroller"},
		endSessionEndpoint: "https://idp.test/logout",
	}

	mux := http.NewServeMux()
	RegisterLoginFlow(mux, reg)
	handler := NewGate(reg).browser(mux)

	r := httptest.NewRequest(http.MethodPost, logoutPath, nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{
		Identity: &v1.Identity{Subject: "alice"},
		IDToken:  "id-tok-123",
		Provider: "test",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "must send only one Set-Cookie header clearing the session")
	assert.Equal(t, sessionCookieName, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Less(t, cookies[0].MaxAge, 0)

	var body struct {
		LogoutURL string `json:"logoutUrl"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "https://idp.test/logout?client_id=qcontroller&id_token_hint=id-tok-123&post_logout_redirect_uri=https%3A%2F%2Fqcontroller.example.com%2Fui%2F", body.LogoutURL)
}

func TestRequireIdentity_NoIdentityReturns401(t *testing.T) {
	handler := requireIdentity()(capture(new(*v1.Identity)))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireIdentity_ExemptPathPassesThroughWithoutIdentity(t *testing.T) {
	var got *v1.Identity
	handler := requireIdentity(append(publicPaths(), "/ui/")...)(capture(&got))

	for _, path := range []string{"/v1/auth/login", "/v1/auth/login?provider=x", "/v1/auth/config", "/v1/auth/logout", "/ui/index.html"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "path %s", path)
	}
	assert.Nil(t, got, "exempt path must not stamp Identity")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "/v1/auth/me must not be exempt")
}

func TestPublicPaths_MatchesProtoAnnotations(t *testing.T) {
	rpcHTTPPath := func(method protoreflect.Name) string {
		m := v1.File_services_auth_v1_service_proto.Services().ByName("AuthService").Methods().ByName(method)
		require.NotNil(t, m)
		rule, ok := proto.GetExtension(m.Options(), annotations.E_Http).(*annotations.HttpRule)
		require.True(t, ok)
		switch p := rule.GetPattern().(type) {
		case *annotations.HttpRule_Get:
			return p.Get
		case *annotations.HttpRule_Post:
			return p.Post
		default:
			t.Fatalf("unexpected http pattern for %s: %T", method, p)
			return ""
		}
	}

	public := publicPaths()
	assert.ElementsMatch(t, []string{
		loginPath,
		callbackPath,
		logoutPath,
		rpcHTTPPath("GetConfig"),
	}, public)
	assert.NotContains(t, public, rpcHTTPPath("GetMe"))

	reg, _ := newTestAuth(t)
	mux := http.NewServeMux()
	RegisterLoginFlow(mux, reg)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, loginPath},
		{http.MethodGet, callbackPath},
		{http.MethodPost, logoutPath},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
		assert.Equal(t, tc.method+" "+tc.path, pattern, "constant %s must match generated OpenAPI route", tc.path)
	}
}

// ---- requireCSRFHeader ----

func TestRequireCSRFHeader_AllowsSafeMethods(t *testing.T) {
	called := false
	handler := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		called = false
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(m, "/v1/x", nil))
		assert.Equal(t, http.StatusOK, rec.Code, "method %s", m)
		assert.True(t, called, "method %s should pass through", m)
	}
}

func TestRequireCSRFHeader_RejectsMutatingWithoutHeader(t *testing.T) {
	handler := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(m, "/v1/x", nil))
		assert.Equal(t, http.StatusForbidden, rec.Code, "method %s", m)
	}
}

func TestRequireCSRFHeader_AllowsWithHeader(t *testing.T) {
	handler := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/v1/x", nil)
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireCSRFHeader_BypassesForBearerAuth(t *testing.T) {
	handler := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/v1/x", nil)
	r.Header.Set("Authorization", "Bearer eyJ.fake.jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code, "bearer auth must skip CSRF")
}

// ---- SecurityHeaders ----

func TestSecurityHeaders_AlwaysSet(t *testing.T) {
	handler := SecurityHeaders("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"))
	csp := rec.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
}

func TestSecurityHeaders_HSTSOnlyForHTTPS(t *testing.T) {
	cases := []struct {
		external string
		wantHSTS bool
	}{
		{"", false},
		{"http://localhost:8080", false},
		{"https://qcontroller.example.com", true},
	}
	for _, c := range cases {
		handler := SecurityHeaders(c.external)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		got := rec.Header().Get("Strict-Transport-Security")
		if c.wantHSTS {
			assert.NotEmpty(t, got, "external=%q should set HSTS", c.external)
		} else {
			assert.Empty(t, got, "external=%q should not set HSTS", c.external)
		}
	}
}

// ---- Composed pipeline (smoke: SecurityHeaders -> Bearer -> Browser -> RequireIdentity -> CSRF -> handler) ----

func composedChain(reg *Authenticator, final http.Handler) http.Handler {
	return SecurityHeaders("")(NewGate(reg).Protect(final))
}

func TestComposedPipeline_GETPasses(t *testing.T) {
	reg, _ := newTestAuth(t)

	var got *v1.Identity
	chain := composedChain(reg, capture(&got))

	r := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{Identity: &v1.Identity{Subject: "alice", IssuedBy: "test"}}))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Subject)
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.True(t, strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'"))
}

func TestComposedPipeline_POSTWithoutCSRFHeaderRejected(t *testing.T) {
	reg, _ := newTestAuth(t)
	chain := composedChain(reg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler must not run")
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/v1/whatever", nil)
	r.AddCookie(signedSessionCookie(t, testSecret, session{Identity: &v1.Identity{Subject: "alice"}}))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
