package runenv

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	for _, tc := range []struct{ environment, service, missing string }{
		{"", "service", EnvironmentKey},
		{"dev", "  ", ServiceKey},
		{"\t", "", "LUCKENV, SERVICEUK"},
	} {
		t.Setenv(EnvironmentKey, tc.environment)
		t.Setenv(ServiceKey, tc.service)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.missing) {
			t.Fatalf("Load(%q, %q) error = %v", tc.environment, tc.service, err)
		}
	}
	t.Setenv(EnvironmentKey, "  UAT  ")
	t.Setenv(ServiceKey, "  service.api  ")
	got, err := Load()
	if err != nil || got != (Environment{Name: "UAT", ServiceUK: "service.api"}) {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}
