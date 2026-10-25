package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig configures the Redis runtime store for service API authorization.
type RedisConfig struct {
	Enabled   bool   `yaml:"enabled"`
	URL       string `yaml:"url"`
	KeyPrefix string `yaml:"key_prefix"`
	// CacheTTL is accepted for compatibility but no longer used: service API
	// records never expire and are kept current by publication.
	CacheTTL string `yaml:"cache_ttl"`
	Timeout  string `yaml:"timeout"`
	// ReconcileInterval is how often the background publisher rebuilds the
	// full service/user snapshot, repairing failed saves and catalog changes.
	ReconcileInterval string `yaml:"reconcile_interval"`
}

func (c RedisConfig) Prefix() string {
	if value := strings.TrimSpace(c.KeyPrefix); value != "" {
		return value
	}
	return "nexusauthorization:dev"
}

func (c RedisConfig) TTLDuration() (time.Duration, error) {
	return redisDuration(c.CacheTTL, 24*time.Hour, "redis.cache_ttl")
}

func (c RedisConfig) TimeoutDuration() (time.Duration, error) {
	return redisDuration(c.Timeout, 200*time.Millisecond, "redis.timeout")
}

func (c RedisConfig) ReconcileIntervalDuration() (time.Duration, error) {
	duration, err := redisDuration(c.ReconcileInterval, 30*time.Second, "redis.reconcile_interval")
	if err == nil && duration < time.Second {
		return 0, fmt.Errorf("redis.reconcile_interval must be at least 1s")
	}
	return duration, err
}

func redisDuration(value string, fallback time.Duration, name string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration < time.Millisecond {
		return 0, fmt.Errorf("%s must be a duration of at least 1ms", name)
	}
	return duration, nil
}

func (c RedisConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if url := strings.TrimSpace(c.URL); url != "" {
		if _, err := redis.ParseURL(url); err != nil {
			// Never include the URL or parser error: either may contain credentials.
			return fmt.Errorf("redis.url must be a valid redis:// or rediss:// URL")
		}
	}
	if strings.ContainsAny(c.Prefix(), "{} \t\r\n") || strings.HasSuffix(c.Prefix(), ":") {
		return fmt.Errorf("redis.key_prefix must not contain braces or whitespace or end with a colon")
	}
	if _, err := c.TTLDuration(); err != nil {
		return err
	}
	if _, err := c.ReconcileIntervalDuration(); err != nil {
		return err
	}
	_, err := c.TimeoutDuration()
	return err
}

func applyRedisEnv(cfg *RedisConfig) error {
	if value, ok := os.LookupEnv("NEXUSAUTHORIZATION_REDIS_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("NEXUSAUTHORIZATION_REDIS_ENABLED must be a valid boolean: %w", err)
		}
		cfg.Enabled = enabled
	}
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"NEXUSAUTHORIZATION_REDIS_URL", &cfg.URL},
		{"NEXUSAUTHORIZATION_REDIS_KEY_PREFIX", &cfg.KeyPrefix},
		{"NEXUSAUTHORIZATION_REDIS_CACHE_TTL", &cfg.CacheTTL},
		{"NEXUSAUTHORIZATION_REDIS_TIMEOUT", &cfg.Timeout},
		{"NEXUSAUTHORIZATION_REDIS_RECONCILE_INTERVAL", &cfg.ReconcileInterval},
	} {
		if value, ok := os.LookupEnv(field.key); ok {
			*field.dst = value
		}
	}
	return nil
}
