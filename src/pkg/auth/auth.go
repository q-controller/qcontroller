package auth

import (
	"context"
	"errors"
	"net/http"

	v1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
)

// ErrUnauthenticated signals a Verifier had no opinion on the request — the
// next Verifier in the chain should be tried. Any other error fails the
// request immediately with 401.
var ErrUnauthenticated = errors.New("unauthenticated")

// Anonymous is the identity used when auth is disabled.
func Anonymous() *v1.Identity {
	return &v1.Identity{Subject: "anonymous", IssuedBy: "anonymous"}
}

// Verifier extracts an Identity from a request.
type Verifier interface {
	Verify(r *http.Request) (*v1.Identity, error)
}

type ctxKey struct{}

// WithIdentity returns a context carrying the given identity.
func WithIdentity(ctx context.Context, id *v1.Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the identity attached to the context, or nil.
func FromContext(ctx context.Context) *v1.Identity {
	id, _ := ctx.Value(ctxKey{}).(*v1.Identity)
	return id
}
