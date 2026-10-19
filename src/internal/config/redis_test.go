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
		{"enabled defaults", RedisConfig{Enabled: true, URL: "redis://localhost:6379/0"}, false},
		{"enabled empty URL", RedisConfig{Enabled: true}, false},
		{"enabled TLS", RedisConfig{Enabled: true, URL: "rediss://user:secret@localhost:6379/1"}, false},
		{"enabled bad URL", RedisConfig{Enabled: true, URL: "http://user:private-secret@localhost"}, true},
		{"enabled bad TTL", RedisConfig{Enabled: true, URL: "redis://localhost", CacheTTL: "0s"}, true},
		{"enabled bad timeout", RedisConfig{Enabled: true, URL: "redis://localhost", Timeout: "-1s"}, true},
		{"enabled too short timeout", RedisConfig{Enabled: true, URL: "redis://localhost", Timeout: "1ns"}, true},
		{"enabled bad prefix", RedisConfig{Enabled: true, URL: "redis://localhost", KeyPrefix: "test:{app}"}, true},
		{"disabled skips validation", RedisConfig{URL: "http://localhost", KeyPrefix: "test:{app}", CacheTTL: "0s", Timeout: "1ns"}, false},
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

func TestLoadRedisEnabledDefaultsAndYAML(t *testing.T) {
	for _, tt := range []struct {
		name        string
		redis       string
		wantEnabled bool
	}{
		{
			name:        "omitted defaults enabled",
			redis:       `{url: "redis://yaml-host:6379"}`,
			wantEnabled: true,
		},
		{
			name:        "YAML disables",
			redis:       `{enabled: false, url: "http://invalid", key_prefix: "bad:{prefix}", cache_ttl: "0s", timeout: "1ns"}`,
			wantEnabled: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearRedisEnv(t)
			cfg, err := Load(writeRedisConfig(t, tt.redis))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Redis.Enabled != tt.wantEnabled {
				t.Fatalf("Redis.Enabled = %t, want %t", cfg.Redis.Enabled, tt.wantEnabled)
			}
		})
	}
}

func TestLoadRedisEnabledEnvironmentOverridesYAML(t *testing.T) {
	for _, tt := range []struct {
		name        string
		redis       string
		env         string
		wantEnabled bool
	}{
		{"env true overrides YAML false", `{enabled: false}`, "true", true},
		{"env false overrides YAML true", `{enabled: true}`, "false", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearRedisEnv(t)
			t.Setenv("NEXUSAUTHORIZATION_REDIS_ENABLED", tt.env)
			cfg, err := Load(writeRedisConfig(t, tt.redis))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Redis.Enabled != tt.wantEnabled {
				t.Fatalf("Redis.Enabled = %t, want %t", cfg.Redis.Enabled, tt.wantEnabled)
			}
		})
	}
}

func TestLoadRedisEnabledRejectsEmptyOrInvalidValue(t *testing.T) {
	for _, value := range []string{"", "not-a-boolean"} {
		t.Run(value, func(t *testing.T) {
			clearRedisEnv(t)
			t.Setenv("NEXUSAUTHORIZATION_REDIS_ENABLED", value)
			if _, err := Load(writeRedisConfig(t, `{url: "redis://localhost:6379"}`)); err == nil {
				t.Fatalf("Load accepted NEXUSAUTHORIZATION_REDIS_ENABLED=%q", value)
			}
		})
	}
}

func TestLoadRedisEnabledWithEmptyURLRemainsEnabled(t *testing.T) {
	clearRedisEnv(t)
	path := writeRedisConfig(t, `{enabled: true, url: ""}`)
	t.Setenv("NEXUSAUTHORIZATION_REDIS_URL", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Redis.Enabled || cfg.Redis.URL != "" {
		t.Fatalf("Redis = %#v, want enabled with empty URL", cfg.Redis)
	}
}

func TestLoadRedisEnvironmentOverrides(t *testing.T) {
	clearRedisEnv(t)
	path := writeRedisConfig(t, `{url: "redis://yaml-host:6379", key_prefix: "nexusauthorization:yaml"}`)
	t.Setenv("NEXUSAUTHORIZATION_REDIS_URL", "rediss://redis.example.com:6380/2")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_KEY_PREFIX", "nexusauthorization:prod")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_CACHE_TTL", "12h")
	t.Setenv("NEXUSAUTHORIZATION_REDIS_TIMEOUT", "100ms")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Redis.Enabled || cfg.Redis.URL != "rediss://redis.example.com:6380/2" || cfg.Redis.Prefix() != "nexusauthorization:prod" || cfg.Redis.CacheTTL != "12h" || cfg.Redis.Timeout != "100ms" {
		t.Fatalf("Redis env override failed: %#v", cfg.Redis)
	}
}

func writeRedisConfig(t *testing.T, redis string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	config := `
http: {addr: ":8080"}
database: {url: "postgres://localhost/test"}
service_resource: {source: local}
redis: ` + redis + `
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func clearRedisEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NEXUSAUTHORIZATION_REDIS_ENABLED",
		"NEXUSAUTHORIZATION_REDIS_URL",
		"NEXUSAUTHORIZATION_REDIS_KEY_PREFIX",
		"NEXUSAUTHORIZATION_REDIS_CACHE_TTL",
		"NEXUSAUTHORIZATION_REDIS_TIMEOUT",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
