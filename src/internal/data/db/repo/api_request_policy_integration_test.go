package repo

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

// fullCurrentPolicyRepository deliberately exposes only the legacy
// RBACV1Repository contract. It lets the integration test compile the complete
// application policy even though the production repository also implements the
// Redis and request-scoped database interfaces.
type fullCurrentPolicyRepository struct{ repo *RBACV1Repository }

func (r fullCurrentPolicyRepository) ListEndpoints(ctx context.Context, key string) ([]biz.RBACEndpoint, error) {
	return r.repo.ListEndpoints(ctx, key)
}

func (r fullCurrentPolicyRepository) SaveEndpoint(ctx context.Context, key string, value biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	return r.repo.SaveEndpoint(ctx, key, value)
}

func (r fullCurrentPolicyRepository) DeleteEndpoint(ctx context.Context, key string, id uuid.UUID) error {
	return r.repo.DeleteEndpoint(ctx, key, id)
}

func (r fullCurrentPolicyRepository) ListRoles(ctx context.Context, key string) ([]biz.RBACRole, error) {
	return r.repo.ListRoles(ctx, key)
}

func (r fullCurrentPolicyRepository) SaveRole(ctx context.Context, key string, value biz.RBACRole) (biz.RBACRole, error) {
	return r.repo.SaveRole(ctx, key, value)
}

func (r fullCurrentPolicyRepository) DeleteRole(ctx context.Context, key string, id uuid.UUID) error {
	return r.repo.DeleteRole(ctx, key, id)
}

func (r fullCurrentPolicyRepository) ListSubjectRoleBindings(ctx context.Context, key string) ([]biz.SubjectRoleBinding, error) {
	return r.repo.ListSubjectRoleBindings(ctx, key)
}

func (r fullCurrentPolicyRepository) ReplaceSubjectRoles(ctx context.Context, key, subject, domain string, roleIDs []uuid.UUID) error {
	return r.repo.ReplaceSubjectRoles(ctx, key, subject, domain, roleIDs)
}

func (r fullCurrentPolicyRepository) GetPolicyMetadata(ctx context.Context, key string) (biz.CurrentPolicy, error) {
	return r.repo.GetPolicyMetadata(ctx, key)
}

func (r fullCurrentPolicyRepository) GetCurrentPolicy(ctx context.Context, policy biz.CurrentPolicy) (biz.CurrentPolicy, error) {
	return r.repo.GetCurrentPolicy(ctx, policy)
}

type apiRequestPolicyCatalog struct {
	resources []*biz.ServiceResource
}

func (c *apiRequestPolicyCatalog) List(context.Context) ([]*biz.ServiceResource, error) {
	items := make([]*biz.ServiceResource, 0, len(c.resources))
	for _, resource := range c.resources {
		copy := *resource
		items = append(items, &copy)
	}
	return items, nil
}

func (c *apiRequestPolicyCatalog) Get(_ context.Context, key string) (*biz.ServiceResource, error) {
	for _, resource := range c.resources {
		if resource.Key == key {
			copy := *resource
			return &copy, nil
		}
	}
	return nil, biz.ErrNotFound
}

func TestAPIRequestPolicyDatabaseModeMatchesFullCurrentPolicyIntegration(t *testing.T) {
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

	suffix := uuid.NewString()
	appKey := "api-snapshot-app-" + suffix
	otherAppKey := "api-snapshot-other-" + suffix
	service := "api-snapshot-orders-" + suffix
	otherService := "api-snapshot-inventory-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1),($2,$2)`, service, otherService); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=ANY($1::text[])`, []string{appKey, otherAppKey})
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=ANY($1::text[])`, []string{service, otherService})
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=ANY($1::text[])`, []string{service, otherService})
	})

	repository := NewRBACV1Repository(pool)
	app, err := repository.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: appKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := repository.SaveApplication(ctx, biz.Application{Key: otherAppKey, DisplayName: otherAppKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceApplicationServices(ctx, app.Key, []string{service, otherService}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceApplicationServices(ctx, otherApp.Key, []string{service}); err != nil {
		t.Fatal(err)
	}

	wide, err := repository.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	export, err := repository.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items/export", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := repository.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/admin", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := repository.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/disabled", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	otherAppEndpoint, err := repository.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/other-app", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherServiceEndpoint, err := repository.SaveEndpoint(ctx, otherService, biz.RBACEndpoint{Method: "GET", PathTemplate: "/inventory", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	reader, err := repository.SaveRole(ctx, app.Key, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{wide.ID, otherServiceEndpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := repository.SaveRole(ctx, app.Key, biz.RBACRole{Code: "writer", Name: "Writer", Enabled: true, EndpointIDs: []uuid.UUID{export.ID}})
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := repository.SaveRole(ctx, app.Key, biz.RBACRole{Code: "auditor", Name: "Auditor", Enabled: false, EndpointIDs: []uuid.UUID{admin.ID}})
	if err != nil {
		t.Fatal(err)
	}
	otherRole, err := repository.SaveRole(ctx, otherApp.Key, biz.RBACRole{Code: "other-reader", Name: "Other reader", Enabled: true, EndpointIDs: []uuid.UUID{otherAppEndpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}

	if err := repository.ReplaceSubjectRoles(ctx, app.Key, "user-a", "tenant-a", []uuid.UUID{reader.ID, writer.ID, auditor.ID}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceSubjectRoles(ctx, app.Key, "user-b", "tenant-a", []uuid.UUID{reader.ID}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceSubjectRoles(ctx, otherApp.Key, "user-a", "tenant-a", []uuid.UUID{otherRole.ID}); err != nil {
		t.Fatal(err)
	}

	full := biz.NewRBACV1Service(fullCurrentPolicyRepository{repo: repository})
	database := biz.NewRBACV1Service(repository).WithDatabaseAuthorization()
	assertFastPathMatchesFull := func(appKey, subject, domain, service, method, path string) {
		t.Helper()
		input := map[string]any{
			"subject":          subject,
			"domain":           domain,
			"service_resource": service,
			"method":           method,
			"path":             path,
		}
		got, gotErr := database.Authorize(ctx, appKey, input)
		want, wantErr := full.Authorize(ctx, appKey, input)
		if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
			t.Fatalf("authorization error mismatch for %s %s %s %s %s: fast=%v full=%v", appKey, subject, domain, method, path, gotErr, wantErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("authorization decision mismatch for %s %s %s %s %s: fast=%+v full=%+v", appKey, subject, domain, method, path, got, want)
		}
	}

	snapshot, err := repository.GetAPIRequestPolicy(ctx, app.Key, " user-b ", " tenant-a ", service)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ApplicationID != app.ID || snapshot.Token == "" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	assertSnapshotListLength(t, snapshot.Data, "endpoints", 5)
	assertSnapshotListLength(t, snapshot.Data, "roles", 1)
	assertSnapshotListLength(t, snapshot.Data, "subject_roles", 1)
	assertSnapshotListLength(t, snapshot.Data, "service_resources", 1)
	if _, exists := snapshot.Data["menus"]; exists {
		t.Fatalf("snapshot retained menu data: %+v", snapshot.Data)
	}
	for _, endpointID := range []uuid.UUID{wide.ID, export.ID, admin.ID, disabled.ID, otherAppEndpoint.ID} {
		if !containsSnapshotID(snapshot.Data["endpoints"], endpointID) {
			t.Fatalf("snapshot omitted requested-service endpoint %s from the complete catalog", endpointID)
		}
	}
	if containsSnapshotID(snapshot.Data["roles"], otherRole.ID) || containsSnapshotID(snapshot.Data["endpoints"], otherServiceEndpoint.ID) {
		t.Fatalf("snapshot leaked another application's role or another service's endpoint: %+v", snapshot.Data)
	}

	tests := []struct {
		name, app, subject, domain, service, method, path, reason string
		allowed                                                   bool
	}{
		{"wide grant allows", app.Key, "user-a", "tenant-a", service, "GET", "/items/123", "allowed", true},
		{"specific grant allows", app.Key, "user-a", "tenant-a", service, "GET", "/items/export", "allowed", true},
		{"wide grant cannot authorize ungranted specific route", app.Key, "user-b", "tenant-a", service, "GET", "/items/export", "endpoint_not_granted", false},
		{"route precedence keeps wide grant", app.Key, "user-b", "tenant-a", service, "GET", "/items/456", "allowed", true},
		{"other domain is isolated", app.Key, "user-a", "tenant-b", service, "GET", "/items/123", "endpoint_not_granted", false},
		{"other subject is isolated", app.Key, "nobody", "tenant-a", service, "GET", "/items/123", "endpoint_not_granted", false},
		{"disabled endpoint wins before grant check", app.Key, "user-a", "tenant-a", service, "GET", "/disabled", "endpoint_disabled", false},
		{"other application grant allows its own request", otherApp.Key, "user-a", "tenant-a", service, "GET", "/other-app", "allowed", true},
		{"other application grant does not leak", app.Key, "user-a", "tenant-a", service, "GET", "/other-app", "endpoint_not_granted", false},
		{"unknown service has no routes", app.Key, "user-a", "tenant-a", "missing-" + suffix, "GET", "/items/123", "endpoint_not_found", false},
		{"unknown method has no routes", app.Key, "user-a", "tenant-a", service, "POST", "/items/123", "endpoint_not_found", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertFastPathMatchesFull(test.app, test.subject, test.domain, test.service, test.method, test.path)
		})
	}

	for _, test := range tests {
		if test.app != app.Key || test.subject != "user-a" || test.domain != "tenant-a" {
			continue
		}
		decision, err := database.Authorize(ctx, test.app, map[string]any{
			"subject": test.subject, "domain": test.domain, "service_resource": test.service, "method": test.method, "path": test.path,
		})
		if test.name == "other application grant does not leak" {
			if err != nil || decision.Allowed || decision.Reason != test.reason {
				t.Fatalf("expected decision = %+v, %v", decision, err)
			}
		}
	}

	assertFastPathMatchesFull(app.Key, "user-a", "tenant-a", service, "GET", "/admin")
	auditor.Enabled = true
	if _, err := repository.SaveRole(ctx, app.Key, auditor); err != nil {
		t.Fatal(err)
	}
	assertFastPathMatchesFull(app.Key, "user-a", "tenant-a", service, "GET", "/admin")
	auditor.Enabled = false
	if _, err := repository.SaveRole(ctx, app.Key, auditor); err != nil {
		t.Fatal(err)
	}
	assertFastPathMatchesFull(app.Key, "user-a", "tenant-a", service, "GET", "/admin")

	if err := repository.ReplaceSubjectRoles(ctx, app.Key, "user-a", "tenant-a", nil); err != nil {
		t.Fatal(err)
	}
	decision, err := database.Authorize(ctx, app.Key, map[string]any{
		"subject": "user-a", "domain": "tenant-a", "service_resource": service, "method": "GET", "path": "/items/123",
	})
	if err != nil || decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("revocation was not visible on the next request: %+v, %v", decision, err)
	}

	_, databaseErr := database.Authorize(ctx, "missing-"+suffix, map[string]any{
		"subject": "user-a", "service_resource": service, "method": "GET", "path": "/items/123",
	})
	_, fullErr := full.Authorize(ctx, "missing-"+suffix, map[string]any{
		"subject": "user-a", "service_resource": service, "method": "GET", "path": "/items/123",
	})
	if !errors.Is(databaseErr, biz.ErrNotFound) || !errors.Is(fullErr, biz.ErrNotFound) {
		t.Fatalf("unknown application = fast:%v full:%v", databaseErr, fullErr)
	}

	otherApp.Enabled = false
	if _, err := repository.SaveApplication(ctx, otherApp); err != nil {
		t.Fatal(err)
	}
	if _, fastErr := database.Authorize(ctx, otherApp.Key, map[string]any{
		"subject": "user-a", "domain": "tenant-a", "service_resource": service, "method": "GET", "path": "/other-app",
	}); !errors.Is(fastErr, biz.ErrNotFound) {
		t.Fatalf("disabled application fast path = %v", fastErr)
	}
	if _, disabledErr := full.Authorize(ctx, otherApp.Key, map[string]any{
		"subject": "user-a", "domain": "tenant-a", "service_resource": service, "method": "GET", "path": "/other-app",
	}); !errors.Is(disabledErr, biz.ErrNotFound) {
		t.Fatalf("disabled application full path = %v", disabledErr)
	}

	repository.catalog = &apiRequestPolicyCatalog{resources: []*biz.ServiceResource{
		{Key: service, DisplayName: service, IsActive: false},
		{Key: otherService, DisplayName: otherService, IsActive: true},
	}}
	assertFastPathMatchesFull(app.Key, "user-b", "tenant-a", service, "GET", "/items/123")
}

func assertSnapshotListLength(t *testing.T, data map[string]any, key string, want int) {
	t.Helper()
	items, ok := data[key].([]any)
	if !ok || len(items) != want {
		t.Fatalf("snapshot %s = %#v, want %d items", key, data[key], want)
	}
}

func containsSnapshotID(value any, want uuid.UUID) bool {
	items, _ := value.([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if id, _ := entry["id"].(string); id == want.String() {
			return true
		}
	}
	return false
}
