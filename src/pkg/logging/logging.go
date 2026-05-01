package logging

import (
	"log/slog"
	"os"
	"time"

	"github.com/q-controller/qcontroller/src/pkg/auth"
)

func LevelFromEnv() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// CreateLogger returns a slog.Logger whose handler is wrapped with
// auth.LogHandler, so any record whose context carries an Identity (set by
// HTTP auth middleware or gRPC server interceptor) gets `user`/`issuedBy`
// attributes for free. Applies to every binary subcommand.
func CreateLogger() *slog.Logger {
	base := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: LevelFromEnv(),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
			}
			return a
		},
	})
	return slog.New(auth.LogHandler(base))
}
