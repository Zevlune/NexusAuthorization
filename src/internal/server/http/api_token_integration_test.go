package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/config"
	"github.com/luck/permission-center-go/internal/data/db/repo"
)

// This test uses the real repository and HTTP handlers. It requires a
// disposable database initialized with migrations/001_init.sql and
// migrations/003_api_tokens.sql.
func TestAPITokenHTTPRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()
	appKey := "api-token-http-" + suffix
	otherAppKey := "api-token-other-" + suffix
	serviceKey := "api-token-service-" + suffix
	internalKey := "api-token-internal-" + suffix

	var appID, otherAppID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,enabled) VALUES($1,$2,TRUE) RETURNING id`, appKey, appKey).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		appKeys := []string{appKey, otherAppKey}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_tokens WHERE application_id IN (SELECT id FROM applications WHERE app_key=ANY($1::text[]))`, appKeys)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_permission_groups WHERE service_resource_key=$1`, serviceKey)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, serviceKey)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM application_service_resources WHERE application_id IN (SELECT id FROM applications WHERE app_key=ANY($1::text[]))`, appKeys)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM applications WHERE app_key=ANY($1::text[])`, appKeys)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM service_resources WHERE resource_key=ANY($1::text[])`, []string{serviceKey})
	})
	if err = pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,enabled) VALUES($1,$2,TRUE) RETURNING id`, otherAppKey, otherAppKey).Scan(&otherAppID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$2)`, serviceKey, serviceKey); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{appID, otherAppID} {
		if _, err = pool.Exec(ctx, `INSERT INTO application_service_resources(application_id,service_resource_key) VALUES($1,$2)`, id, serviceKey); err != nil {
			t.Fatal(err)
		}
	}
	endpointRead := integrationInsertEndpoint(t, ctx, pool, serviceKey, "read", "GET", "/items/{id}", "Read item")
	endpointWrite := integrationInsertEndpoint(t, ctx, pool, serviceKey, "write", "POST", "/items", "Write item")
	endpointLocal := integrationInsertEndpoint(t, ctx, pool, serviceKey, "local", "GET", "/v1/rbac-v1/roles", "List roles")

	// Use the real service directory for application-linked group maintenance.
	serviceResources, err := biz.NewServiceResourceCatalog(repo.NewServiceResourceRepository(pool), nil, biz.ServiceResourceSourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	tokenService := biz.NewAPITokenService(repo.NewAPITokenRepository(pool))
	adminSubject := "api-token-integration-admin-" + suffix
	handler := NewHandler(nil).
		WithServiceResourceCatalog(serviceResources).
		WithServiceResourceService(serviceResources).
		WithRBACV1Service(biz.NewRBACV1Service(repo.NewRBACV1Repository(pool, serviceResources))).
		WithAPIPermissionGroups(biz.NewAPIPermissionGroupService(repo.NewAPIPermissionGroupRepository(pool))).
		WithAPITokens(tokenService, config.APITokenConfig{
			ServiceResource: serviceKey,
			AdminSubjects:   []string{adminSubject},
			InternalCallers: []config.APITokenInternalCaller{{ServiceResource: serviceKey, Key: internalKey}},
		}, true)
	managementServer := NewServer(handler, integrationIdentity(adminSubject))
	internalServer := NewServer(handler)

	groupResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-permission-groups?app_key="+appKey+"&service_resource="+serviceKey, biz.APIPermissionGroup{Code: "items.read", Name: "Items Read", Action: "read", Enabled: true, EndpointIDs: []uuid.UUID{endpointRead}}, "")
	if groupResponse.Code != http.StatusCreated {
		t.Fatalf("group create: %d %s", groupResponse.Code, groupResponse.Body.String())
	}
	var groupEnvelope struct {
		Result biz.APIPermissionGroup `json:"result"`
	}
	integrationDecode(t, groupResponse, &groupEnvelope)
	groupList := integrationRequest(t, managementServer, http.MethodGet, "/v1/api-permission-groups?app_key="+appKey, nil, "")
	if groupList.Code != 200 || !strings.Contains(groupList.Body.String(), "items.read") {
		t.Fatalf("group list: %d %s", groupList.Code, groupList.Body.String())
	}
	createResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens?app_key="+appKey, biz.APITokenInput{
		Name:     "integration token",
		Policies: []biz.APITokenPolicy{{Effect: "allow", ServiceResource: serviceKey, TenantID: "tenant-a", ProjectID: "project-a", PermissionGroupIDs: []uuid.UUID{groupEnvelope.Result.ID}}},
	}, "")
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", createResponse.Code, createResponse.Body.String())
	}
	var createdEnvelope struct {
		Result biz.APITokenCreated `json:"result"`
	}
	integrationDecode(t, createResponse, &createdEnvelope)
	created := createdEnvelope.Result
	if created.RawToken == "" || created.Token.TokenHash != "" || len(created.Token.Permissions) != 1 || len(created.Token.Policies) != 1 || created.Token.OwnerID != appKey {
		t.Fatalf("real token was not created: %+v", created)
	}
	stored, err := tokenService.Get(ctx, appKey, created.Token.ID)
	if err != nil || stored.TokenHash == "" || stored.TokenHash != biz.APITokenHash(created.RawToken) {
		t.Fatalf("stored credential hash incorrect: %v", err)
	}

	listResponse := integrationRequest(t, managementServer, http.MethodGet, "/v1/api-tokens?app_key="+appKey, nil, "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Result biz.RBACPage[biz.APIToken] `json:"result"`
	}
	integrationDecode(t, listResponse, &listed)
	if len(listed.Result.Items) != 1 || len(listed.Result.Items[0].Permissions) != 1 {
		t.Fatalf("list did not include permissions: %+v", listed.Result)
	}
	if strings.Contains(listResponse.Body.String(), stored.TokenHash) || strings.Contains(listResponse.Body.String(), created.RawToken) {
		t.Fatal("list response leaked token hash or raw token")
	}

	readTarget := biz.APITokenTarget{AppKey: appKey, ServiceResource: serviceKey, Method: "GET", Path: "/items/42", TenantID: "tenant-a", ProjectID: "project-a"}
	status, decision := integrationAuthorize(t, internalServer, internalKey, created.RawToken, readTarget)
	if status != http.StatusOK || !decision.Active || !decision.Allowed {
		t.Fatalf("internal allow failed: status=%d decision=%+v", status, decision)
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, readTarget)
	if status != http.StatusOK || !decision.Allowed {
		t.Fatalf("repeat internal allow failed: status=%d decision=%+v", status, decision)
	}
	wrongTenant := readTarget
	wrongTenant.TenantID = "tenant-b"
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, wrongTenant)
	if status != http.StatusOK || !decision.Active || decision.Allowed {
		t.Fatalf("wrong scope was allowed: status=%d decision=%+v", status, decision)
	}
	wrongService := readTarget
	wrongService.ServiceResource = "other-service"
	status, _ = integrationAuthorize(t, internalServer, internalKey, created.RawToken, wrongService)
	if status != http.StatusForbidden {
		t.Fatalf("wrong internal service scope status=%d", status)
	}

	updatedInput := biz.APITokenInput{
		Name: "integration token",
		Permissions: []biz.APITokenPermission{{
			EndpointID: endpointWrite, ServiceResource: serviceKey, Action: "write",
			TenantID: "tenant-b", ProjectID: "project-b",
		}},
	}
	updateResponse := integrationRequest(t, managementServer, http.MethodPut, "/v1/api-tokens/"+created.Token.ID.String()+"?app_key="+appKey, updatedInput, "")
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", updateResponse.Code, updateResponse.Body.String())
	}
	oldTarget := readTarget
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, oldTarget)
	if status != http.StatusOK || !decision.Active || decision.Allowed {
		t.Fatalf("old permission remained active: status=%d decision=%+v", status, decision)
	}
	writeTarget := biz.APITokenTarget{AppKey: appKey, ServiceResource: serviceKey, Method: "POST", Path: "/items", TenantID: "tenant-b", ProjectID: "project-b"}
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, writeTarget)
	if status != http.StatusOK || !decision.Active || !decision.Allowed {
		t.Fatalf("updated permission was not active: status=%d decision=%+v", status, decision)
	}

	statusResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens/"+created.Token.ID.String()+"/status?app_key="+appKey, map[string]string{"status": "disabled"}, "")
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("disable status %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, writeTarget)
	if status != http.StatusOK || decision.Active || decision.Allowed {
		t.Fatalf("disabled token still authorized: status=%d decision=%+v", status, decision)
	}
	statusResponse = integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens/"+created.Token.ID.String()+"/status?app_key="+appKey, map[string]string{"status": "active"}, "")
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("re-enable status %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, writeTarget)
	if status != http.StatusOK || !decision.Active || !decision.Allowed {
		t.Fatalf("re-enabled token was not authorized: status=%d decision=%+v", status, decision)
	}

	rotateResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens/"+created.Token.ID.String()+"/rotate?app_key="+appKey, map[string]bool{"revoke_old": true}, "")
	if rotateResponse.Code != http.StatusCreated {
		t.Fatalf("rotate status %d: %s", rotateResponse.Code, rotateResponse.Body.String())
	}
	var rotatedEnvelope struct {
		Result biz.APITokenCreated `json:"result"`
	}
	integrationDecode(t, rotateResponse, &rotatedEnvelope)
	rotated := rotatedEnvelope.Result
	if rotated.RawToken == "" || rotated.RawToken == created.RawToken || rotated.Token.ID == created.Token.ID {
		t.Fatalf("rotation did not produce a new token: %+v", rotated)
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, created.RawToken, writeTarget)
	if status != http.StatusOK || decision.Active || decision.Allowed {
		t.Fatalf("revoked old token still authorized: status=%d decision=%+v", status, decision)
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, rotated.RawToken, writeTarget)
	if status != http.StatusOK || !decision.Active || !decision.Allowed {
		t.Fatalf("rotated token was not authorized: status=%d decision=%+v", status, decision)
	}

	revokeResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens/"+rotated.Token.ID.String()+"/revoke?app_key="+appKey, nil, "")
	if revokeResponse.Code != http.StatusOK {
		t.Fatalf("revoke status %d: %s", revokeResponse.Code, revokeResponse.Body.String())
	}
	resurrectResponse := integrationRequest(t, managementServer, http.MethodPost, "/v1/api-tokens/"+rotated.Token.ID.String()+"/status?app_key="+appKey, map[string]string{"status": "active"}, "")
	if resurrectResponse.Code != http.StatusConflict {
		t.Fatalf("revoked token was reactivated: %d %s", resurrectResponse.Code, resurrectResponse.Body.String())
	}
	status, decision = integrationAuthorize(t, internalServer, internalKey, rotated.RawToken, writeTarget)
	if status != http.StatusOK || decision.Active || decision.Allowed {
		t.Fatalf("revoked token still authorized: status=%d decision=%+v", status, decision)
	}

	localToken, err := tokenService.Create(ctx, appKey, biz.APITokenInput{
		Name: "local middleware token", OwnerType: "user", OwnerID: "integration-user",
		Permissions: []biz.APITokenPermission{{EndpointID: endpointLocal, ServiceResource: serviceKey, Action: "read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	nextCalled := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		if _, ok := apiTokenFromRequest(r); !ok {
			t.Fatal("local middleware did not attach token decision")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	identityCalls := 0
	identity := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identityCalls++
			next.ServeHTTP(w, r)
		})
	}
	localServer := handler.tokenOrIdentity(identity, next)
	localRequest := func(app string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/rbac-v1/roles?app_key="+app, nil)
		request.Header.Set("Authorization", "Bearer "+localToken.RawToken)
		response := httptest.NewRecorder()
		localServer.ServeHTTP(response, request)
		return response
	}
	if response := localRequest(appKey); response.Code != http.StatusNoContent {
		t.Fatalf("local token was denied in owner app: %d %s", response.Code, response.Body.String())
	}
	if response := localRequest(otherAppKey); response.Code != http.StatusForbidden {
		t.Fatalf("local token crossed app boundary: %d %s", response.Code, response.Body.String())
	}
	if nextCalled != 1 || identityCalls != 0 {
		t.Fatalf("local middleware fallback/call count: next=%d identity=%d", nextCalled, identityCalls)
	}
}

func integrationInsertEndpoint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, serviceKey, key, method, path, summary string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,summary,enabled) VALUES($1,$2,$3,$4,$5,TRUE) RETURNING id`, serviceKey, key+"-"+uuid.NewString(), method, path, summary).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func integrationIdentity(subject string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{Subject: subject})))
		})
	}
}

func integrationRequest(t *testing.T, server http.Handler, method, path string, body any, internalKey string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if internalKey != "" {
		request.Header.Set("X-Internal-Service-Key", internalKey)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func integrationAuthorize(t *testing.T, server http.Handler, internalKey, rawToken string, target biz.APITokenTarget) (int, biz.APITokenDecision) {
	t.Helper()
	request := map[string]any{
		"token": rawToken, "app_key": target.AppKey, "service_resource": target.ServiceResource,
		"method": target.Method, "path": target.Path, "tenant_id": target.TenantID, "project_id": target.ProjectID,
	}
	response := integrationRequest(t, server, http.MethodPost, "/internal/v1/api-tokens/authorize", request, internalKey)
	var decision biz.APITokenDecision
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil {
			t.Fatalf("decode authorization response: %v (%s)", err, response.Body.String())
		}
	}
	return response.Code, decision
}

func integrationDecode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode HTTP response: %v (%s)", err, response.Body.String())
	}
}
