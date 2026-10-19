package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
)

func TestRedisEnabledRequiresURLBeforeOpeningDatabase(t *testing.T) {
	for _, url := range []string{"", " \t"} {
		application, err := New(context.Background(), &config.Config{
			Redis:    config.RedisConfig{Enabled: true, URL: url},
			Database: config.DatabaseConfig{URL: "://invalid-database"},
		})
		if application != nil || err == nil || !strings.Contains(err.Error(), "redis.url is required") {
			t.Fatalf("enabled Redis without URL: application=%v error=%v", application, err)
		}
	}
}

// Requires a disposable database initialized with migrations/001_init.sql.
// No Redis server or URL is used: this exercises startup wiring and actual
// configuration saves as well as decisions in database mode.
func TestRedisDisabledApplicationIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	application, err := New(ctx, &config.Config{
		Database:        config.DatabaseConfig{URL: dsn, MaxConns: 4},
		ServiceResource: config.ServiceResourceConfig{Source: biz.ServiceResourceSourceLocal},
		Redis:           config.RedisConfig{Enabled: false, URL: "invalid-unused-url"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if application.PolicyCache != nil {
		t.Fatal("disabled Redis created a cache client")
	}

	suffix := uuid.NewString()
	serviceKey, appKey := "db-service-"+suffix, "db-app-"+suffix
	t.Cleanup(func() {
		for _, query := range []struct{ sql, key string }{
			{`DELETE FROM roles WHERE application_id IN (SELECT id FROM applications WHERE app_key=$1)`, appKey},
			{`DELETE FROM applications WHERE app_key=$1`, appKey},
			{`DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey},
			{`DELETE FROM service_resources WHERE resource_key=$1`, serviceKey},
		} {
			if _, err := application.Pool.Exec(ctx, query.sql, query.key); err != nil {
				t.Errorf("cleanup database authorization fixture: %v", err)
			}
		}
	})
	if _, err := application.ServiceResources.Create(ctx, &biz.ServiceResource{Key: serviceKey, DisplayName: serviceKey, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	rbac := application.RBACV1
	if _, err := rbac.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: appKey, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := rbac.ReplaceApplicationServices(ctx, appKey, []string{serviceKey}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := rbac.SaveEndpoint(ctx, serviceKey, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := rbac.SaveRole(ctx, appKey, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rbac.ReplaceSubjectRoles(ctx, appKey, "user-1", "", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"service_resource": serviceKey, "subject": "user-1", "method": "GET", "path": "/orders"}
	decision, err := rbac.Authorize(ctx, appKey, input)
	if err != nil || !decision.Allowed {
		t.Fatalf("database grant = %+v, %v", decision, err)
	}
	if err := rbac.ReplaceSubjectRoles(ctx, appKey, "user-1", "", nil); err != nil {
		t.Fatal(err)
	}
	decision, err = rbac.Authorize(ctx, appKey, input)
	if err != nil || decision.Allowed {
		t.Fatalf("database revocation = %+v, %v", decision, err)
	}
}
