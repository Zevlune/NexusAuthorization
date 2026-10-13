package repo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

// Requires a disposable database initialized with migrations/001_init.sql,
// migrations/002_application_url.sql, and migrations/003_api_tokens.sql.
func TestAPITokenRepositoryIntegration(t *testing.T) {
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

	suffix := uuid.NewString()
	appKey, serviceKey := "token-app-"+suffix, "token-service-"+suffix
	var appID, endpointID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,enabled) VALUES($1,$2,TRUE) RETURNING id`, appKey, appKey).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$2)`, serviceKey, serviceKey); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO application_service_resources(application_id,service_resource_key) VALUES($1,$2)`, appID, serviceKey); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,summary,enabled) VALUES($1,$2,'GET','/items/{id}','Items',TRUE) RETURNING id`, serviceKey, "items-"+suffix).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	var disabledEndpointID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,summary,enabled) VALUES($1,$2,'POST','/items','Create item',FALSE) RETURNING id`, serviceKey, "create-"+suffix).Scan(&disabledEndpointID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, appID)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE id=ANY($1::uuid[])`, []uuid.UUID{endpointID, disabledEndpointID})
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, serviceKey)
	})

	r := NewAPITokenRepository(pool)
	permission := biz.APITokenPermission{EndpointID: endpointID, ServiceResource: serviceKey, Action: "read", Method: "GET", PathTemplate: "/items/{id}", Summary: "Items"}
	newToken := func(name string) biz.APIToken {
		now := time.Now().UTC()
		return biz.APIToken{ID: uuid.New(), AppKey: appKey, Name: name, TokenPrefix: "nxa_" + uuid.NewString()[:8], TokenHash: biz.APITokenHash(name + "-secret"), OwnerType: "service", OwnerID: "worker", Status: "active", CreatedBy: "test", CreatedAt: now, UpdatedAt: now, Permissions: []biz.APITokenPermission{permission}}
	}

	created, err := r.Create(ctx, newToken("primary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Permissions) != 1 || created.Permissions[0].Summary != "Items" {
		t.Fatalf("created grant snapshot: %+v", created.Permissions)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || contains(string(encoded), created.TokenHash) {
		t.Fatal("token hash leaked into JSON")
	}
	loaded, err := r.Find(ctx, created.ID)
	if err != nil || len(loaded.Permissions) != 1 {
		t.Fatalf("find token: %+v %v", loaded, err)
	}
	activePage, err := r.List(ctx, biz.APITokenFilter{AppKey: appKey, Status: "active", Page: 1, PageSize: 50})
	if err != nil || len(activePage.Items) != 1 || activePage.Items[0].ID != created.ID {
		t.Fatalf("active token list: %+v %v", activePage, err)
	}
	past := newToken("expired")
	expiredAt := time.Now().Add(-time.Minute)
	past.ExpiresAt = &expiredAt
	if _, err = r.Create(ctx, past); err != nil {
		t.Fatal(err)
	}
	disabled := newToken("disabled")
	if _, err = r.Create(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SetStatus(ctx, appKey, disabled.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	activePage, err = r.List(ctx, biz.APITokenFilter{AppKey: appKey, Status: "active", Page: 1, PageSize: 50})
	if err != nil || len(activePage.Items) != 1 || activePage.Items[0].ID != created.ID {
		t.Fatalf("expired token leaked into active list: %+v %v", activePage, err)
	}
	disabledPage, err := r.List(ctx, biz.APITokenFilter{AppKey: appKey, Status: "disabled", Page: 1, PageSize: 50})
	if err != nil || len(disabledPage.Items) != 1 || disabledPage.Items[0].ID != disabled.ID {
		t.Fatalf("disabled token list: %+v %v", disabledPage, err)
	}
	expiredPage, err := r.List(ctx, biz.APITokenFilter{AppKey: appKey, Status: "expired", Page: 1, PageSize: 50})
	if err != nil || len(expiredPage.Items) != 1 || expiredPage.Items[0].ID != past.ID {
		t.Fatalf("expired token list: %+v %v", expiredPage, err)
	}
	if _, err = r.SetStatus(ctx, appKey, past.ID, "revoked"); err != nil {
		t.Fatal(err)
	}
	revokedPage, err := r.List(ctx, biz.APITokenFilter{AppKey: appKey, Status: "revoked", Page: 1, PageSize: 50})
	if err != nil || len(revokedPage.Items) != 1 || revokedPage.Items[0].ID != past.ID {
		t.Fatalf("revoked expired token missing from revoked list: %+v %v", revokedPage, err)
	}

	invalid := newToken("invalid")
	invalid.ID = uuid.New()
	invalid.Permissions = []biz.APITokenPermission{{EndpointID: disabledEndpointID, ServiceResource: serviceKey, Action: "write", Method: "POST", PathTemplate: "/items", Summary: "Create item"}}
	if _, err = r.Create(ctx, invalid); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("disabled grant error: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE id=$1`, invalid.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create was persisted: %d %v", count, err)
	}

	endpoints, err := r.Endpoints(ctx, appKey, serviceKey)
	if err != nil || len(endpoints) != 2 {
		t.Fatalf("local endpoint directory: %+v %v", endpoints, err)
	}
	if endpoints[0].Enabled == endpoints[1].Enabled {
		t.Fatalf("disabled endpoint omitted or enabled incorrectly: %+v", endpoints)
	}
	if _, err = pool.Exec(ctx, `UPDATE applications SET enabled=FALSE WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Endpoints(ctx, appKey, serviceKey); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("disabled application catalog remained available: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE applications SET enabled=TRUE WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}

	updated := loaded
	updated.Name = "renamed"
	updated.OwnerID = "different-owner"
	if _, err = r.Update(ctx, updated); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("owner mutation accepted: %v", err)
	}
	updated.OwnerID = loaded.OwnerID
	updated.ExpiresAt = func() *time.Time { v := time.Now().Add(time.Hour); return &v }()
	if _, err = r.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}

	rotated, err := r.Rotate(ctx, appKey, created.ID, newToken("rotated"), true)
	if err != nil || len(rotated.Permissions) != 1 {
		t.Fatalf("rotate token: %+v %v", rotated, err)
	}
	if old, getErr := r.Get(ctx, appKey, created.ID); getErr != nil || old.Status != "revoked" {
		t.Fatalf("old token was not revoked: %+v %v", old, getErr)
	}
	if _, err = r.SetStatus(ctx, appKey, created.ID, "active"); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("revoked token resurrected: %v", err)
	}

	concurrent := newToken("concurrent")
	concurrent, err = r.Create(ctx, concurrent)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		value, getErr := r.Get(ctx, appKey, concurrent.ID)
		if getErr == nil {
			value.Name = "racing-update"
			_, _ = r.Update(ctx, value)
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = r.SetStatus(ctx, appKey, concurrent.ID, "revoked")
	}()
	wg.Wait()
	final, err := r.Get(ctx, appKey, concurrent.ID)
	if err != nil || final.Status != "revoked" {
		t.Fatalf("concurrent update resurrected token: %+v %v", final, err)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
