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

// Service API authorization is published directly to Redis after each control
// plane save. Menu reads remain a separate current-policy path.
func TestRedisCurrentPolicyImmediateSaveIntegration(t *testing.T) {
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
	r := NewRBACV1Repository(pool).WithPolicyCache(cache)
	svc := biz.NewRBACV1Service(r)

	appKey := "live-" + uuid.NewString()
	serviceKey := "orders-" + uuid.NewString()
	subject := "u-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, serviceKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, serviceKey)
	})

	if _, err := r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: appKey, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceApplicationServices(ctx, appKey, []string{serviceKey}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := r.SaveEndpoint(ctx, serviceKey, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	menu, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "menu", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := r.SaveRole(ctx, appKey, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}, MenuIDs: []uuid.UUID{menu.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceSubjectRoles(ctx, appKey, subject, "tenant", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}

	input := map[string]any{"subject": subject, "domain": "tenant", "service_resource": serviceKey, "method": "GET", "path": "/orders"}
	decision, err := svc.Authorize(ctx, appKey, input)
	if err != nil || !decision.Allowed {
		t.Fatalf("allow = %+v, %v", decision, err)
	}
	identity := biz.SubjectAPIIdentity{ApplicationKey: appKey, Subject: subject, Domain: "tenant", ServiceResource: serviceKey}
	published, err := cache.LoadSubjectAPIs(ctx, identity)
	if err != nil || len(published.APIs) != 1 {
		t.Fatalf("published = %+v, %v", published, err)
	}

	role.Enabled = false
	if _, err := r.SaveRole(ctx, appKey, role); err != nil {
		t.Fatal(err)
	}
	decision, err = svc.Authorize(ctx, appKey, input)
	if err != nil || decision.Allowed {
		t.Fatalf("revoke = %+v, %v", decision, err)
	}

	if _, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{ID: menu.ID, Code: "orders", Name: "Orders changed", MenuType: "menu", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	afterMenu, err := cache.LoadSubjectAPIs(ctx, identity)
	if err != nil || len(afterMenu.APIs) != 0 || afterMenu.ServiceResource != published.ServiceResource {
		t.Fatalf("menu save changed API record = %+v, %v", afterMenu, err)
	}
}
