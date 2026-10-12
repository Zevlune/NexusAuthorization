package logging

import (
	"context"
	"log/slog"
)

type contextKey struct{}

// WithLogger stores a request-scoped logger in a context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

// FromContext returns the request-scoped logger or slog.Default when absent.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(contextKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
