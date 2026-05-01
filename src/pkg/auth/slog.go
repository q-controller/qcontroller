package auth

import (
	"context"
	"log/slog"
)

// LogHandler wraps a slog.Handler so every record whose context carries an
// Identity (set by Middleware) gets `user` and `issuedBy` attributes. Use as
//
//	slog.SetDefault(slog.New(auth.LogHandler(slog.Default().Handler())))
//
// Callers must use the *Context log variants (slog.InfoContext, etc.) for
// the request's context to flow through.
type logHandler struct {
	base slog.Handler
}

func LogHandler(base slog.Handler) slog.Handler {
	return &logHandler{base: base}
}

func (h *logHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.base.Enabled(ctx, lvl)
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != nil {
		// `user` is the human-readable display (email > name > subject).
		// `subject` is the stable ID (only added when it differs from `user`,
		// so audit consumers can correlate even if email/name change).
		display := id.Email
		if display == "" {
			display = id.Name
		}
		if display == "" {
			display = id.Subject
		}
		r.AddAttrs(slog.String("user", display))
		if display != id.Subject {
			r.AddAttrs(slog.String("subject", id.Subject))
		}
		r.AddAttrs(slog.String("issuedBy", id.IssuedBy))
	}
	return h.base.Handle(ctx, r)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logHandler{base: h.base.WithAttrs(attrs)}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	return &logHandler{base: h.base.WithGroup(name)}
}
