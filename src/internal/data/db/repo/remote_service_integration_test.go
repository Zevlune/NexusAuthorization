package repo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

type changingServiceCatalog struct {
	resource *biz.ServiceResource
	err      error
}

func (c *changingServiceCatalog) List(context.Context) ([]*biz.ServiceResource, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.resource == nil {
		return []*biz.ServiceResource{}, nil
	}
	copy := *c.resource
	return []*biz.ServiceResource{&copy}, nil
}
func (c *changingServiceCatalog) Get(ctx context.Context, key string) (*biz.ServiceResource, error) {
	values, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.Key == key {
			return value, nil
		}
	}
	return nil, biz.ErrNotFound
}

// Exercises the full control plane with no local service catalog entry, then
// verifies that a conflicting local entry cannot shadow or replace remote data.
func TestRemoteApplicationPermissionIntegration(t *testing.T) {
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
	cache, err := rediscache.New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization-test-" + uuid.NewString(), CacheTTL: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	suffix := uuid.NewString()
	appKey, key := "remote-console-"+suffix, "remote-orders-"+suffix
	foreignKey := "unlinked-orders-" + suffix
	catalog := &changingServiceCatalog{resource: &biz.ServiceResource{ID: "external-resource-id", Key: key, DisplayName: "Remote orders", IsActive: true, Source: "nexusauth"}}
	service, err := biz.NewServiceResourceCatalog(NewServiceResourceRepository(pool), catalog, "nexusauth")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRBACV1Repository(pool, service).WithPolicyCache(cache)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, key)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, foreignKey)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, key)
	})
	if _, err := r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Remote Console", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceApplicationServices(ctx, appKey, []string{key}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE applications SET enabled=FALSE WHERE app_key=$1`, appKey); err != nil {
		t.Fatal(err)
	}
	if err := r.RequireLinkedApplicationService(ctx, appKey, key); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("disabled application accepted by link check: %v", err)
	}
	if _, _, err := r.ListApplicationEndpoints(ctx, appKey, "", 1, 100); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("disabled application listed endpoints: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE applications SET enabled=TRUE WHERE app_key=$1`, appKey); err != nil {
		t.Fatal(err)
	}
	endpoint, err := r.SaveEndpoint(ctx, key, biz.RBACEndpoint{Method: "GET", PathTemplate: "/items", Enabled: true})
	if err != nil || endpoint.ServiceResource != key {
		t.Fatalf("remote endpoint: %+v %v", endpoint, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template) VALUES($1,$2,'GET','/unlinked')`, foreignKey, "unlinked:"+suffix); err != nil {
		t.Fatal(err)
	}
	role, err := r.SaveRole(ctx, appKey, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceSubjectRoles(ctx, appKey, "user", "global", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	svc := biz.NewRBACV1Service(r)
	// The startup pass records the catalog that later passes compare against.
	if _, err := r.ReconcileSubjectAPIs(ctx, true); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"service_resource": key, "subject": "user", "domain": "global", "method": "GET", "path": "/items"}
	decision, err := svc.Authorize(ctx, appKey, input)
	if err != nil || !decision.Allowed {
		t.Fatalf("remote grant failed: %+v %v", decision, err)
	}
	endpoints, total, err := r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 1 || len(endpoints) != 1 || endpoints[0].ServiceResource != key {
		t.Fatalf("remote catalog: %+v %d %v", endpoints, total, err)
	}
	if err := svc.RequireLinkedApplicationService(ctx, appKey, key); err != nil {
		t.Fatalf("linked remote service rejected: %v", err)
	}
	if err := svc.RequireLinkedApplicationService(ctx, appKey, foreignKey); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("unlinked endpoint scope accepted: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM service_resources WHERE resource_key=$1`, key).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remote metadata persisted: %d %v", count, err)
	}
	// A local record with the same key must not be read in remote mode.
	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name,enabled) VALUES($1,'Local stale name',FALSE)`, key); err != nil {
		t.Fatal(err)
	}
	catalog.resource.DisplayName = "Remote renamed"
	resources, err := r.ListApplicationServices(ctx, appKey)
	if err != nil || len(resources) != 1 || resources[0].DisplayName != "Remote renamed" || resources[0].ID != "external-resource-id" {
		t.Fatalf("not using current remote metadata: %+v %v", resources, err)
	}
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM service_resources WHERE resource_key=$1`, key).Scan(&displayName); err != nil || displayName != "Local stale name" {
		t.Fatalf("remote read changed local record: %q %v", displayName, err)
	}
	catalog.err = errors.New("remote unavailable")
	if _, err := r.ListApplicationServices(ctx, appKey); !errors.Is(err, catalog.err) {
		t.Fatalf("remote error hidden: %v", err)
	}
	endpoints, total, err = r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 1 || len(endpoints) != 1 || endpoints[0].ID != endpoint.ID {
		t.Fatalf("endpoint list depended on remote catalog: %+v %d %v", endpoints, total, err)
	}
	listed, err := r.ListEndpoints(ctx, key)
	if err != nil || len(listed) != 1 || listed[0].ID != endpoint.ID {
		t.Fatalf("service endpoint list depended on remote catalog: %+v %v", listed, err)
	}
	if err := svc.RequireLinkedApplicationService(ctx, appKey, key); err != nil {
		t.Fatalf("linked service blocked by remote outage: %v", err)
	}
	if err := svc.RequireApplicationService(ctx, appKey, key); !errors.Is(err, catalog.err) {
		t.Fatalf("write path ignored remote outage: %v", err)
	}
	if _, err := r.GetPolicyMetadata(ctx, appKey); !errors.Is(err, catalog.err) {
		t.Fatalf("runtime fallback: %v", err)
	}
	catalog.err = nil
	catalog.resource.IsActive = false
	endpoints, total, err = r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 1 || len(endpoints) != 1 || endpoints[0].ID != endpoint.ID {
		t.Fatalf("disabled remote endpoint hidden: %+v %d %v", endpoints, total, err)
	}
	listed, err = r.ListEndpoints(ctx, key)
	if err != nil || len(listed) != 1 || listed[0].ID != endpoint.ID {
		t.Fatalf("disabled service endpoint list: %+v %v", listed, err)
	}
	if err := svc.RequireLinkedApplicationService(ctx, appKey, key); err != nil {
		t.Fatalf("disabled linked service rejected: %v", err)
	}
	if err := svc.RequireApplicationService(ctx, appKey, key); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("write path accepted disabled service: %v", err)
	}
	if _, err := r.SaveEndpoint(ctx, key, biz.RBACEndpoint{Method: "POST", PathTemplate: "/items", Enabled: true}); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("disabled remote accepted: %v", err)
	}
	// The remote catalog changed without any local save: the reconciler
	// detects it and republishes the linked application.
	if _, err := r.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	decision, err = svc.Authorize(ctx, appKey, input)
	if err != nil || decision.Allowed {
		t.Fatalf("disabled remote authorized: %+v %v", decision, err)
	}
	catalog.resource = nil
	resources, err = r.ListApplicationServices(ctx, appKey)
	if err != nil || len(resources) != 1 || resources[0].IsActive || resources[0].DisplayName != key {
		t.Fatalf("missing remote fallback: %+v %v", resources, err)
	}
	endpoints, total, err = r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 1 || len(endpoints) != 1 || endpoints[0].ID != endpoint.ID {
		t.Fatalf("missing remote endpoint hidden: %+v %d %v", endpoints, total, err)
	}
	if err := r.ReplaceApplicationServices(ctx, appKey, []string{key}); err != nil {
		t.Fatalf("cannot retain existing unavailable link: %v", err)
	}
	if err := r.ReplaceApplicationServices(ctx, appKey, []string{}); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unlinked granted service: %v", err)
	}
	catalog.err = errors.New("remote unavailable during endpoint cleanup")
	if err := svc.RequireLinkedApplicationService(ctx, appKey, key); err != nil {
		t.Fatalf("linked service blocked during cleanup: %v", err)
	}
	if err := svc.DeleteEndpoint(ctx, key, endpoint.ID); !errors.Is(err, biz.ErrPublishPending) {
		t.Fatalf("endpoint cleanup publish state: %v", err)
	}
	endpoints, total, err = r.ListApplicationEndpoints(ctx, appKey, "", 1, 100)
	if err != nil || total != 0 || len(endpoints) != 0 {
		t.Fatalf("deleted endpoint remained listed: %+v %d %v", endpoints, total, err)
	}
	listed, err = r.ListEndpoints(ctx, key)
	if err != nil || len(listed) != 0 {
		t.Fatalf("deleted service endpoint remained listed: %+v %v", listed, err)
	}
	catalog.err = nil
	if err := r.DeleteRole(ctx, appKey, role.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceApplicationServices(ctx, appKey, []string{}); err != nil {
		t.Fatalf("cannot unlink missing remote: %v", err)
	}
}
