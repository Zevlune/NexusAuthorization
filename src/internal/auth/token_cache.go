package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const tokenCacheCapacity = 1024

type tokenCacheEntry[T any] struct {
	value   T
	expires time.Time
}

// Only validated successes are retained. Hash keys keep raw credentials out of
// cache identities, and the fixed capacity bounds memory under token churn.
type tokenCache[T any] struct {
	mu      sync.Mutex
	entries map[string]tokenCacheEntry[T]
	flights singleflight.Group
}

func newTokenCache[T any]() *tokenCache[T] {
	return &tokenCache[T]{entries: make(map[string]tokenCacheEntry[T])}
}

func (c *tokenCache[T]) get(key string, now time.Time) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if ok && now.Before(entry.expires) {
		return entry.value, true
	}
	delete(c.entries, key)
	var zero T
	return zero, false
}

func (c *tokenCache[T]) load(ctx context.Context, credential string, now func() time.Time, fetch func(context.Context) (T, time.Time, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	digest := sha256.Sum256([]byte(credential))
	key := hex.EncodeToString(digest[:])
	if value, ok := c.get(key, now()); ok {
		return value, nil
	}
	result := c.flights.DoChan(key, func() (any, error) {
		if value, ok := c.get(key, now()); ok {
			return value, nil
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		value, expires, err := fetch(loadCtx)
		if err != nil {
			return nil, err
		}
		if err := loadCtx.Err(); err != nil {
			return nil, err
		}
		if !now().Before(expires) {
			return nil, ErrInvalidToken
		}
		c.mu.Lock()
		for prior, entry := range c.entries {
			if !now().Before(entry.expires) {
				delete(c.entries, prior)
			}
		}
		if len(c.entries) >= tokenCacheCapacity {
			for prior := range c.entries {
				delete(c.entries, prior)
				break
			}
		}
		c.entries[key] = tokenCacheEntry[T]{value: value, expires: expires}
		c.mu.Unlock()
		return value, nil
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return zero, result.Err
		}
		return result.Val.(T), nil
	}
}
