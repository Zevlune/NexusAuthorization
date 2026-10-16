package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedisConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cfg     RedisConfig
		invalid bool
	}{
		{"disabled", RedisConfig{}, false},
		{"defaults", RedisConfig{URL: "redis://localhost:6379/0"}, false},
		{"tls", RedisConfig{URL: "rediss://user:secret@localhost:6379/1"}, false},
		{"bad URL", RedisConfig{URL: "http://user:private-secret@localhost"}, true},
		{"bad TTL", RedisConfig{URL: "redis://localhost", CacheTTL: "0s"}, true},
		{"bad timeout", RedisConfig{URL: "redis://localhost", Timeout: "-1s"}, true},
		{"too short timeout", RedisConfig{URL: "redis://localhost", Timeout: "1ns"}, true},
		{"bad prefix", RedisConfig{URL: "redis://localhost", KeyPrefix: "test:{app}"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.invalid {
				t.Fatalf("Validate = %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-secret") {
				t.Fatal("credentials leaked")
			}
		})
	}
	cfg := RedisConfig{}
	if ttl, err := cfg.TTLDuration(); err != nil || ttl != 24*time.Hour {
		t.Fatalf("TTL = %s, %v", ttl, err)
	}
	if timeout, err := cfg.TimeoutDuration(); err != nil || timeout != 200*time.Millisecond {
		t.Fatalf("timeout = %s, %v", timeout, err)
	}
}

func TestLoadRedisEnvironmentAndDisableOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(`
http: {addr: ":8080"}
database: {url: "postgres://localhost/test"}
service_resource: {source: local}
redis: {url: "redis://yaml-host:6379", key_prefix: "nexusauthorization:yaml"}
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_REDIS_URL", "rediss://redis.example.com:6380/2")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_KEY_PREFIX", "nexusauthorization:prod")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_CACHE_TTL", "12h")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_TIMEOUT", "100ms")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Redis.URL != "rediss://redis.example.com:6380/2" || cfg.Redis.Prefix() != "nexusauthorization:prod" || cfg.Redis.CacheTTL != "12h" || cfg.Redis.Timeout != "100ms" {
		t.Fatalf("Redis env override failed: %#v", cfg.Redis)
	}
	t.Setenv("NEXUSAUTHORIZATION_REDIS_URL", "")
	cfg, err = Load(path)
	if err != nil || cfg.Redis.URL != "" {
		t.Fatalf("empty env should disable Redis: %v", err)
	}
}
