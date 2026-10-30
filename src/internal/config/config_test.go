package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceUKIsRequiredAndEnvironmentOnly(t *testing.T) {
	clearRedisEnv(t)
	path := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://localhost/test"}
service_resource: {source: local}
redis: {enabled: false}
authorization: {management_service_resource: "yaml-value"}
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " \t "} {
		t.Setenv("SERVICEUK", value)
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "SERVICEUK") {
			t.Fatalf("SERVICEUK=%q: expected missing service error, got %v", value, err)
		}
	}
	t.Setenv("SERVICEUK", "  service.example  ")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.ServiceUK != "service.example" {
		t.Fatalf("service = %q", cfg.Runtime.ServiceUK)
	}
	t.Setenv("LUCKENV", "  UAT:west  ")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Name != "UAT:west" || cfg.Redis.Prefix() != "nexusauthorization:UAT%3Awest" {
		t.Fatalf("runtime = %+v, redis prefix = %q", cfg.Runtime, cfg.Redis.Prefix())
	}
}

func TestLoadOIDCBackchannelAuthorityFromEnvironment(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http:
  addr: ":8080"
database:
  url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"
service_resource:
  source: nexusauth
  nexusauth_base_url: "http://nexus-auth:5100"
  api_key: "directory-token"
oidc:
  enabled: true
  authority: "http://localhost:5100"
  backchannel_authority: "http://yaml-nexus-auth:5100"
  client_id: "nexus-authorization-api"
  client_secret: "test-client-secret"
  audience: "permission.center.api"
  redirect_uri: "http://localhost:8080/signin-oidc"
  post_logout_redirect_uri: "http://localhost:5274/"
  scopes: ["openid"]
  session_secret: "test-session-secret-at-least-32-characters"
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_OIDC_BACKCHANNEL_AUTHORITY", "http://env-nexus-auth:5100")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC.Authority != "http://localhost:5100" {
		t.Fatalf("public authority = %q", cfg.OIDC.Authority)
	}
	if cfg.OIDC.BackchannelAuthority != "http://env-nexus-auth:5100" {
		t.Fatalf("backchannel authority = %q", cfg.OIDC.BackchannelAuthority)
	}
	if cfg.Runtime.ServiceUK != "test.service" {
		t.Fatalf("management service = %q", cfg.Runtime.ServiceUK)
	}
	t.Setenv("SERVICEUK", "  custom.management \t")
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.ServiceUK != "custom.management" {
		t.Fatalf("management service override = %q", cfg.Runtime.ServiceUK)
	}
	t.Setenv("NEXUSAUTHORIZATION_REDIS_ENABLED", "false")
	if _, err = Load(configPath); err == nil || !strings.Contains(err.Error(), "management authorization") {
		t.Fatalf("OIDC without Redis must fail: %v", err)
	}
}

func TestLoadLoggingRemainsFromYAMLWhenEnvironmentIsSet(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource: {source: local}
logging: {module: "yaml-module", minimum_level: "info"}
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_LOGGING_MODULE", "new-environment-module")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Module != "yaml-module" {
		t.Fatalf("logging module = %q", cfg.Logging.Module)
	}
}

func TestLoadDerivesCookieSecurityAndNameFromRedirectURI(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		uri    string
		secure bool
	}{
		{name: "http", uri: "http://localhost:8080/signin-oidc", secure: false},
		{name: "https", uri: "https://permissions.example.com/signin-oidc", secure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearConfigEnv(t)
			configPath := filepath.Join(t.TempDir(), "app.yaml")
			contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource: {source: local}
oidc:
  enabled: true
  authority: "http://localhost:5100"
  client_id: "nexus-authorization-api"
  client_secret: "test-client-secret"
  audience: "permission.center.api"
  redirect_uri: "` + testCase.uri + `"
  post_logout_redirect_uri: "http://localhost:5274/"
  scopes: ["openid"]
  session_secret: "test-session-secret-at-least-32-characters"
  cookie_secure: true
  cookie_name: "yaml-cookie"
`)
			if err := os.WriteFile(configPath, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NEXUSAUTHORIZATION_OIDC_COOKIE_SECURE", "false")
			t.Setenv("NEXUSAUTHORIZATION_OIDC_COOKIE_NAME", "environment-cookie")

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.OIDC.CookieSecure != testCase.secure {
				t.Fatalf("cookie secure = %v, want %v", cfg.OIDC.CookieSecure, testCase.secure)
			}
			if cfg.OIDC.CookieName != "nexusauthorization_session" {
				t.Fatalf("cookie name = %q", cfg.OIDC.CookieName)
			}
		})
	}
}

func TestOIDCValidateBackchannelAuthority(t *testing.T) {
	cfg := validOIDCConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("missing backchannel authority should be valid: %v", err)
	}

	cfg.BackchannelAuthority = "nexus-auth:5100"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "oidc.backchannel_authority") {
		t.Fatalf("invalid backchannel authority error = %v", err)
	}
}

func TestOIDCValidateRequiresAudience(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.Audience = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "oidc.audience") {
		t.Fatalf("missing audience error = %v", err)
	}
}

func TestOIDCValidateAllowsAutomaticSessionKey(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.SessionSecret = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("automatic session key should be allowed: %v", err)
	}
	for _, secret := range []string{"short", "replace-with-at-least-32-random-characters", strings.Repeat(" ", 32)} {
		cfg.SessionSecret = secret
		if err := cfg.Validate(); err == nil {
			t.Fatal("invalid explicit session key should fail validation")
		}
	}
}

func TestOIDCValidateRequiresClientSecretForIntrospection(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.ClientSecret = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "oidc.client_secret") {
		t.Fatalf("missing client secret error = %v", err)
	}
}

func TestLoadServiceResourceFromEnvironment(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http:
  addr: ":8080"
database:
  url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"
service_resource:
  source: nexusauth
  nexusauth_base_url: "http://yaml-nexus-auth:5100"
  api_key: "yaml-token"
  timeout: "1s"
oidc:
  enabled: false
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE", "NEXUSAUTH")
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL", "http://nexus-auth:5100")
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY", "directory-token")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceResource.Source != "nexusauth" || cfg.ServiceResource.BaseURL() != "http://nexus-auth:5100" || cfg.ServiceResource.Credential() != "directory-token" {
		t.Fatalf("service-resource config = %#v", cfg.ServiceResource)
	}
	duration, err := cfg.ServiceResource.TimeoutDuration()
	if err != nil || duration != time.Second {
		t.Fatalf("timeout = %v, %v", duration, err)
	}
}

func TestLoadSCIMConfigFromEnvironment(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource: {source: local}
scim:
  nexusauth_base_url: "http://yaml-scim:8800"
  api_key: "yaml-token"
  timeout: "2s"
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_SCIM_NEXUSAUTH_BASE_URL", "https://env-scim:8800")
	t.Setenv("NEXUSAUTHORIZATION_SCIM_API_KEY", "scim-read-token")
	t.Setenv("NEXUSAUTHORIZATION_SCIM_TIMEOUT", "3s")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SCIM.NexusAuthBaseURL != "https://env-scim:8800" || cfg.SCIM.APIKey != "scim-read-token" || cfg.SCIM.Timeout != "3s" {
		t.Fatalf("SCIM config = %#v", cfg.SCIM)
	}
	duration, err := cfg.SCIM.TimeoutDuration()
	if err != nil || duration != 3*time.Second {
		t.Fatalf("SCIM timeout = %v, %v", duration, err)
	}
}

func TestLoadRejectsNexusAuthServiceResourceWithoutCredential(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource:
  source: nexusauth
  nexusauth_base_url: "http://nexus-auth:5100"
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("missing credential error = %v", err)
	}
}

func TestLoadDefaultsServiceResourceToNexusAuth(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL", "http://nexus-auth:5100")
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY", "directory-token")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceResource.Source != "nexusauth" {
		t.Fatalf("default service-resource source = %q", cfg.ServiceResource.Source)
	}
}

func TestLoadRejectsUnsupportedServiceResourceSource(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource: {source: unsupported}
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "service_resource.source must be local or nexusauth") {
		t.Fatalf("unsupported source error = %v", err)
	}
}

func TestLoadIgnoresRemovedServiceResourceTimeoutEnvironment(t *testing.T) {
	clearConfigEnv(t)
	configPath := filepath.Join(t.TempDir(), "app.yaml")
	contents := []byte(`
http: {addr: ":8080"}
database: {url: "postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable"}
service_resource:
  nexusauth_base_url: "http://nexus-auth:5100"
  api_key: "directory-token"
oidc: {enabled: false}
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_TIMEOUT_SECONDS", "not-a-number")
	if _, err := Load(configPath); err != nil {
		t.Fatalf("removed timeout_seconds environment should be ignored: %v", err)
	}
}

func validOIDCConfig() OIDCConfig {
	return OIDCConfig{
		Enabled:               true,
		Authority:             "http://localhost:5100",
		ClientID:              "nexus-authorization-api",
		ClientSecret:          "test-client-secret",
		Audience:              "permission.center.api",
		RedirectURI:           "http://localhost:8080/signin-oidc",
		PostLogoutRedirectURI: "http://localhost:5274/",
		Scopes:                []string{"openid"},
		SessionSecret:         "test-session-secret-at-least-32-characters",
		CookieName:            "nexusauthorization_session",
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NEXUSAUTHORIZATION_DATABASE_URL",
		"NEXUSAUTHORIZATION_OIDC_ENABLED",
		"NEXUSAUTHORIZATION_OIDC_AUTHORITY",
		"NEXUSAUTHORIZATION_OIDC_BACKCHANNEL_AUTHORITY",
		"NEXUSAUTHORIZATION_OIDC_CLIENT_ID",
		"NEXUSAUTHORIZATION_OIDC_CLIENT_SECRET",
		"NEXUSAUTHORIZATION_OIDC_REDIRECT_URI",
		"NEXUSAUTHORIZATION_OIDC_POST_LOGOUT_REDIRECT_URI",
		"NEXUSAUTHORIZATION_OIDC_SCOPES",
		"NEXUSAUTHORIZATION_OIDC_AUDIENCE",
		"NEXUSAUTHORIZATION_OIDC_SESSION_SECRET",
		"NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE",
		"NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL",
		"NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY",
		"NEXUSAUTHORIZATION_SCIM_NEXUSAUTH_BASE_URL",
		"NEXUSAUTHORIZATION_SCIM_API_KEY",
		"NEXUSAUTHORIZATION_SCIM_TIMEOUT",
		"NEXUSAUTHORIZATION_AUTHORIZATION_SERVICE_CREDENTIAL",
		"NEXUSAUTHORIZATION_LOGGING_MODULE",
		"NEXUSAUTHORIZATION_OIDC_COOKIE_SECURE",
		"NEXUSAUTHORIZATION_OIDC_COOKIE_NAME",
		"NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOW_LOOPBACK",
		"NEXUSAUTHORIZATION_SWAGGER_IMPORT_ALLOWED_HOSTS",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("SERVICEUK", "test.service")
	t.Setenv("LUCKENV", "test")
}
