package identity

import (
	"context"

	v1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
)

type ctxKey struct{}

// WithContext returns a context carrying the given identity.
func WithContext(ctx context.Context, id *v1.Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the identity attached to the context, or nil.
func FromContext(ctx context.Context) *v1.Identity {
	id, _ := ctx.Value(ctxKey{}).(*v1.Identity)
	return id
}
