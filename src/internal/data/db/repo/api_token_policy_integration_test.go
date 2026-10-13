package repo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

func TestAPITokenPermissionGroupPoliciesIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	suffix := uuid.NewString()
	appKey, serviceKey := "policy-app-"+suffix, "policy-service-"+suffix
	var appID, getID, postID uuid.UUID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,enabled) VALUES($1,$1,TRUE) RETURNING id`, appKey).Scan(&appID))
	_, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, serviceKey)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO application_service_resources(application_id,service_resource_key) VALUES($1,$2)`, appID, serviceKey)
	must(err)
	must(pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,enabled) VALUES($1,$2,'GET','/items',TRUE) RETURNING id`, serviceKey, "get-"+suffix).Scan(&getID))
	must(pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,enabled) VALUES($1,$2,'POST','/items',TRUE) RETURNING id`, serviceKey, "post-"+suffix).Scan(&postID))
	defer func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, appID)
		pool.Exec(ctx, `DELETE FROM api_permission_groups WHERE service_resource_key=$1`, serviceKey)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, serviceKey)
	}()
	groups := NewAPIPermissionGroupRepository(pool)
	group, err := groups.Save(ctx, biz.APIPermissionGroup{ServiceResource: serviceKey, Code: "items.write", Name: "Items Write", Action: "write", Enabled: true, EndpointIDs: []uuid.UUID{getID, postID}})
	must(err)
	service := biz.NewAPITokenService(NewAPITokenRepository(pool))
	policy := biz.APITokenPolicy{Effect: "allow", ServiceResource: serviceKey, TenantID: "tenant-a", PermissionGroupIDs: []uuid.UUID{group.ID}}
	input := biz.APITokenInput{Name: "Policy token", Policies: []biz.APITokenPolicy{policy}}
	created, err := service.Create(ctx, appKey, input)
	must(err)
	if created.Token.OwnerType != "service" || created.Token.OwnerID != appKey || len(created.Token.Policies) != 1 || len(created.Token.Permissions) != 2 {
		t.Fatal("policy token not resolved or no automatic identity")
	}
	var snapshots int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM api_token_permissions WHERE token_id=$1`, created.Token.ID).Scan(&snapshots))
	if snapshots != 0 {
		t.Fatal("group token must not persist legacy grant snapshots")
	}
	check := func(method, tenant string, want bool) {
		t.Helper()
		d, err := service.Authorize(ctx, created.RawToken, biz.APITokenTarget{AppKey: appKey, ServiceResource: serviceKey, Method: method, Path: "/items", TenantID: tenant})
		must(err)
		if !d.Active || d.Allowed != want {
			t.Fatalf("%s/%s: %+v expected %v", method, tenant, d, want)
		}
	}
	check("GET", "tenant-a", true)
	check("POST", "tenant-a", true)
	check("GET", "tenant-b", false)
	group.EndpointIDs = []uuid.UUID{postID}
	group, err = groups.Save(ctx, group)
	must(err)
	check("GET", "tenant-a", false)
	check("POST", "tenant-a", true)
	group.Enabled = false
	group, err = groups.Save(ctx, group)
	must(err)
	check("POST", "tenant-a", false)
	if _, err = service.Rotate(ctx, appKey, created.Token.ID, false); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("disabled policy group rotation accepted: %v", err)
	}
	group.Enabled = true
	group, err = groups.Save(ctx, group)
	must(err)
	rotated, err := service.Rotate(ctx, appKey, created.Token.ID, false)
	must(err)
	if len(rotated.Token.Policies) != 1 || rotated.Token.Policies[0].PermissionGroupIDs[0] != group.ID {
		t.Fatal("rotation lost policy references")
	}
	loaded, err := service.Get(ctx, appKey, created.Token.ID)
	must(err)
	if len(loaded.Policies) != 1 || len(loaded.Permissions) != 1 {
		t.Fatal("detail must return policy refs and live grants")
	}
	overlap := input
	overlap.Policies = []biz.APITokenPolicy{policy, policy}
	overlapped, err := service.Create(ctx, appKey, overlap)
	must(err)
	if len(overlapped.Token.Permissions) != 1 {
		t.Fatal("overlapping policies did not union grants")
	}
	scoped := policy
	scoped.TenantID = "tenant-b"
	input.Policies = append(input.Policies, scoped)
	updated, err := service.Update(ctx, appKey, created.Token.ID, input)
	must(err)
	if len(updated.Policies) != 2 {
		t.Fatal("scopes lost on update")
	}
	check("POST", "tenant-b", true)
	for _, bad := range []biz.APITokenPolicy{
		{Effect: "deny", ServiceResource: serviceKey, PermissionGroupIDs: []uuid.UUID{group.ID}},
		{Effect: "allow", ServiceResource: "other", PermissionGroupIDs: []uuid.UUID{group.ID}},
		{Effect: "allow", ServiceResource: serviceKey, PermissionGroupIDs: []uuid.UUID{uuid.New()}},
		{Effect: "allow", ServiceResource: serviceKey, PermissionGroupIDs: []uuid.UUID{group.ID, group.ID}},
	} {
		if _, err := service.Create(ctx, appKey, biz.APITokenInput{Name: "Invalid", Policies: []biz.APITokenPolicy{bad}}); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	if err := groups.Delete(ctx, serviceKey, group.ID); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("referenced group deletion accepted: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE api_endpoints SET enabled=FALSE WHERE id=$1`, postID)
	must(err)
	check("POST", "tenant-a", false)
	_, err = pool.Exec(ctx, `UPDATE api_endpoints SET enabled=TRUE WHERE id=$1`, postID)
	must(err)
	_, err = pool.Exec(ctx, `UPDATE service_resources SET enabled=FALSE WHERE resource_key=$1`, serviceKey)
	if err == nil {
		t.Fatal("linked service should be protected from disabling")
	}
	_, err = pool.Exec(ctx, `DELETE FROM application_service_resources WHERE application_id=$1`, appID)
	must(err)
	check("POST", "tenant-a", false)
	_, err = pool.Exec(ctx, `UPDATE service_resources SET enabled=FALSE WHERE resource_key=$1`, serviceKey)
	must(err)
	check("POST", "tenant-a", false)
}
