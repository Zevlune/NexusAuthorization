package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

// Requires a disposable database initialized with migrations/001_init.sql,
// migrations/002_application_url.sql, migrations/003_api_tokens.sql and
// migrations/004_api_permission_groups.sql.
func TestAPIPermissionGroupRepositoryIntegration(t *testing.T) {
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
	serviceKey, otherServiceKey := "pg-service-"+suffix, "pg-other-"+suffix
	appKey := "pg-app-" + suffix
	for _, key := range []string{serviceKey, otherServiceKey} {
		if _, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$2)`, key, key); err != nil {
			t.Fatal(err)
		}
	}
	var appID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,enabled) VALUES($1,$2,TRUE) RETURNING id`, appKey, appKey).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	newEndpoint := func(service, key, method, path string, enabled bool) uuid.UUID {
		var id uuid.UUID
		if err = pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,summary,enabled) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
			service, key, method, path, path, enabled).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	getID := newEndpoint(serviceKey, "get-"+suffix, "GET", "/items/{id}", true)
	headID := newEndpoint(serviceKey, "head-"+suffix, "HEAD", "/items/{id}", true)
	postID := newEndpoint(serviceKey, "post-"+suffix, "POST", "/items", true)
	putID := newEndpoint(serviceKey, "put-"+suffix, "PUT", "/items/{id}", true)
	disabledID := newEndpoint(serviceKey, "disabled-"+suffix, "GET", "/disabled", false)
	foreignID := newEndpoint(otherServiceKey, "foreign-"+suffix, "GET", "/foreign", true)
	releaseID := newEndpoint(serviceKey, "release-"+suffix, "GET", "/release", true)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM api_tokens WHERE application_id=$1`, appID)
		pool.Exec(ctx, `DELETE FROM api_permission_groups WHERE service_resource_key=ANY($1::text[])`, []string{serviceKey, otherServiceKey})
		pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, appID)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE id=ANY($1::uuid[])`, []uuid.UUID{getID, headID, postID, putID, disabledID, foreignID, releaseID})
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=ANY($1::text[])`, []string{serviceKey, otherServiceKey})
	})

	r := NewAPIPermissionGroupRepository(pool)
	readGroup := biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "items-read", Name: "Items read", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{getID, headID}}
	created, err := r.Save(ctx, readGroup)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("create did not populate identity: %+v", created)
	}
	if len(created.EndpointIDs) != 2 {
		t.Fatalf("create did not persist endpoints: %+v", created.EndpointIDs)
	}

	// Write groups may include read-shaped methods such as POST.
	writeGroup := biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "items-write", Name: "Items write", Action: "write", Enabled: true, EndpointIDs: []uuid.UUID{postID, getID, putID}}
	if _, err = r.Save(ctx, writeGroup); err != nil {
		t.Fatalf("write group rejected: %v", err)
	}

	groups, err := r.List(ctx, serviceKey)
	if err != nil || len(groups) != 2 {
		t.Fatalf("list groups: %+v %v", groups, err)
	}
	other, err := r.List(ctx, otherServiceKey)
	if err != nil || len(other) != 0 {
		t.Fatalf("list leaked cross-service groups: %+v %v", other, err)
	}

	for _, tc := range []struct {
		name  string
		group biz.APIPermissionGroup
		want  error
	}{
		{"read group with POST", biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "bad-read-" + suffix, Name: "bad", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{postID}}, biz.ErrInvalidArgument},
		{"disabled endpoint", biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "bad-disabled-" + suffix, Name: "bad", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{disabledID}}, biz.ErrConflict},
		{"cross service endpoint", biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "bad-cross-" + suffix, Name: "bad", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{foreignID}}, biz.ErrConflict},
		{"unknown endpoint", biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "bad-missing-" + suffix, Name: "bad", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{uuid.New()}}, biz.ErrConflict},
		{"duplicate code", biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "items-read", Name: "dupe", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{getID}}, biz.ErrAlreadyExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Save(ctx, tc.group); !errors.Is(err, tc.want) {
				t.Fatalf("save error=%v want %v", err, tc.want)
			}
		})
	}

	// The same code is allowed in a different service because the code is scoped.
	if _, err = r.Save(ctx, biz.APIPermissionGroup{ServiceResource: otherServiceKey, Code: "items-read", Name: "Items read", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{foreignID}}); err != nil {
		t.Fatalf("service-scoped code rejected: %v", err)
	}

	updated := created
	updated.Name = "Items read v2"
	updated.EndpointIDs = []uuid.UUID{getID}
	updated.Enabled = false
	updated, err = r.Save(ctx, updated)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Name != "Items read v2" || updated.Enabled || len(updated.EndpointIDs) != 1 || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update did not apply bounded change: %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("updated_at went backwards: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
	}

	immutableCode := updated
	immutableCode.Code = "renamed"
	if _, err = r.Save(ctx, immutableCode); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("code mutation accepted: %v", err)
	}
	immutableService := updated
	immutableService.ServiceResource = otherServiceKey
	if _, err = r.Save(ctx, immutableService); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("service mutation accepted: %v", err)
	}
	if _, err = r.Save(ctx, biz.APIPermissionGroup{ID: uuid.New(), ServiceResource: serviceKey, Code: "ghost", Name: "ghost", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{getID}}); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("update of missing group error=%v", err)
	}

	// The trigger refuses membership rows that bypass the repository and point
	// at a different service.
	if _, err = pool.Exec(ctx, `INSERT INTO api_permission_group_endpoints(group_id,endpoint_id) VALUES($1,$2)`, updated.ID, foreignID); err == nil {
		t.Fatal("trigger accepted cross-service membership")
	}

	// A group referenced by a token policy cannot be deleted.
	policies := fmt.Sprintf(`[{"id":"%s","effect":"allow","service_resource":"%s","tenant_id":"","project_id":"","permission_group_ids":["%s"]}]`, uuid.New(), serviceKey, updated.ID)
	var tokenID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO api_tokens(id,application_id,name,token_prefix,token_hash,owner_type,owner_id,status,policies,created_by)
        VALUES($1,$2,'policy token',$3,$4,'service','worker','active',$5::jsonb,'test') RETURNING id`,
		uuid.New(), appID, "nxa_"+uuid.NewString()[:8], biz.APITokenHash(uuid.NewString()), policies).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	if err = r.Delete(ctx, serviceKey, updated.ID); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("referenced group delete error=%v", err)
	}
	if err = r.Delete(ctx, otherServiceKey, updated.ID); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("cross-service delete error=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE api_tokens SET policies='[]'::jsonb WHERE id=$1`, tokenID); err != nil {
		t.Fatal(err)
	}

	// Deleting the referenced group then releases its endpoints.
	if err = r.Delete(ctx, serviceKey, updated.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if groups, err = r.List(ctx, serviceKey); err != nil || len(groups) != 1 || groups[0].Code != "items-write" {
		t.Fatalf("delete did not remove exactly one group: %+v %v", groups, err)
	}
	var membership int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM api_permission_group_endpoints WHERE group_id=$1`, updated.ID).Scan(&membership); err != nil || membership != 0 {
		t.Fatalf("membership rows not cascaded: %d %v", membership, err)
	}

	// Endpoint rows referenced by a group are protected from deletion, and are
	// released once the last referencing group is removed.
	releaseGroup, err := r.Save(ctx, biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "release", Name: "Release", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{releaseID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM api_endpoints WHERE id=$1`, releaseID); err == nil {
		t.Fatal("referenced endpoint was deleted")
	}
	if err = r.Delete(ctx, serviceKey, releaseGroup.ID); err != nil {
		t.Fatalf("release group delete failed: %v", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM api_endpoints WHERE id=$1`, releaseID); err != nil {
		t.Fatalf("endpoint remained locked after group delete: %v", err)
	}
}
