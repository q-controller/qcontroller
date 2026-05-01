package auth

import (
	"context"
	"errors"
	"net/http"
)

// ErrUnauthenticated signals a Verifier had no opinion on the request — the
// next Verifier in the chain should be tried. Any other error fails the
// request immediately with 401.
var ErrUnauthenticated = errors.New("unauthenticated")

// Identity is the result of successfully verifying a request.
type Identity struct {
	Subject  string   `json:"subject"`
	Email    string   `json:"email,omitempty"`
	Name     string   `json:"name,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	IssuedBy string   `json:"issuedBy"`
}

// Anonymous is the identity used when auth is disabled.
func Anonymous() *Identity {
	return &Identity{Subject: "anonymous", IssuedBy: "anonymous"}
}

// Verifier extracts an Identity from a request.
type Verifier interface {
	Verify(r *http.Request) (*Identity, error)
}

type ctxKey struct{}

// WithIdentity returns a context carrying the given identity.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the identity attached to the context, or nil.
func FromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(ctxKey{}).(*Identity)
	return id
}
