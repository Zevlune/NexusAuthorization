package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/config"
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
		if path == "/v1/authorization/decisions" || path == "/v1/pdp/decisions" || strings.HasPrefix(path, "/v1/rbac-config") {
			t.Fatalf("legacy path present in OpenAPI document: %s", path)
		}
	}
	for _, operation := range []struct {
		path   string
		method string
	}{
		{path: "/v1/applications", method: "get"},
		{path: "/v1/applications", method: "post"},
		{path: "/v1/applications/{key}", method: "put"},
		{path: "/v1/applications/{key}/service-resources", method: "get"},
		{path: "/v1/applications/{key}/service-resources", method: "put"},
		{path: "/v1/api-tokens", method: "get"},
		{path: "/v1/api-tokens", method: "post"},
		{path: "/v1/api-tokens/{id}", method: "get"},
		{path: "/v1/api-tokens/{id}", method: "put"},
		{path: "/v1/api-tokens/{id}/status", method: "post"},
		{path: "/v1/api-tokens/{id}/revoke", method: "post"},
		{path: "/v1/api-tokens/{id}/rotate", method: "post"},
		{path: "/v1/rbac-v1/menus", method: "get"},
		{path: "/v1/rbac-v1/menus", method: "post"},
		{path: "/v1/rbac-v1/menus/{id}", method: "delete"},
		{path: "/v1/rbac-v1/subject-menus", method: "get"},
		{path: "/v1/authorization/api-endpoints", method: "post"},
		{path: "/v1/authorization/api-endpoints", method: "get"},
		{path: "/v1/authorization/api-endpoints/import-swagger", method: "post"},
		{path: "/v1/authorization/api-endpoints/{id}", method: "get"},
		{path: "/v1/authorization/api-endpoints/{id}", method: "put"},
		{path: "/v1/authorization/api-endpoints/{id}", method: "delete"},
	} {
		pathItem, ok := paths[operation.path].(map[string]any)
		if !ok || pathItem[operation.method] == nil {
			t.Fatalf("OpenAPI operation missing: %s %s", strings.ToUpper(operation.method), operation.path)
		}
	}
	if _, ok := paths["/internal/v1/api-tokens/authorize"]; ok {
		t.Fatal("internal API token authorization path must not be public in OpenAPI")
	}
	tokenList := paths["/v1/api-tokens"].(map[string]any)["get"].(map[string]any)
	if !hasRequiredQueryParameter(tokenList, "app_key") {
		t.Fatal("API token list must require app_key")
	}
	components := document["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := components["APITokenCreated"]; !ok {
		t.Fatal("APITokenCreated schema missing")
	}
	permission := components["APITokenPermission"].(map[string]any)
	action := permission["properties"].(map[string]any)["action"].(map[string]any)
	if len(action["enum"].([]any)) != 2 {
		t.Fatalf("API token action scope schema = %#v", action["enum"])
	}
}

func hasRequiredQueryParameter(operation map[string]any, name string) bool {
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		return false
	}
	for _, raw := range parameters {
		parameter, ok := raw.(map[string]any)
		if ok && parameter["name"] == name && parameter["in"] == "query" && parameter["required"] == true {
			return true
		}
	}
	return false
}

func TestApplicationMenuRequiresAppKey(t *testing.T) {
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(&testRBACRepo{})))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/rbac-v1/menus", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing app_key status = %d body=%s", response.Code, response.Body.String())
	}
}

type testRoleRepo struct {
	values  map[string]*biz.Role
	menuIDs []uuid.UUID
}

func (r *testRoleRepo) Create(_ context.Context, value *biz.Role) (*biz.Role, error) {
	value.ID = "role-test"
	r.values[value.ID] = value
	return value, nil
}
func (r *testRoleRepo) Get(_ context.Context, id string) (*biz.Role, error) {
	value, ok := r.values[id]
	if !ok {
		return nil, biz.ErrNotFound
	}
	return value, nil
}
func (r *testRoleRepo) Update(_ context.Context, value *biz.Role) (*biz.Role, error) {
	if _, ok := r.values[value.ID]; !ok {
		return nil, biz.ErrNotFound
	}
	r.values[value.ID] = value
	return value, nil
}
func (r *testRoleRepo) SoftDelete(_ context.Context, id string) error {
	value, ok := r.values[id]
	if !ok {
		return biz.ErrNotFound
	}
	value.IsDeleted = true
	value.Enabled = false
	return nil
}
func (*testRoleRepo) ListByServiceResource(context.Context, string) ([]*biz.Role, error) {
	return nil, nil
}
func (r *testRoleRepo) ReplaceMenus(_ context.Context, _ string, menuIDs []uuid.UUID) error {
	r.menuIDs = append([]uuid.UUID(nil), menuIDs...)
	return nil
}
func (r *testRoleRepo) MenuIDs(context.Context, string) ([]uuid.UUID, error) {
	return append([]uuid.UUID(nil), r.menuIDs...), nil
}

type testMenuRepo struct {
	values map[uuid.UUID]*biz.Menu
}

type testUserRoleRepo struct {
	subject string
	app     string
	roleIDs []string
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
func (*testRBACRepo) BuildRBACSnapshot(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (*testRBACRepo) CreateRBACRelease(context.Context, string, string, string, map[string]any) (biz.RBACRelease, error) {
	return biz.RBACRelease{}, nil
}
func (*testRBACRepo) ListRBACReleases(context.Context, string) ([]biz.RBACRelease, error) {
	return nil, nil
}
func (*testRBACRepo) GetActiveRBACRelease(context.Context, string) (biz.RBACRelease, error) {
	return biz.RBACRelease{}, nil
}

func (r *testUserRoleRepo) ReplaceRoles(_ context.Context, subject, app string, roleIDs []string) error {
	r.subject, r.app, r.roleIDs = subject, app, append([]string(nil), roleIDs...)
	return nil
}
func (r *testUserRoleRepo) RoleIDs(context.Context, string, string) ([]string, error) {
	return append([]string(nil), r.roleIDs...), nil
}

func (r *testMenuRepo) Create(_ context.Context, menu *biz.Menu) (*biz.Menu, error) {
	if r.values == nil {
		r.values = map[uuid.UUID]*biz.Menu{}
	}
	if menu.ID == uuid.Nil {
		menu.ID = uuid.New()
	}
	r.values[menu.ID] = menu
	return menu, nil
}
func (r *testMenuRepo) Get(_ context.Context, id uuid.UUID) (*biz.Menu, error) {
	menu, ok := r.values[id]
	if !ok {
		return nil, biz.ErrNotFound
	}
	return menu, nil
}
func (r *testMenuRepo) Update(_ context.Context, menu *biz.Menu) (*biz.Menu, error) {
	if _, ok := r.values[menu.ID]; !ok {
		return nil, biz.ErrNotFound
	}
	r.values[menu.ID] = menu
	return menu, nil
}
func (r *testMenuRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	menu, ok := r.values[id]
	if !ok {
		return biz.ErrNotFound
	}
	menu.IsDeleted = true
	menu.Enabled = false
	return nil
}
func (r *testMenuRepo) ListByServiceResource(_ context.Context, serviceResource string) ([]*biz.Menu, error) {
	menus := make([]*biz.Menu, 0)
	for _, menu := range r.values {
		if menu.ServiceResource == serviceResource {
			menus = append(menus, menu)
		}
	}
	return menus, nil
}

func TestRoleCreateEndpointIsRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/roles", strings.NewReader(`{"service_resource":"admin","code":"operator","name":"Operator"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired role create status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestRejectsUnknownJSONFields(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)
	request := httptest.NewRequest(http.MethodPost, "/v1/service-resources", strings.NewReader(`{"key":"orders","display_name":"Orders","other":true}`))
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
		{method: http.MethodPost, path: "/v1/menus"},
		{method: http.MethodGet, path: "/v1/menus/tree?service_resource=admin"},
		{method: http.MethodPut, path: "/v1/roles/role-1/menus"},
		{method: http.MethodGet, path: "/v1/roles/role-1/menus"},
		{method: http.MethodGet, path: "/v1/resources/tree?service_resource=admin"},
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
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/menus", strings.NewReader(payload)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired menu metadata route payload %s status = %d body=%s", payload, response.Code, response.Body.String())
		}
	}
}

func TestReplaceRBACV1SubjectRoles(t *testing.T) {
	repository := &testRBACRepo{}
	roleID := uuid.New()
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	request := httptest.NewRequest(http.MethodPut, "/v1/rbac-v1/subject-roles?app_key=admin", strings.NewReader(`{"subject":"nexus-user-1","domain":"global","role_ids":["`+roleID.String()+`"]}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if repository.scope != "admin" || repository.subject != "nexus-user-1" || repository.domain != "global" || len(repository.roleIDs) != 1 || repository.roleIDs[0] != roleID {
		t.Fatalf("assignment = %#v", repository)
	}
}

func TestRoleUpdateAndDeleteEndpointsAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, "/v1/roles/role-1", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired role %s status = %d body=%s", method, response.Code, response.Body.String())
		}
	}
}

func TestMenuUpdateAndDeleteEndpointsAreRetired(t *testing.T) {
	server := NewServer(NewHandler(nil))
	for _, path := range []string{"/v1/menus/123", "/v1/menus/not-a-uuid"} {
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
	request := httptest.NewRequest(http.MethodPost, "/v1/service-resources", strings.NewReader(`{"key":"orders","display_name":"Orders"}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.actors) != 1 || repository.actors[0].ID != "development" || repository.actors[0].Name != "开发模式" {
		t.Fatalf("audit actor = %#v", repository.actors)
	}
}

type testSwaggerImportRepo struct {
	*testRBACRepo
	services []*biz.ServiceResource
	saved    []biz.RBACEndpoint
}

func (r *testSwaggerImportRepo) ListEndpoints(context.Context, string) ([]biz.RBACEndpoint, error) {
	return nil, nil
}
func (r *testSwaggerImportRepo) SaveEndpoint(_ context.Context, scope string, value biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	value.ServiceResource = scope
	r.saved = append(r.saved, value)
	return value, nil
}
func (r *testSwaggerImportRepo) ListApplications(context.Context) ([]biz.Application, error) {
	return nil, nil
}
func (r *testSwaggerImportRepo) SaveApplication(_ context.Context, value biz.Application) (biz.Application, error) {
	return value, nil
}
func (r *testSwaggerImportRepo) ListApplicationServices(context.Context, string) ([]*biz.ServiceResource, error) {
	return r.services, nil
}
func (r *testSwaggerImportRepo) ReplaceApplicationServices(context.Context, string, []string) error {
	return nil
}

func TestImportRBACV1SwaggerFromDocument(t *testing.T) {
	repository := &testSwaggerImportRepo{testRBACRepo: &testRBACRepo{}, services: []*biz.ServiceResource{{Key: "orders", IsActive: true}}}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	body := `{"swagger_document":"openapi: 3.0.3\ninfo:\n  title: Orders API\npaths:\n  \"/orders\":\n    get:\n      summary: List orders"}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/rbac-v1/endpoints/import-swagger?app_key=admin&service_resource=orders", strings.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("import document status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.saved) != 1 || repository.saved[0].Method != "GET" || repository.saved[0].PathTemplate != "/orders" || repository.saved[0].Summary != "List orders" {
		t.Fatalf("imported endpoints = %#v", repository.saved)
	}
}

func TestImportRBACV1SwaggerRequiresSource(t *testing.T) {
	repository := &testSwaggerImportRepo{testRBACRepo: &testRBACRepo{}, services: []*biz.ServiceResource{{Key: "orders", IsActive: true}}}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/rbac-v1/endpoints/import-swagger?app_key=admin&service_resource=orders", strings.NewReader(`{}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing source status = %d body=%s", response.Code, response.Body.String())
	}
}
