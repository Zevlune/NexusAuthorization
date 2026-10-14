package repo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

// TestAuthorizationAPIEndpointRepositoryIntegration covers the still
// registered /v1/authorization/api-endpoints compatibility routes by driving
// their repository against authorization_api_endpoints. Like the other
// integration tests it reuses NEXUSAUTHORIZATION_TEST_DATABASE_URL and a
// disposable database initialized only with migrations/001_init.sql, and it
// skips when that variable is unset.
func TestAuthorizationAPIEndpointRepositoryIntegration(t *testing.T) {
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

	// authorization_api_endpoints is created by migrations/001_init.sql; a
	// missing table means the disposable database predates the schema under
	// test.
	var table *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('authorization_api_endpoints')::text`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table == nil {
		t.Fatal("authorization_api_endpoints is missing; initialize the test database with migrations/001_init.sql")
	}

	repository := NewAPIEndpointRepository(pool)
	serviceResource := "compat-endpoints-" + uuid.NewString()
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM authorization_api_endpoints WHERE service_resource=$1`, serviceResource); err != nil {
			t.Errorf("cleanup authorization_api_endpoints %s: %v", serviceResource, err)
		}
	})

	creator := biz.WithAuditActor(ctx, biz.AuditActor{ID: "creator-id", Name: "Creator"})
	editor := biz.WithAuditActor(ctx, biz.AuditActor{ID: "editor-id", Name: "Editor"})

	created, err := repository.Create(creator, &biz.APIEndpoint{
		ServiceResource: serviceResource,
		Controller:      "order",
		Method:          "GET",
		PathTemplate:    "/orders/{id}",
		Summary:         "Get order",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("create did not assign an id")
	}
	if created.ServiceResource != serviceResource || created.Controller != "order" || created.Method != "GET" || created.PathTemplate != "/orders/{id}" || created.Summary != "Get order" || !created.Enabled || created.IsDeleted {
		t.Fatalf("created endpoint fields = %#v", created)
	}
	if created.CreatedByID != "creator-id" || created.CreatedByName != "Creator" || created.UpdatedByID != "creator-id" || created.UpdatedByName != "Creator" {
		t.Fatalf("created audit fields = %#v", created.BaseFields)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps missing = %#v", created.BaseFields)
	}

	fetched, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	if fetched.ID != created.ID || fetched.PathTemplate != created.PathTemplate || fetched.CreatedByName != "Creator" {
		t.Fatalf("fetched endpoint = %#v", fetched)
	}

	second, err := repository.Create(creator, &biz.APIEndpoint{
		ServiceResource: serviceResource,
		Controller:      "order",
		Method:          "DELETE",
		PathTemplate:    "/orders/{id}",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create second endpoint: %v", err)
	}

	// List is scoped to the service resource and ordered by controller, method,
	// path_template and id.
	listed, err := repository.ListByServiceResource(ctx, serviceResource)
	if err != nil {
		t.Fatalf("list endpoints: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != created.ID {
		t.Fatalf("listed endpoints = %#v", listed)
	}
	if other, err := repository.ListByServiceResource(ctx, serviceResource+"-absent"); err != nil || len(other) != 0 {
		t.Fatalf("foreign service resource leaked %#v %v", other, err)
	}

	// An active (service_resource, method, path_template) must stay unique.
	if _, err := repository.Create(creator, &biz.APIEndpoint{
		ServiceResource: serviceResource,
		Controller:      "order",
		Method:          "GET",
		PathTemplate:    "/orders/{id}",
		Enabled:         true,
	}); !errors.Is(err, biz.ErrAlreadyExists) {
		t.Fatalf("duplicate active route accepted: %v", err)
	}

	// Update only changes mutable fields and records the editor while keeping
	// the service resource and creation audit.
	updated, err := repository.Update(editor, &biz.APIEndpoint{
		ID:           created.ID,
		Controller:   "orders",
		Method:       "PATCH",
		PathTemplate: "/orders/{id}",
		Summary:      "Patch order",
		Enabled:      false,
	})
	if err != nil {
		t.Fatalf("update endpoint: %v", err)
	}
	if updated.Controller != "orders" || updated.Method != "PATCH" || updated.Summary != "Patch order" || updated.Enabled || updated.ServiceResource != serviceResource {
		t.Fatalf("updated endpoint fields = %#v", updated)
	}
	if updated.CreatedByID != "creator-id" || updated.CreatedByName != "Creator" || updated.UpdatedByID != "editor-id" || updated.UpdatedByName != "Editor" {
		t.Fatalf("updated audit fields = %#v", updated.BaseFields)
	}

	// Soft delete hides the row, disables it and records the editor.
	if err := repository.SoftDelete(editor, created.ID); err != nil {
		t.Fatalf("soft delete endpoint: %v", err)
	}
	if _, err := repository.Get(ctx, created.ID); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("soft-deleted endpoint still readable: %v", err)
	}
	if err := repository.SoftDelete(editor, created.ID); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("second soft delete = %v", err)
	}
	remaining, err := repository.ListByServiceResource(ctx, serviceResource)
	if err != nil || len(remaining) != 1 || remaining[0].ID != second.ID {
		t.Fatalf("soft-deleted endpoint still listed %#v %v", remaining, err)
	}
	var deleted, enabled bool
	var deletedBy string
	if err := pool.QueryRow(ctx, `SELECT is_deleted, enabled, updated_by_id FROM authorization_api_endpoints WHERE id=$1`, created.ID).Scan(&deleted, &enabled, &deletedBy); err != nil {
		t.Fatal(err)
	}
	if !deleted || enabled || deletedBy != "editor-id" {
		t.Fatalf("soft delete state is_deleted=%v enabled=%v updated_by=%q", deleted, enabled, deletedBy)
	}

	// The partial unique index only guards active rows, so the same route may be
	// recreated after the previous row was soft-deleted.
	recreated, err := repository.Create(creator, &biz.APIEndpoint{
		ServiceResource: serviceResource,
		Controller:      "order",
		Method:          "PATCH",
		PathTemplate:    "/orders/{id}",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("recreate after soft delete: %v", err)
	}
	if recreated.ID == created.ID {
		t.Fatal("recreated endpoint reused the soft-deleted id")
	}
	if _, err := repository.Create(creator, &biz.APIEndpoint{
		ServiceResource: serviceResource,
		Controller:      "order",
		Method:          "PATCH",
		PathTemplate:    "/orders/{id}",
		Enabled:         true,
	}); !errors.Is(err, biz.ErrAlreadyExists) {
		t.Fatalf("duplicate recreated route accepted: %v", err)
	}
}
