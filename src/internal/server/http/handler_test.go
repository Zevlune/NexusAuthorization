package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
)

func TestSwaggerUIAndOpenAPIDocument(t *testing.T) {
	server := NewServer(NewHandler(nil))

	redirect := httptest.NewRecorder()
	server.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/swagger/index.html" {
		t.Fatalf("swagger redirect = %d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}

	ui := httptest.NewRecorder()
	server.ServeHTTP(ui, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if ui.Code != http.StatusOK || !strings.Contains(ui.Body.String(), "Swagger UI") {
		t.Fatalf("swagger UI = %d body=%s", ui.Code, ui.Body.String())
	}

	documentResponse := httptest.NewRecorder()
	server.ServeHTTP(documentResponse, httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil))
	if documentResponse.Code != http.StatusOK {
		t.Fatalf("openapi document = %d body=%s", documentResponse.Code, documentResponse.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(documentResponse.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	if document["openapi"] != "3.0.3" {
		t.Fatalf("openapi version = %#v", document["openapi"])
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI paths missing from document: %#v", document["paths"])
	}
	for path := range paths {
		if path == "/api/v1/authorization/decisions" || path == "/api/v1/pdp/decisions" || strings.HasPrefix(path, "/api/v1/rbac-config") {
			t.Fatalf("legacy path present in OpenAPI document: %s", path)
		}
		if strings.Contains(path, "releases") {
			t.Fatalf("retired release path present in OpenAPI document: %s", path)
		}
	}
	if strings.Contains(documentResponse.Body.String(), "release_version") {
		t.Fatal("retired release_version field present in OpenAPI document")
	}
	for _, operation := range []struct {
		path   string
		method string
	}{
		{path: "/api/v1/applications", method: "get"},
		{path: "/api/v1/applications", method: "post"},
		{path: "/api/v1/applications/{key}", method: "put"},
		{path: "/api/v1/applications/{key}/service-resources", method: "get"},
		{path: "/api/v1/applications/{key}/service-resources", method: "put"},
		{path: "/api/v1/rbac-v1/menus", method: "get"},
		{path: "/api/v1/rbac-v1/menus", method: "post"},
		{path: "/api/v1/rbac-v1/menus/{id}", method: "delete"},
		{path: "/api/v1/rbac-v1/subject-menus", method: "get"},
		{path: "/api/v1/rbac-v1/endpoints", method: "get"},
		{path: "/api/v1/rbac-v1/endpoints", method: "post"},
		{path: "/api/v1/rbac-v1/endpoints/import-swagger", method: "post"},
		{path: "/api/v1/rbac-v1/endpoints/{id}", method: "delete"},
	} {
		pathItem, ok := paths[operation.path].(map[string]any)
		if !ok || pathItem[operation.method] == nil {
			t.Fatalf("OpenAPI operation missing: %s %s", strings.ToUpper(operation.method), operation.path)
		}
	}

	// The default domain is optional: authorization must not require it, while
	// named domains stay available through the retained property.
	authorize := paths["/api/v1/authorize"].(map[string]any)["post"].(map[string]any)
	required := authorize["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["required"].([]any)
	for _, name := range required {
		if name == "domain" {
			t.Fatal("OpenAPI still lists domain as a required authorization parameter")
		}
	}
	properties := authorize["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := properties["domain"]; !ok {
		t.Fatal("OpenAPI dropped the named-domain property")
	}
	menuParameters := paths["/api/v1/rbac-v1/subject-menus"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)
	domainOptional := false
	for _, raw := range menuParameters {
		parameter := raw.(map[string]any)
		if parameter["name"] == "domain" {
			if parameter["required"] == true {
				t.Fatal("subject-menus still requires domain")
			}
			domainOptional = true
		}
	}
	if !domainOptional {
		t.Fatal("subject-menus no longer documents the optional domain filter")
	}
}

func TestCanonicalAPIRoutesUseAPIPrefix(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, testCase := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/healthz", http.StatusOK},
		{http.MethodGet, "/healthz", http.StatusNotFound},
		{http.MethodGet, "/api/auth/config", http.StatusOK},
		{http.MethodGet, "/auth/config", http.StatusNotFound},
		// The OIDC callback stays at /signin-oidc, outside the API prefix.
		{http.MethodGet, "/signin-oidc", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/signin-oidc", http.StatusNotFound},
		{http.MethodGet, "/api/swagger/openapi.json", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(testCase.method, testCase.path, nil))
		if response.Code != testCase.want {
			t.Fatalf("%s %s status = %d, want %d", testCase.method, testCase.path, response.Code, testCase.want)
		}
	}
}

// newJSONRequest builds a request with a JSON body, as browsers and the SDK
// send it.
func newJSONRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestApplicationMenuRequiresAppKey(t *testing.T) {
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(&testRBACRepo{})))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/rbac-v1/menus", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing app_key status = %d body=%s", response.Code, response.Body.String())
	}
}

type testRBACRepo struct {
	scope   string
	subject string
	domain  string
	roleIDs []uuid.UUID
}

func (*testRBACRepo) ListEndpoints(context.Context, string) ([]biz.RBACEndpoint, error) {
	return nil, nil
}
func (*testRBACRepo) ListEndpointsPage(context.Context, string, int, int) ([]biz.RBACEndpoint, int, error) {
	return nil, 0, nil
}
func (*testRBACRepo) SaveEndpoint(context.Context, string, biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	return biz.RBACEndpoint{}, nil
}
func (*testRBACRepo) DeleteEndpoint(context.Context, string, uuid.UUID) error { return nil }
func (*testRBACRepo) ListRoles(context.Context, string) ([]biz.RBACRole, error) {
	return nil, nil
}
func (*testRBACRepo) SaveRole(context.Context, string, biz.RBACRole) (biz.RBACRole, error) {
	return biz.RBACRole{}, nil
}
func (*testRBACRepo) DeleteRole(context.Context, string, uuid.UUID) error { return nil }
func (*testRBACRepo) ListSubjectRoleBindings(context.Context, string) ([]biz.SubjectRoleBinding, error) {
	return nil, nil
}
func (r *testRBACRepo) ReplaceSubjectRoles(_ context.Context, scope, subject, domain string, roleIDs []uuid.UUID) error {
	r.scope, r.subject, r.domain = scope, subject, domain
	r.roleIDs = append([]uuid.UUID(nil), roleIDs...)
	return nil
}
func (*testRBACRepo) GetPolicyMetadata(context.Context, string) (biz.CurrentPolicy, error) {
	return biz.CurrentPolicy{}, nil
}
func (*testRBACRepo) GetCurrentPolicy(context.Context, biz.CurrentPolicy) (biz.CurrentPolicy, error) {
	return biz.CurrentPolicy{}, nil
}

func TestRoleCreateEndpointIsRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/roles", `{"service_resource":"admin","code":"operator","name":"Operator"}`))
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired role create status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestRejectsUnknownJSONFields(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)
	request := newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"orders","display_name":"Orders","other":true}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown service resource field status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestMenuEndpointsAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/menus"},
		{method: http.MethodGet, path: "/api/v1/menus/tree?service_resource=admin"},
		{method: http.MethodPut, path: "/api/v1/roles/role-1/menus"},
		{method: http.MethodGet, path: "/api/v1/roles/role-1/menus"},
		{method: http.MethodGet, path: "/api/v1/resources/tree?service_resource=admin"},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired menu route %s %s status = %d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestMenuMetadataRoutesAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, payload := range []string{
		`{"service_resource":"admin","code":"create","name":"Create","type":"button"}`,
		`{"service_resource":"admin","code":"settings","name":"Settings","type":"menu","api_path":"/v1/settings"}`,
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/menus", payload))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired menu metadata route payload %s status = %d body=%s", payload, response.Code, response.Body.String())
		}
	}
}

func TestReplaceRBACV1SubjectRoles(t *testing.T) {
	roleID := uuid.New()
	for _, tc := range []struct {
		name, body, wantDomain string
	}{
		{"legacy global folds to the default", `{"subject":"nexus-user-1","domain":"global","role_ids":["` + roleID.String() + `"]}`, ""},
		{"missing domain selects the default", `{"subject":"nexus-user-1","role_ids":["` + roleID.String() + `"]}`, ""},
		{"named domain is preserved", `{"subject":"nexus-user-1","domain":"tenant-a","role_ids":["` + roleID.String() + `"]}`, "tenant-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &testRBACRepo{}
			server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
			request := newJSONRequest(http.MethodPut, "/api/v1/rbac-v1/subject-roles?app_key=admin", tc.body)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if repository.scope != "admin" || repository.subject != "nexus-user-1" || repository.domain != tc.wantDomain || len(repository.roleIDs) != 1 || repository.roleIDs[0] != roleID {
				t.Fatalf("assignment = %#v", repository)
			}
		})
	}
}

func TestRoleUpdateAndDeleteEndpointsAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, "/api/v1/roles/role-1", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired role %s status = %d body=%s", method, response.Code, response.Body.String())
		}
	}
}

func TestMenuUpdateAndDeleteEndpointsAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, path := range []string{"/api/v1/menus/123", "/api/v1/menus/not-a-uuid"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("retired menu %s %s status = %d body=%s", method, path, response.Code, response.Body.String())
			}
		}
	}
}

func TestLegacyRoutesAreNotFound(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, legacyPath := range []string{
		"/v1/authorization/decisions",
		"/v1/pdp/decisions",
		"/v1/applications",
		"/v1/rbac-config",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, legacyPath, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy route %s status = %d", legacyPath, response.Code)
		}
	}
}

func TestRBACReleaseRoutesAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := httptest.NewRecorder()
		request := newJSONRequest(method, "/api/v1/rbac-v1/releases", `{"description":"legacy"}`)
		server.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired release route %s status = %d body=%s", method, response.Code, response.Body.String())
		}
	}
}

func TestDevelopmentModeInjectsAuditActor(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	service, err := biz.NewServiceResourceCatalog(repository, nil, biz.ServiceResourceSourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.New(context.Background(), config.OIDCConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(NewHandler(nil, authenticator).
		WithServiceResourceCatalog(service).
		WithServiceResourceService(service))
	request := newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"orders","display_name":"Orders"}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.actors) != 1 || repository.actors[0].ID != "development" || repository.actors[0].Name != "开发模式" {
		t.Fatalf("audit actor = %#v", repository.actors)
	}
}
