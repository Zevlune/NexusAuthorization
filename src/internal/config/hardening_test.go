package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedisReconcileInterval(t *testing.T) {
	clearRedisEnv(t)
	cfg, err := Load(writeRedisConfig(t, `{url: "redis://yaml-host:6379"}`))
	if err != nil {
		t.Fatal(err)
	}
	if interval, err := cfg.Redis.ReconcileIntervalDuration(); err != nil || interval != 30*time.Second {
		t.Fatalf("default interval = %s, %v", interval, err)
	}

	cfg, err = Load(writeRedisConfig(t, `{url: "redis://yaml-host:6379", reconcile_interval: "5s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if interval, _ := cfg.Redis.ReconcileIntervalDuration(); interval != 5*time.Second {
		t.Fatalf("YAML interval = %s", interval)
	}

	t.Setenv("NEXUSAUTHORIZATION_REDIS_RECONCILE_INTERVAL", "2m")
	cfg, err = Load(writeRedisConfig(t, `{url: "redis://yaml-host:6379", reconcile_interval: "5s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if interval, _ := cfg.Redis.ReconcileIntervalDuration(); interval != 2*time.Minute {
		t.Fatalf("environment interval = %s", interval)
	}

	for _, value := range []string{"500ms", "soon", "-1s"} {
		t.Setenv("NEXUSAUTHORIZATION_REDIS_RECONCILE_INTERVAL", value)
		if _, err := Load(writeRedisConfig(t, `{url: "redis://yaml-host:6379"}`)); err == nil || !strings.Contains(err.Error(), "reconcile_interval") {
			t.Fatalf("interval %q accepted: %v", value, err)
		}
	}
	// A disabled Redis does not validate its settings.
	if _, err := Load(writeRedisConfig(t, `{enabled: false}`)); err != nil {
		t.Fatalf("disabled Redis validated its interval: %v", err)
	}
}

func writeSwaggerImportConfig(t *testing.T, swaggerImport string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	config := `
http: {addr: ":8080"}
database: {url: "postgres://localhost/test"}
service_resource: {source: local}
redis: {enabled: false}
swagger_import: ` + swaggerImport + `
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSwaggerImportConfiguration(t *testing.T) {
	clearRedisEnv(t)
	cfg, err := Load(writeSwaggerImportConfig(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SwaggerImport.AllowLoopback || len(cfg.SwaggerImport.AllowedHosts) != 0 {
		t.Fatalf("defaults = %+v, want no loopback and no allowlist", cfg.SwaggerImport)
	}

	cfg, err = Load(writeSwaggerImportConfig(t, `{allow_loopback: true, allowed_hosts: ["docs.internal", "10.0.0.0/8"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SwaggerImport.AllowLoopback || strings.Join(cfg.SwaggerImport.AllowedHosts, ",") != "docs.internal,10.0.0.0/8" {
		t.Fatalf("YAML = %+v", cfg.SwaggerImport)
	}

	t.Setenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOW_LOOPBACK", "false")
	t.Setenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOWED_HOSTS", "a.example, 192.168.0.0/16")
	cfg, err = Load(writeSwaggerImportConfig(t, `{allow_loopback: true}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SwaggerImport.AllowLoopback || strings.Join(cfg.SwaggerImport.AllowedHosts, ",") != "a.example,192.168.0.0/16" {
		t.Fatalf("environment = %+v", cfg.SwaggerImport)
	}

	t.Setenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOW_LOOPBACK", "maybe")
	if _, err := Load(writeSwaggerImportConfig(t, `{}`)); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	t.Setenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOW_LOOPBACK", "")
	if err := os.Unsetenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOW_LOOPBACK"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOWED_HOSTS", "10.0.0.0/33")
	if _, err := Load(writeSwaggerImportConfig(t, `{}`)); err == nil || !strings.Contains(err.Error(), "CIDR") {
		t.Fatalf("invalid CIDR accepted: %v", err)
	}
}
