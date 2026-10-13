package config

import (
	"testing"
)

func TestAPITokenConfiguration(t *testing.T) {
	t.Setenv("NEXUSAUTHORIZATION_API_TOKEN_ADMIN_SUBJECTS", "admin-1,admin-2")
	t.Setenv("NEXUSAUTHORIZATION_API_TOKEN_INTERNAL_KEY", "separate-key-with-at-least-32-bytes")
	var cfg APITokenConfig
	if err := applyAPITokenConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceResource != "nexusauthorization" || len(cfg.AdminSubjects) != 2 || len(cfg.InternalCallers) != 1 || cfg.InternalCallers[0].ServiceResource != "nexusauth" {
		t.Fatalf("invalid config: %+v", cfg)
	}
	t.Setenv("NEXUSAUTHORIZATION_API_TOKEN_INTERNAL_KEY", "")
	for _, callers := range [][]APITokenInternalCaller{
		{{ServiceResource: "nexusauth", Key: "short"}},
		{{ServiceResource: "a", Key: "same-key-of-at-least-thirty-two-bytes"}, {ServiceResource: "b", Key: "same-key-of-at-least-thirty-two-bytes"}},
		{{ServiceResource: "a", Key: "first-key-of-at-least-thirty-two-bytes"}, {ServiceResource: "a", Key: "other-key-of-at-least-thirty-two-bytes"}},
	} {
		if err := applyAPITokenConfig(&APITokenConfig{InternalCallers: callers}); err == nil {
			t.Fatal("unsafe bootstrap configuration accepted")
		}
	}
}
