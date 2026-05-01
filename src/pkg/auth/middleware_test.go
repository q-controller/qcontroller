package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubVerifier is a minimal Verifier for testing the chain.
type stubVerifier struct {
	id  *Identity
	err error
}

func (s stubVerifier) Verify(_ *http.Request) (*Identity, error) {
	return s.id, s.err
}

func capture(captured **Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// ---- Middleware (auth chain) ----

func TestMiddleware_NoVerifiers_StampsAnonymous(t *testing.T) {
	var got *Identity
	handler := Middleware(nil)(capture(&got))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/whatever", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "anonymous", got.Subject)
	assert.Equal(t, "anonymous", got.IssuedBy)
}

func TestMiddleware_FirstSuccessfulVerifierWins(t *testing.T) {
	first := stubVerifier{err: ErrUnauthenticated}
	second := stubVerifier{id: &Identity{Subject: "bob", IssuedBy: "oidc:test"}}
	third := stubVerifier{id: &Identity{Subject: "should-not-be-used"}}

	var got *Identity
	handler := Middleware([]Verifier{first, second, third})(capture(&got))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "bob", got.Subject)
}

func TestMiddleware_AllUnauthenticated_Returns401(t *testing.T) {
	v := stubVerifier{err: ErrUnauthenticated}
	handler := Middleware([]Verifier{v, v})(capture(new(*Identity)))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_HardErrorReturns401AndStopsChain(t *testing.T) {
	hardErr := errors.New("token tampered")
	first := stubVerifier{err: hardErr}
	second := stubVerifier{id: &Identity{Subject: "shouldnt-run"}}

	var got *Identity
	handler := Middleware([]Verifier{first, second})(capture(&got))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "token tampered")
	assert.Contains(t, rec.Body.String(), "unauthenticated")
	assert.Nil(t, got, "second verifier must not run after a hard error")
}

func TestMiddleware_ExemptPathPassesThroughWithoutIdentity(t *testing.T) {
	v := stubVerifier{err: ErrUnauthenticated}

	var got *Identity
	handler := Middleware([]Verifier{v}, "/auth/login", "/ui/")(capture(&got))

	for _, path := range []string{"/auth/login", "/auth/login?provider=x", "/ui/index.html"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "path %s", path)
	}
	assert.Nil(t, got, "exempt path must not stamp Identity")
}

// ---- RequireCSRFHeader ----

func TestRequireCSRFHeader_AllowsSafeMethods(t *testing.T) {
	called := false
	handler := RequireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := RequireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(m, "/v1/x", nil))
		assert.Equal(t, http.StatusForbidden, rec.Code, "method %s", m)
	}
}

func TestRequireCSRFHeader_AllowsWithHeader(t *testing.T) {
	handler := RequireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/v1/x", nil)
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireCSRFHeader_BypassesForBearerAuth(t *testing.T) {
	handler := RequireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestSecurityHeaders_CSPExemptForSwagger(t *testing.T) {
	handler := SecurityHeaders("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, p := range []string{"/v1/swagger/index.html", "/v1/swagger/swagger-ui.css"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		assert.Empty(t, rec.Header().Get("Content-Security-Policy"), "path %s", p)
		// Other headers still set even on swagger.
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
}

// ---- Composed pipeline (smoke: SecurityHeaders → Middleware → CSRF → handler) ----

func TestComposedPipeline_GETPasses(t *testing.T) {
	v := stubVerifier{id: &Identity{Subject: "alice", IssuedBy: "oidc:test"}}

	var got *Identity
	final := capture(&got)
	chain := SecurityHeaders("")(Middleware([]Verifier{v})(RequireCSRFHeader(final)))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/whatever", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Subject)
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.True(t, strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'"))
}

func TestComposedPipeline_POSTWithoutCSRFHeaderRejected(t *testing.T) {
	v := stubVerifier{id: &Identity{Subject: "alice"}}
	chain := SecurityHeaders("")(Middleware([]Verifier{v})(RequireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler must not run")
		w.WriteHeader(http.StatusOK)
	}))))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/whatever", nil))

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
