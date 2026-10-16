package repo

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

func TestSubjectAPIDirectRedisPublicationIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := newRepositoryTestPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	server := miniredis.RunT(t)
	cache, err := rediscache.New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization:test:" + uuid.NewString(), CacheTTL: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	service := "direct-api-" + uuid.NewString()
	app := "direct-app-" + uuid.NewString()
	subject := "user-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, service); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, app)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, service)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, service)
	})

	repo := NewRBACV1Repository(pool).WithPolicyCache(cache)
	rbac := biz.NewRBACV1Service(repo)
	storedApp, err := repo.SaveApplication(ctx, biz.Application{Key: app, DisplayName: app, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceApplicationServices(ctx, app, []string{service}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := repo.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, app, biz.RBACRole{Code: "reader", Name: "reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceSubjectRoles(ctx, app, subject, "", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}

	decision, err := rbac.Authorize(ctx, app, map[string]any{"service_resource": service, "subject": subject, "method": "GET", "path": "/orders"})
	if err != nil || !decision.Allowed {
		t.Fatalf("direct redis authorize = %+v, %v", decision, err)
	}
	identity := biz.SubjectAPIIdentity{ApplicationKey: app, Subject: subject, ServiceResource: service}
	before, err := cache.LoadSubjectAPIs(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if before.ApplicationID != storedApp.ID || len(before.APIs) != 1 {
		t.Fatalf("published policy = %+v", before)
	}

	role.Enabled = false
	if _, err := repo.SaveRole(ctx, app, role); err != nil {
		t.Fatal(err)
	}
	decision, err = rbac.Authorize(ctx, app, map[string]any{"service_resource": service, "subject": subject, "method": "GET", "path": "/orders"})
	if err != nil || decision.Allowed {
		t.Fatalf("revoked direct redis authorize = %+v, %v", decision, err)
	}
	beforeMenu, err := cache.LoadSubjectAPIs(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.SaveApplicationMenu(ctx, app, biz.ApplicationMenu{Code: "menu", Name: "menu", MenuType: "menu", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	afterMenu, err := cache.LoadSubjectAPIs(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterMenu.APIs) != len(beforeMenu.APIs) || len(afterMenu.RoleCodes) != len(beforeMenu.RoleCodes) || afterMenu.ServiceResource != beforeMenu.ServiceResource || afterMenu.Subject != beforeMenu.Subject {
		t.Fatalf("menu save changed service API Redis record: before=%+v after=%+v", beforeMenu, afterMenu)
	}
}
