package identity

import (
	"cmp"
	"context"
	"log/slog"
)

// LogHandler wraps a slog.Handler so every record whose context carries an
// Identity gets `user` and `issuedBy` attributes.
type logHandler struct {
	slog.Handler
}

func LogHandler(base slog.Handler) slog.Handler {
	return &logHandler{Handler: base}
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != nil {
		display := cmp.Or(id.Email, id.Name, id.Subject)
		r.AddAttrs(
			slog.String("user", display),
			slog.String("issuedBy", id.IssuedBy),
		)
		if display != id.Subject {
			r.AddAttrs(slog.String("subject", id.Subject))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	return &logHandler{Handler: h.Handler.WithGroup(name)}
}
