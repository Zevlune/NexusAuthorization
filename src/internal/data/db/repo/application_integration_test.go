package repo

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

// Requires a disposable database initialized only with migrations/001_init.sql.
func TestApplicationPermissionIntegration(t *testing.T) {
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
	r := NewRBACV1Repository(pool)
	suffix := uuid.NewString()
	appKey := "console-" + suffix
	otherKey := "other-" + suffix
	s1 := "orders-" + suffix
	s2 := "inventory-" + suffix
	s3 := "unlinked-" + suffix
	for _, key := range []string{s1, s2, s3} {
		if _, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, key); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		// Remove API grants before deleting application-service links.
		pool.Exec(ctx, `DELETE FROM roles WHERE application_id IN (SELECT id FROM applications WHERE app_key=ANY($1))`, []string{appKey, otherKey})
		// Delete children before parents because the menu parent FK is RESTRICT.
		pool.Exec(ctx, `DELETE FROM menus WHERE application_id IN (SELECT id FROM applications WHERE app_key=ANY($1)) AND parent_id IS NOT NULL`, []string{appKey, otherKey})
		pool.Exec(ctx, `DELETE FROM menus WHERE application_id IN (SELECT id FROM applications WHERE app_key=ANY($1))`, []string{appKey, otherKey})
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=ANY($1)`, []string{appKey, otherKey})
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=ANY($1)`, []string{s1, s2, s3})
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=ANY($1)`, []string{s1, s2, s3})
	})
	app, err := r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Console", URL: "https://example.com/console", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.URL != "https://example.com/console" {
		t.Fatalf("application URL not persisted: %q", app.URL)
	}
	app.URL = "http://localhost:5274/updated"
	app, err = r.SaveApplication(ctx, app)
	if err != nil || app.URL != "http://localhost:5274/updated" {
		t.Fatalf("application URL update failed: %+v %v", app, err)
	}
	applications, err := r.ListApplications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range applications {
		if item.Key == appKey {
			found = item.URL == app.URL
		}
	}
	if !found {
		t.Fatal("application list did not return saved URL")
	}
	_, err = r.SaveApplication(ctx, biz.Application{Key: otherKey, DisplayName: "Other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ReplaceApplicationServices(ctx, appKey, []string{s1, s2}); err != nil {
		t.Fatal(err)
	}
	if err = r.ReplaceApplicationServices(ctx, otherKey, []string{s1}); err != nil {
		t.Fatal(err)
	}
	services, err := r.ListApplicationServices(ctx, appKey)
	if err != nil || len(services) != 2 {
		t.Fatalf("linked services %+v %v", services, err)
	}
	if services[0].Key != s2 || services[1].Key != s1 || services[0].CreatedAt.IsZero() {
		t.Fatalf("incorrect linked service fields or ordering: %+v", services)
	}
	if _, err := pool.Exec(ctx, `UPDATE service_resources SET enabled=FALSE WHERE resource_key=$1`, s1); err == nil {
		t.Fatal("disabled a linked local service")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, s1); err == nil {
		t.Fatal("deleted a linked local service")
	}
	e1, err := r.SaveEndpoint(ctx, s1, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := r.SaveEndpoint(ctx, s2, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e3, err := r.SaveEndpoint(ctx, s3, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "directory", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	child, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "list", Name: "List", MenuType: "menu", ParentID: &parent.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.SaveApplicationMenu(ctx, otherKey, biz.ApplicationMenu{Code: "bad", Name: "Bad", MenuType: "menu", ParentID: &parent.ID, Enabled: true})
	if !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("cross-app menu accepted: %v", err)
	}
	parent.ParentID = &child.ID
	if _, err = r.SaveApplicationMenu(ctx, appKey, parent); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("menu cycle accepted: %v", err)
	}
	_, err = r.SaveRole(ctx, appKey, biz.RBACRole{Code: "bad", Name: "Bad", Enabled: true, EndpointIDs: []uuid.UUID{e3.ID}})
	if !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unlinked endpoint accepted: %v", err)
	}
	role, err := r.SaveRole(ctx, appKey, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{e1.ID}, MenuIDs: []uuid.UUID{child.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if role.ApplicationID != app.ID {
		t.Fatalf("wrong application %s", role.ApplicationID)
	}
	if err = r.ReplaceApplicationServices(ctx, appKey, []string{s1, s2}); err != nil {
		t.Fatalf("no-op association rejected: %v", err)
	}
	if err = r.ReplaceApplicationServices(ctx, appKey, []string{s2}); err == nil {
		t.Fatal("removed service with API grants")
	}
	if err = r.ReplaceSubjectRoles(ctx, otherKey, "u", "d", []uuid.UUID{role.ID}); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("cross-app binding accepted: %v", err)
	}
	if err = r.ReplaceSubjectRoles(ctx, appKey, "u", "d", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	svc := biz.NewRBACV1Service(r)
	input := map[string]any{"subject": "u", "domain": "d", "service_resource": s1, "method": "GET", "path": "/items/123"}
	d, err := svc.Authorize(ctx, appKey, input)
	if err != nil || !d.Allowed {
		t.Fatalf("allow failed %+v %v", d, err)
	}
	input["service_resource"] = s2
	d, err = svc.Authorize(ctx, appKey, input)
	if err != nil || d.Allowed {
		t.Fatalf("same path cross service leak %+v %v", d, err)
	}
	input["service_resource"] = s1
	d, err = svc.Authorize(ctx, otherKey, input)
	if err != nil || d.Allowed {
		t.Fatalf("cross application leak %+v %v", d, err)
	}
	menus, err := svc.SubjectMenus(ctx, appKey, "u", "d")
	if err != nil || len(menus) != 2 {
		t.Fatalf("current menus %+v %v", menus, err)
	}
	role.EndpointIDs = append(role.EndpointIDs, e2.ID)
	if _, err = r.SaveRole(ctx, appKey, role); err != nil {
		t.Fatal(err)
	}
	input["service_resource"] = s2
	d, err = svc.Authorize(ctx, appKey, input)
	if err != nil || !d.Allowed {
		t.Fatalf("saved grant not effective: %+v %v", d, err)
	}
	role.EndpointIDs = nil
	if _, err = r.SaveRole(ctx, appKey, role); err != nil {
		t.Fatal(err)
	}
	d, err = svc.Authorize(ctx, appKey, input)
	if err != nil || d.Allowed {
		t.Fatalf("saved revocation not effective: %+v %v", d, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.SaveRole(ctx, appKey, role); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	d, err = svc.Authorize(ctx, appKey, input)
	if err != nil || d.Allowed {
		t.Fatalf("concurrent saves lost revocation: %+v %v", d, err)
	}
	endpoints, total, err := r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 2 || len(endpoints) != 2 {
		t.Fatalf("linked catalog %+v %d %v", endpoints, total, err)
	}
}
