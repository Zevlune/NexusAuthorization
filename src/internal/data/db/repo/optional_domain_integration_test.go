package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	pool, err := pgxpool.New(context.Background(), dsn)
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

// TestOptionalDomainMigrationIntegration applies migrations/003_optional_domain.sql
// against a scratch schema built from migrations/001_init.sql. It covers the
// legacy-to-empty conversion, the removed domain-only constraint, duplicate
// collapsing, policy token invalidation, and idempotent re-runs.
func TestOptionalDomainMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// A single session keeps the schema-scoped search_path stable across
	// statements.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })

	schema := "optional_domain_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatal(err)
	}

	root := migrationsRoot(t)
	execSQLFile(t, ctx, conn, filepath.Join(root, "migrations", "001_init.sql"))

	appKey := "migration-" + uuid.NewString()
	var appID, roleA, roleB uuid.UUID
	if err := conn.QueryRow(ctx, `INSERT INTO applications(app_key,display_name) VALUES($1,$1) RETURNING id`, appKey).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO roles(application_id,code,name) VALUES($1,'role-a','Role A') RETURNING id`, appID).Scan(&roleA); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO roles(application_id,code,name) VALUES($1,'role-b','Role B') RETURNING id`, appID).Scan(&roleB); err != nil {
		t.Fatal(err)
	}

	// Legacy state: the domain column defaults to 'global' and rejects blanks.
	// Fresh schemas from 001_init.sql already ship the new empty default, so the
	// legacy shape is reconstructed explicitly to represent an old install.
	for _, statement := range []string{
		`ALTER TABLE subject_role_bindings ALTER COLUMN domain SET DEFAULT 'global'`,
		`ALTER TABLE subject_role_bindings DROP CONSTRAINT IF EXISTS subject_role_bindings_domain_check`,
		`ALTER TABLE subject_role_bindings ADD CONSTRAINT subject_role_bindings_domain_check CHECK (btrim(domain) <> '')`,
		// The same non-blank predicate under a different name, plus a distinct
		// domain constraint that must survive the migration.
		`ALTER TABLE subject_role_bindings ADD CONSTRAINT domain_notblank_legacy_renamed CHECK (btrim(domain) <> '')`,
		`ALTER TABLE subject_role_bindings ADD CONSTRAINT domain_acceptance_length CHECK (length(domain) <= 128)`,
	} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			t.Fatalf("set up legacy schema (%s): %v", statement, err)
		}
	}
	if got := scalarString(t, ctx, conn, `SELECT column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='subject_role_bindings' AND column_name='domain'`); !strings.Contains(got, "global") {
		t.Fatalf("pre-migration domain default = %q, want a 'global' default", got)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,role_id) VALUES($1,'legacy-user',$2)`, appID, roleA); err != nil {
		t.Fatal(err)
	}
	// A second legacy alias for the same grant identity. No canonical empty row
	// exists yet, so a naive UPDATE would collide on the unique key.
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,'legacy-user',' global ',$2)`, appID, roleA); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,role_id) VALUES($1,'legacy-user',$2)`, appID, roleB); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,'legacy-user',' global ',$2)`, appID, roleB); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,'legacy-user','tenant-a',$2)`, appID, roleA); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,'legacy-user','',$2)`, appID, roleA); err == nil {
		t.Fatal("legacy schema accepted an empty domain; the precondition is wrong")
	}

	// First run converts the legacy schema and data.
	beforeFirstRun := scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID)
	execSQLFile(t, ctx, conn, filepath.Join(root, "migrations", "003_optional_domain.sql"))
	if afterFirstRun := scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID); afterFirstRun == beforeFirstRun {
		t.Fatal("policy token was not invalidated by the first normalization run")
	}

	if got := scalarString(t, ctx, conn, `SELECT column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='subject_role_bindings' AND column_name='domain'`); strings.Contains(strings.ToLower(got), "global") {
		t.Fatalf("post-migration domain default = %q, want empty", got)
	}
	// Both the known-name and the renamed copy of the legacy non-blank check are
	// gone, while the unrelated length bound stays.
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM pg_constraint con WHERE con.conrelid=to_regclass('subject_role_bindings') AND con.contype='c' AND con.conname IN ('subject_role_bindings_domain_check','domain_notblank_legacy_renamed')`); count != 0 {
		t.Fatalf("legacy non-blank domain checks remaining = %d, want 0", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM pg_constraint con WHERE con.conrelid=to_regclass('subject_role_bindings') AND con.contype='c' AND con.conname='domain_acceptance_length'`); count != 1 {
		t.Fatalf("unrelated domain length check = %d, want 1 (kept)", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain=''`); count != 2 {
		t.Fatalf("converted default bindings = %d, want 2 (role A and role B grants preserved)", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain='global'`); count != 0 {
		t.Fatalf("legacy global bindings remaining = %d, want 0", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain=' global '`); count != 0 {
		t.Fatalf("legacy whitespace alias bindings remaining = %d, want 0", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain='tenant-a'`); count != 1 {
		t.Fatalf("named domain bindings = %d, want the tenant-a grant untouched", count)
	}

	// Second run must collapse a "global" row that duplicates an existing empty
	// grant while keeping the role grant, and must invalidate the cached policy.
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) VALUES($1,'legacy-user','global',$2)`, appID, roleB); err != nil {
		t.Fatal(err)
	}
	// A bare insert proves the new empty default.
	if _, err := conn.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,role_id) VALUES($1,'fresh-user',$2)`, appID, roleA); err != nil {
		t.Fatal(err)
	}
	if got := scalarString(t, ctx, conn, `SELECT domain FROM subject_role_bindings WHERE subject='fresh-user'`); got != "" {
		t.Fatalf("bare insert stored domain %q, want empty", got)
	}
	before := scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID)
	execSQLFile(t, ctx, conn, filepath.Join(root, "migrations", "003_optional_domain.sql"))
	if after := scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID); after == before {
		t.Fatal("policy token was not invalidated after normalization")
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain=''`); count != 2 {
		t.Fatalf("default bindings after dedupe = %d, want 2", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM subject_role_bindings WHERE subject='legacy-user' AND domain='' AND role_id=$1`, roleB); count != 1 {
		t.Fatalf("duplicated role B grant count = %d, want 1", count)
	}
	if count := scalarInt(t, ctx, conn, `SELECT count(*) FROM pg_constraint con WHERE con.conrelid=to_regclass('subject_role_bindings') AND con.contype='c' AND con.conname='domain_acceptance_length'`); count != 1 {
		t.Fatalf("unrelated domain length check after re-run = %d, want 1 (kept)", count)
	}

	// Third run is a no-op: no error and no token churn.
	before = scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID)
	execSQLFile(t, ctx, conn, filepath.Join(root, "migrations", "003_optional_domain.sql"))
	if after := scalarString(t, ctx, conn, `SELECT policy_token::text FROM applications WHERE id=$1`, appID); after != before {
		t.Fatal("idempotent re-run still invalidated the policy token")
	}
}

func migrationsRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "001_init.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("repository root with migrations/001_init.sql not found from %s", dir)
	return ""
}

func execSQLFile(t *testing.T, ctx context.Context, conn *pgx.Conn, path string) {
	t.Helper()
	sql, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
		t.Fatalf("exec %s: %v", filepath.Base(path), err)
	}
}

func scalarString(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) string {
	t.Helper()
	var value *string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		t.Fatalf("scalar %s: %v", sql, err)
	}
	if value == nil {
		return ""
	}
	return *value
}

func scalarInt(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) int {
	t.Helper()
	var value int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		t.Fatalf("scalar %s: %v", sql, err)
	}
	return value
}
