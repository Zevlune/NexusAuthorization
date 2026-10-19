package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Retry only transient discovery failures. The authority and issuer validation
// stay unchanged on every attempt, and token exchanges are never retried here.
func discoverOIDCProvider(ctx context.Context, authority string, client *http.Client) (*oidc.Provider, error) {
	const attempts = 3
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("discover oidc provider: %w", err)
		}
		provider, err := oidc.NewProvider(withOIDCHTTPClient(ctx, client), authority)
		if err == nil {
			return provider, nil
		}
		if ctx.Err() != nil || attempt == attempts || !temporaryDiscoveryError(err) {
			return nil, fmt.Errorf("discover oidc provider from %q after %d attempt(s): %w", authority, attempt, err)
		}
		delay := time.Duration(attempt) * 500 * time.Millisecond
		slog.WarnContext(ctx, "OIDC discovery request failed; retrying.",
			slog.Int("Attempt", attempt), slog.Duration("RetryDelay", delay), slog.Any("Error", err))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("discover oidc provider: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func temporaryDiscoveryError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
