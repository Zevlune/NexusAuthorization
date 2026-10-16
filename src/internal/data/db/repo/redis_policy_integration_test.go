package repo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

func TestRedisCurrentPolicyImmediateSaveIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	server := miniredis.RunT(t)
	cache, err := rediscache.New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization:test", CacheTTL: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	r := NewRBACV1Repository(pool).WithPolicyCache(cache)
	svc := biz.NewRBACV1Service(r)
	key := "live-" + uuid.NewString()
	serviceKey := "orders-" + uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name)VALUES($1,$1)`, serviceKey); err != nil {
		t.Fatal(err)
	}
	app, err := r.SaveApplication(ctx, biz.Application{Key: key, DisplayName: "Live permissions", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM roles WHERE application_id=$1`, app.ID)
		pool.Exec(ctx, `DELETE FROM menus WHERE application_id=$1`, app.ID)
		pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, app.ID)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, serviceKey)
	})
	if err = r.ReplaceApplicationServices(ctx, key, []string{serviceKey}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := r.SaveEndpoint(ctx, serviceKey, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	menu, err := r.SaveApplicationMenu(ctx, key, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "menu", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := r.SaveRole(ctx, key, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}, MenuIDs: []uuid.UUID{menu.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ReplaceSubjectRoles(ctx, key, "u", "tenant", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"subject": "u", "domain": "tenant", "service_resource": serviceKey, "method": "GET", "path": "/orders/42"}
	check := func(service *biz.RBACV1Service, allowed bool) {
		t.Helper()
		decision, err := service.Authorize(ctx, key, input)
		if err != nil || decision.Allowed != allowed {
			t.Fatalf("allowed=%t decision=%+v err=%v", allowed, decision, err)
		}
	}
	check(svc, true)
	old, err := r.GetPolicyMetadata(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	old, err = r.GetCurrentPolicy(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	menus, err := svc.SubjectMenus(ctx, key, "u", "tenant")
	if err != nil || len(menus) != 1 {
		t.Fatalf("menus=%v %v", menus, err)
	}
	server.SetError("ERR outage")
	role.EndpointIDs = nil
	role.MenuIDs = nil
	if _, err = r.SaveRole(ctx, key, role); err != nil {
		t.Fatal(err)
	}
	check(svc, false)
	menus, err = svc.SubjectMenus(ctx, key, "u", "tenant")
	if err != nil || len(menus) != 0 {
		t.Fatalf("revoked menus=%v %v", menus, err)
	}
	server.SetError("")
	// A late old cache fill cannot grant after the completed save, even on a cold process.
	if err = cache.Store(ctx, old); err != nil {
		t.Fatal(err)
	}
	check(biz.NewRBACV1Service(r), false)
	current, err := r.GetPolicyMetadata(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Load(ctx, current); err != nil {
		t.Fatalf("cache not repaired: %v", err)
	}
	// Invalid save rolls back its data and invalidation token together.
	bad := role
	bad.EndpointIDs = []uuid.UUID{uuid.New()}
	if _, err = r.SaveRole(ctx, key, bad); err == nil {
		t.Fatal("invalid grant accepted")
	}
	after, err := r.GetPolicyMetadata(ctx, key)
	if err != nil || after.Token != current.Token {
		t.Fatalf("rollback changed token: %v", err)
	}
	role.EndpointIDs = []uuid.UUID{endpoint.ID}
	if _, err = r.SaveRole(ctx, key, role); err != nil {
		t.Fatal(err)
	}
	check(svc, true)
	// Direct database writes also invalidate all process indexes.
	if _, err = pool.Exec(ctx, `UPDATE api_endpoints SET enabled=FALSE WHERE id=$1`, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	check(svc, false)
	if _, err = pool.Exec(ctx, `UPDATE api_endpoints SET enabled=TRUE WHERE id=$1`, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	check(svc, true)
	if err = r.ReplaceSubjectRoles(ctx, key, "u", "tenant", nil); err != nil {
		t.Fatal(err)
	}
	check(svc, false)
	if err = r.ReplaceSubjectRoles(ctx, key, "u", "tenant", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	check(svc, true)
	if err = server.Set(rediscache.PolicyKey("nexusauthorization:test", app.ID), `{"format":999}`); err != nil {
		t.Fatal(err)
	}
	check(biz.NewRBACV1Service(r), true)
	server.FastForward(time.Hour)
	check(biz.NewRBACV1Service(r), true)
	app.Enabled = false
	if _, err = r.SaveApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authorize(ctx, key, input); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("disabled app served cached grant: %v", err)
	}
}
