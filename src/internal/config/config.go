package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP            HTTPConfig            `yaml:"http"`
	Logging         LoggingConfig         `yaml:"logging"`
	Database        DatabaseConfig        `yaml:"database"`
	OIDC            OIDCConfig            `yaml:"oidc"`
	ServiceResource ServiceResourceConfig `yaml:"service_resource"`
	Authorization   AuthorizationConfig   `yaml:"authorization"`
	APITokens       APITokenConfig        `yaml:"api_tokens"`
}

// Internal callers are bootstrap credentials, loaded once at startup. They
// never authenticate through the API Token system they are used to query.
type APITokenInternalCaller struct {
	ServiceResource string `yaml:"service_resource"`
	Key             string `yaml:"key"`
}

type APITokenConfig struct {
	ServiceResource string                   `yaml:"service_resource"`
	AdminSubjects   []string                 `yaml:"admin_subjects"`
	InternalCallers []APITokenInternalCaller `yaml:"internal_callers"`
}

// LoggingConfig controls the shared Luck log format and destinations.
type LoggingConfig struct {
	Module       string `yaml:"module"`
	MinimumLevel string `yaml:"minimum_level"`
	FilePath     string `yaml:"file_path"`
}

// ServiceResourceConfig 配置服务资源来源和 NexusAuth 连接信息。
type ServiceResourceConfig struct {
	Source                  string `yaml:"source"`
	NexusAuthBaseURL        string `yaml:"nexusauth_base_url"`
	NexusAuthOpenAPIBaseURL string `yaml:"nexusauth_openapi_base_url"`
	APIKey                  string `yaml:"api_key"`
	OpenCredential          string `yaml:"open_credential"`
	Timeout                 string `yaml:"timeout"`
	TimeoutSeconds          int    `yaml:"timeout_seconds"`
}

func (c ServiceResourceConfig) BaseURL() string {
	if strings.TrimSpace(c.NexusAuthOpenAPIBaseURL) != "" {
		return strings.TrimSpace(c.NexusAuthOpenAPIBaseURL)
	}
	return strings.TrimSpace(c.NexusAuthBaseURL)
}

func (c ServiceResourceConfig) Credential() string {
	if strings.TrimSpace(c.OpenCredential) != "" {
		return strings.TrimSpace(c.OpenCredential)
	}
	return strings.TrimSpace(c.APIKey)
}

func (c ServiceResourceConfig) TimeoutDuration() (time.Duration, error) {
	if c.TimeoutSeconds > 0 {
		return time.Duration(c.TimeoutSeconds) * time.Second, nil
	}
	if strings.TrimSpace(c.Timeout) == "" {
		return 5 * time.Second, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(c.Timeout))
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("service_resource.timeout must be a positive duration")
	}
	return duration, nil
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
}
type AuthorizationConfig struct {
	ServiceCredential string `yaml:"service_credential"`
}
type DatabaseConfig struct {
	URL      string `yaml:"url"`
	MaxConns int32  `yaml:"max_conns"`
	MinConns int32  `yaml:"min_conns"`
}

// OIDCConfig controls the browser login flow. The service does not create an
// identity provider; it discovers the provider endpoints from Authority.
type OIDCConfig struct {
	Enabled               bool     `yaml:"enabled"`
	Authority             string   `yaml:"authority"`
	BackchannelAuthority  string   `yaml:"backchannel_authority"`
	ClientID              string   `yaml:"client_id"`
	ClientSecret          string   `yaml:"client_secret"`
	RedirectURI           string   `yaml:"redirect_uri"`
	PostLogoutRedirectURI string   `yaml:"post_logout_redirect_uri"`
	Scopes                []string `yaml:"scopes"`
	Audience              string   `yaml:"audience"`
	SessionSecret         string   `yaml:"session_secret"`
	CookieSecure          bool     `yaml:"cookie_secure"`
	CookieName            string   `yaml:"cookie_name"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_DATABASE_URL"); value != "" {
		cfg.Database.URL = value
	}
	applyOIDCEnv(&cfg.OIDC)
	if err := applyAPITokenConfig(&cfg.APITokens); err != nil {
		return nil, err
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_AUTHORIZATION_SERVICE_CREDENTIAL"); value != "" {
		cfg.Authorization.ServiceCredential = value
	}
	applyServiceResourceEnv(&cfg.ServiceResource)
	cfg.ServiceResource.Source = strings.ToLower(strings.TrimSpace(cfg.ServiceResource.Source))
	if cfg.ServiceResource.Source == "" {
		cfg.ServiceResource.Source = "nexusauth"
	}
	if cfg.ServiceResource.Source != "local" && cfg.ServiceResource.Source != "nexusauth" {
		return nil, fmt.Errorf("service_resource.source must be local or nexusauth")
	}
	if _, err := cfg.ServiceResource.TimeoutDuration(); err != nil {
		return nil, err
	}
	if cfg.ServiceResource.Source == "nexusauth" {
		if err := validateHTTPURL(cfg.ServiceResource.BaseURL(), "service_resource.nexusauth_base_url"); err != nil {
			return nil, err
		}
		if cfg.ServiceResource.Credential() == "" {
			return nil, fmt.Errorf("service_resource.api_key or service_resource.open_credential is required when source is nexusauth")
		}
	}
	if len(cfg.OIDC.Scopes) == 0 {
		cfg.OIDC.Scopes = []string{"openid", "profile", "email"}
	}
	cfg.OIDC.CookieName = "nexusauthorization_session"
	cfg.OIDC.CookieSecure = redirectUsesHTTPS(cfg.OIDC.RedirectURI)
	if cfg.HTTP.Addr == "" || cfg.Database.URL == "" {
		return nil, fmt.Errorf("http.addr and database.url are required")
	}
	if err := cfg.OIDC.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyAPITokenConfig(cfg *APITokenConfig) error {
	if value := os.Getenv("NEXUSAUTHORIZATION_API_TOKEN_SERVICE_RESOURCE"); value != "" {
		cfg.ServiceResource = value
	}
	cfg.ServiceResource = strings.TrimSpace(cfg.ServiceResource)
	if cfg.ServiceResource == "" {
		cfg.ServiceResource = "nexusauthorization"
	}
	if value, ok := os.LookupEnv("NEXUSAUTHORIZATION_API_TOKEN_ADMIN_SUBJECTS"); ok {
		cfg.AdminSubjects = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_API_TOKEN_INTERNAL_KEY"); value != "" {
		// Environment shortcut for the NexusAuth callback. Other callers may
		// have separate keys in YAML; never share one unrestricted global key.
		replaced := false
		for i := range cfg.InternalCallers {
			if cfg.InternalCallers[i].ServiceResource == "nexusauth" {
				cfg.InternalCallers[i].Key = value
				replaced = true
			}
		}
		if !replaced {
			cfg.InternalCallers = append(cfg.InternalCallers, APITokenInternalCaller{ServiceResource: "nexusauth", Key: value})
		}
	}
	services, keys := map[string]bool{}, map[string]bool{}
	for i := range cfg.InternalCallers {
		c := &cfg.InternalCallers[i]
		c.ServiceResource, c.Key = strings.TrimSpace(c.ServiceResource), strings.TrimSpace(c.Key)
		if c.ServiceResource == "" || len(c.ServiceResource) > 128 || len(c.Key) < 32 || services[c.ServiceResource] || keys[c.Key] {
			return fmt.Errorf("api_tokens.internal_callers requires unique services and unique keys of at least 32 bytes")
		}
		services[c.ServiceResource], keys[c.Key] = true, true
	}
	for i := range cfg.AdminSubjects {
		cfg.AdminSubjects[i] = strings.TrimSpace(cfg.AdminSubjects[i])
	}
	return nil
}

func applyServiceResourceEnv(cfg *ServiceResourceConfig) {
	if value := os.Getenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE"); value != "" {
		cfg.Source = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL"); value != "" {
		cfg.NexusAuthBaseURL = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY"); value != "" {
		cfg.APIKey = value
	}
}

func applyOIDCEnv(cfg *OIDCConfig) {
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_ENABLED"); value != "" {
		cfg.Enabled = strings.EqualFold(value, "true") || value == "1"
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_AUTHORITY"); value != "" {
		cfg.Authority = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_BACKCHANNEL_AUTHORITY"); value != "" {
		cfg.BackchannelAuthority = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_CLIENT_ID"); value != "" {
		cfg.ClientID = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_CLIENT_SECRET"); value != "" {
		cfg.ClientSecret = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_REDIRECT_URI"); value != "" {
		cfg.RedirectURI = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_POST_LOGOUT_REDIRECT_URI"); value != "" {
		cfg.PostLogoutRedirectURI = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_SCOPES"); value != "" {
		cfg.Scopes = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_AUDIENCE"); value != "" {
		cfg.Audience = value
	}
	if value := os.Getenv("NEXUSAUTHORIZATION_OIDC_SESSION_SECRET"); value != "" {
		cfg.SessionSecret = value
	}
}

func redirectUsesHTTPS(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(parsed.Scheme, "https")
}

func (c OIDCConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Authority) == "" || strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("oidc.authority and oidc.client_id are required when oidc is enabled")
	}
	if strings.TrimSpace(c.Audience) == "" {
		return fmt.Errorf("oidc.audience is required when oidc is enabled")
	}
	if strings.TrimSpace(c.ClientSecret) == "" {
		return fmt.Errorf("oidc.client_secret is required for token introspection when oidc is enabled")
	}
	if err := validateHTTPURL(c.Authority, "oidc.authority"); err != nil {
		return err
	}
	if strings.TrimSpace(c.BackchannelAuthority) != "" {
		if err := validateHTTPURL(c.BackchannelAuthority, "oidc.backchannel_authority"); err != nil {
			return err
		}
	}
	if err := validateHTTPURL(c.RedirectURI, "oidc.redirect_uri"); err != nil {
		return err
	}
	if err := validateHTTPURL(c.PostLogoutRedirectURI, "oidc.post_logout_redirect_uri"); err != nil {
		return err
	}
	if len(c.SessionSecret) < 32 || strings.HasPrefix(c.SessionSecret, "replace-with-") {
		return fmt.Errorf("oidc.session_secret must be at least 32 characters when oidc is enabled")
	}
	if strings.TrimSpace(c.CookieName) == "" || strings.ContainsAny(c.CookieName, " \t\r\n;") {
		return fmt.Errorf("oidc.cookie_name must be a valid cookie name")
	}
	if len(c.Scopes) == 0 {
		return fmt.Errorf("oidc.scopes must include at least one scope")
	}
	seen := make(map[string]struct{}, len(c.Scopes))
	for _, scope := range c.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return fmt.Errorf("oidc.scopes must not contain empty values")
		}
		seen[scope] = struct{}{}
	}
	if _, ok := seen["openid"]; !ok {
		return fmt.Errorf("oidc.scopes must include openid")
	}
	return nil
}

func validateHTTPURL(value, field string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute http or https URL", field)
	}
	return nil
}
