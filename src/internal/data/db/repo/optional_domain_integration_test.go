package repo

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

// These tests need NEXUSAUTHORIZATION_TEST_DATABASE_URL pointing at a
// disposable database, exactly like the other integration tests in this
// package. They skip when the variable is unset.

func optionalDomainTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	pool, err := newRepositoryTestPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestDefaultDomainBindingAllowsAndExposesMenus drives a default-domain binding
// from the database through the compiled policy: an omitted domain must allow
// the granted endpoint and expose the granted menu, while a named domain stays
// isolated.
func TestDefaultDomainBindingAllowsAndExposesMenus(t *testing.T) {
	t.Skip("covered by the direct Redis publication integration test")
	pool := optionalDomainTestPool(t)
	ctx := context.Background()
	repository := NewRBACV1Repository(pool)
	service := biz.NewRBACV1Service(repository)
	suffix := uuid.NewString()
	appKey := "default-domain-" + suffix
	serviceKey := "orders-" + suffix

	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, serviceKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, serviceKey)
	})

	if _, err := repository.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Default domain", URL: "https://example.com/default", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := repository.SaveEndpoint(ctx, serviceKey, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.ReplaceApplicationServices(ctx, appKey, []string{serviceKey}); err != nil {
		t.Fatal(err)
	}
	menu, err := repository.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "menu", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := repository.SaveRole(ctx, appKey, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}, MenuIDs: []uuid.UUID{menu.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// The write path receives the legacy alias and must persist the empty default.
	if err = repository.ReplaceSubjectRoles(ctx, appKey, "user-1", "global", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}

	decision, err := service.Authorize(ctx, appKey, map[string]any{"subject": "user-1", "service_resource": serviceKey, "method": "GET", "path": "/items/1"})
	if err != nil || !decision.Allowed {
		t.Fatalf("default-domain binding should allow: %+v %v", decision, err)
	}
	named, err := service.Authorize(ctx, appKey, map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": serviceKey, "method": "GET", "path": "/items/1"})
	if err != nil {
		t.Fatal(err)
	}
	if named.Allowed {
		t.Fatalf("default-domain binding leaked into a named domain: %+v", named)
	}
	for _, domain := range []string{"", "global"} {
		menus, err := service.SubjectMenus(ctx, appKey, "user-1", domain)
		if err != nil || len(menus) != 1 || menus[0].ID != menu.ID {
			t.Fatalf("default-domain menus for %q = %+v %v", domain, menus, err)
		}
	}
	if menus, err := service.SubjectMenus(ctx, appKey, "user-1", "tenant-a"); err != nil || len(menus) != 0 {
		t.Fatalf("default-domain menus leaked into a named domain: %+v %v", menus, err)
	}

	// Clearing the default domain also clears raw pre-migration aliases. A stale
	// "global" row left behind would keep authorizing after the caller removed
	// the grant, so the same request must be denied afterwards.
	if _, err := pool.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) SELECT id,$1,'global',$2 FROM applications WHERE app_key=$3`, "user-1", role.ID, appKey); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceSubjectRoles(ctx, appKey, "user-1", "", nil); err != nil {
		t.Fatal(err)
	}
	var remainingAliases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM subject_role_bindings WHERE application_id=(SELECT id FROM applications WHERE app_key=$1) AND subject=$2`, appKey, "user-1").Scan(&remainingAliases); err != nil {
		t.Fatal(err)
	}
	if remainingAliases != 0 {
		t.Fatalf("default-domain replace left %d alias rows, want 0", remainingAliases)
	}
	if decision, err := service.Authorize(ctx, appKey, map[string]any{"subject": "user-1", "service_resource": serviceKey, "method": "GET", "path": "/items/1"}); err != nil || decision.Allowed {
		t.Fatalf("cleared default-domain binding should deny: %+v %v", decision, err)
	}
}

// TestReplaceSubjectRolesStoresEmptyDefaultDomain proves the repository folds
// the retired "global" alias onto the empty default domain on write and on
// read, and that a bare insert uses the empty column default.
func TestReplaceSubjectRolesStoresEmptyDefaultDomain(t *testing.T) {
	pool := optionalDomainTestPool(t)
	ctx := context.Background()
	repository := NewRBACV1Repository(pool)
	suffix := uuid.NewString()
	appKey := "optional-domain-" + suffix
	subject := "user:" + suffix

	var appID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name) VALUES($1,$1) RETURNING id`, appKey).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, appID)
	})
	var roleID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO roles(application_id,code,name) VALUES($1,'reader','Reader') RETURNING id`, appID).Scan(&roleID); err != nil {
		t.Fatal(err)
	}

	if err := repository.ReplaceSubjectRoles(ctx, appKey, subject, "global", []uuid.UUID{roleID}); err != nil {
		t.Fatal(err)
	}
	var storedDomain string
	if err := pool.QueryRow(ctx, `SELECT domain FROM subject_role_bindings WHERE application_id=$1 AND subject=$2`, appID, subject).Scan(&storedDomain); err != nil {
		t.Fatal(err)
	}
	if storedDomain != "" {
		t.Fatalf("legacy global alias stored as %q, want the empty default", storedDomain)
	}

	// A legacy row written before the migration is folded on read too.
	legacySubject := "legacy:" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,$2,'global',$3)`, appID, legacySubject, roleID); err != nil {
		t.Fatal(err)
	}
	// A pre-migration duplicate ("global" alongside the empty grant) collapses
	// into a single binding on read.
	if _, err := pool.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,$2,'global',$3)`, appID, subject, roleID); err != nil {
		t.Fatal(err)
	}
	bindings, err := repository.ListSubjectRoleBindings(ctx, appKey)
	if err != nil {
		t.Fatal(err)
	}
	seen, duplicates := 0, 0
	for _, binding := range bindings {
		if binding.Subject == legacySubject {
			seen++
			if binding.Domain != "" {
				t.Fatalf("legacy binding read as domain %q, want empty", binding.Domain)
			}
		}
		if binding.Subject == subject {
			duplicates++
		}
	}
	if seen != 1 {
		t.Fatalf("legacy binding count = %d, want 1", seen)
	}
	if duplicates != 1 {
		t.Fatalf("collapsed binding count = %d, want 1", duplicates)
	}

	// A raw pre-migration binding (here a second " global " alias) is still an
	// active default-domain grant. Replacing the default domain with an empty
	// set must delete every alias, not just the canonical empty row, or the
	// retired grant would keep authorizing.
	if _, err := pool.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,$2,' global ',$3)`, appID, legacySubject, roleID); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceSubjectRoles(ctx, appKey, legacySubject, "", nil); err != nil {
		t.Fatal(err)
	}
	var remainingAliases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM subject_role_bindings WHERE subject=$1`, legacySubject).Scan(&remainingAliases); err != nil {
		t.Fatal(err)
	}
	if remainingAliases != 0 {
		t.Fatalf("default-domain replace left %d legacy alias rows, want 0", remainingAliases)
	}
	bindings, err = repository.ListSubjectRoleBindings(ctx, appKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.Subject == legacySubject {
			t.Fatalf("cleared legacy binding still listed: %+v", binding)
		}
	}

	// A bare insert with no domain column uses the database default.
	defaultSubject := "default:" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,role_id) VALUES($1,$2,$3)`, appID, defaultSubject, roleID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT domain FROM subject_role_bindings WHERE application_id=$1 AND subject=$2`, appID, defaultSubject).Scan(&storedDomain); err != nil {
		t.Fatal(err)
	}
	if storedDomain != "" {
		t.Fatalf("column default stored %q, want the empty default", storedDomain)
	}
}
