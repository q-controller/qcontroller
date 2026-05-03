// TODO: split into pkg/contextutil, pkg/fileutil, pkg/urlutil, pkg/configutil
// to give each helper a domain-specific home. Suppressing the lint until then.
//
//nolint:revive // package name "utils" — see TODO above
package utils

import "context"

// AsyncCtx returns a context that preserves values from parent (Identity,
// trace context, etc.) but is decoupled from parent's cancellation. It is
// canceled when lifetime is signaled (closed or sent on) or when the returned
// cancel is called.
//
// Use for fire-and-forget work that must outlive the originating request but
// stop when the surrounding service shuts down. Pass a service-lifetime
// signal as lifetime — typically a `chan struct{}` closed at shutdown, or
// the result of a service-scoped context's `Done()`.
func AsyncCtx(parent context.Context, lifetime <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	go func() {
		select {
		case <-lifetime:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
