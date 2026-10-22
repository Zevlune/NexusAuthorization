// Package rediscache stores service API authorization records in Redis using
// go-redis. Records are published after control-plane saves and are read
// without a database fallback.
package rediscache

import (
	"fmt"
	"strings"
	"time"

	"github.com/luck/nexus-authorization/internal/config"
	"github.com/redis/go-redis/v9"
)

type PolicyCache struct {
	client  *redis.Client
	prefix  string
	timeout time.Duration
}

func New(cfg config.RedisConfig) (*PolicyCache, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	options, err := redis.ParseURL(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("redis.url must be a valid redis:// or rediss:// URL")
	}
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		return nil, err
	}
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout = timeout, timeout, timeout
	options.PoolTimeout = timeout
	options.MaxRetries = -1
	options.ContextTimeoutEnabled = true
	return &PolicyCache{client: redis.NewClient(options), prefix: cfg.Prefix(), timeout: timeout}, nil
}

func (c *PolicyCache) Close() error { return c.client.Close() }
