package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig configures the Redis runtime store for service API authorization.
type RedisConfig struct {
	URL       string `yaml:"url"`
	KeyPrefix string `yaml:"key_prefix"`
	CacheTTL  string `yaml:"cache_ttl"`
	Timeout   string `yaml:"timeout"`
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
	if strings.TrimSpace(c.URL) == "" {
		return nil
	}
	if _, err := redis.ParseURL(strings.TrimSpace(c.URL)); err != nil {
		// Never include the URL or parser error: either may contain credentials.
		return fmt.Errorf("redis.url must be a valid redis:// or rediss:// URL")
	}
	if strings.ContainsAny(c.Prefix(), "{} \t\r\n") || strings.HasSuffix(c.Prefix(), ":") {
		return fmt.Errorf("redis.key_prefix must not contain braces or whitespace or end with a colon")
	}
	if _, err := c.TTLDuration(); err != nil {
		return err
	}
	_, err := c.TimeoutDuration()
	return err
}

func applyRedisEnv(cfg *RedisConfig) {
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"NEXUSAUTHORIZATION_REDIS_URL", &cfg.URL},
		{"NEXUSAUTHORIZATION_REDIS_KEY_PREFIX", &cfg.KeyPrefix},
		{"NEXUSAUTHORIZATION_REDIS_CACHE_TTL", &cfg.CacheTTL},
		{"NEXUSAUTHORIZATION_REDIS_TIMEOUT", &cfg.Timeout},
	} {
		if value, ok := os.LookupEnv(field.key); ok {
			*field.dst = value
		}
	}
}
