package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

type subjectMenusHTTPRepository struct {
	policy biz.CurrentPolicy
}

func (*subjectMenusHTTPRepository) ListEndpoints(context.Context, string) ([]biz.RBACEndpoint, error) {
	return nil, nil
}
func (*subjectMenusHTTPRepository) SaveEndpoint(context.Context, string, biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	return biz.RBACEndpoint{}, nil
}
func (*subjectMenusHTTPRepository) DeleteEndpoint(context.Context, string, uuid.UUID) error {
	return nil
}
func (*subjectMenusHTTPRepository) ListRoles(context.Context, string) ([]biz.RBACRole, error) {
	return nil, nil
}
func (*subjectMenusHTTPRepository) SaveRole(context.Context, string, biz.RBACRole) (biz.RBACRole, error) {
	return biz.RBACRole{}, nil
}
func (*subjectMenusHTTPRepository) DeleteRole(context.Context, string, uuid.UUID) error {
	return nil
}
func (*subjectMenusHTTPRepository) ListSubjectRoleBindings(context.Context, string) ([]biz.SubjectRoleBinding, error) {
	return nil, nil
}
func (*subjectMenusHTTPRepository) ReplaceSubjectRoles(context.Context, string, string, string, []uuid.UUID) error {
	return nil
}
func (r *subjectMenusHTTPRepository) GetPolicyMetadata(context.Context, string) (biz.CurrentPolicy, error) {
	return r.policy, nil
}
func (r *subjectMenusHTTPRepository) GetCurrentPolicy(context.Context, biz.CurrentPolicy) (biz.CurrentPolicy, error) {
	return r.policy, nil
}
func (r *subjectMenusHTTPRepository) GetMenuPolicy(context.Context, string) (biz.CurrentPolicy, error) {
	return r.policy, nil
}

func newSubjectMenusHTTPFixture(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	menuID, childID, roleID, appID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	policy := biz.CurrentPolicy{Data: map[string]any{
		"menus": []any{
			map[string]any{"id": menuID.String(), "application_id": appID.String(), "code": "orders", "name": "Orders", "menu_type": "menu", "sort_order": 1, "enabled": true},
			map[string]any{"id": childID.String(), "application_id": appID.String(), "parent_id": menuID.String(), "code": "create", "name": "Create", "menu_type": "button", "sort_order": 2, "enabled": true},
		},
		"roles": []any{
			map[string]any{"id": roleID.String(), "enabled": true, "menu_ids": []any{childID.String()}},
		},
		"subject_roles": []any{
			map[string]any{"subject": "user/one", "domain": "tenant a", "role_id": roleID.String()},
		},
	}}
	handler := NewHandler(nil).
		WithAuthorizationServiceCredential("service-secret").
		WithRBACV1Service(biz.NewRBACV1Service(&subjectMenusHTTPRepository{policy: policy}))
	managementAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer management-token" {
				next.ServeHTTP(w, r)
				return
			}
			if cookie, err := r.Cookie("nexusauthorization_session"); err == nil && cookie.Value == "management-session" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "management authentication required", http.StatusUnauthorized)
		})
	}
	return NewServer(handler, managementAuth), menuID.String(), childID.String()
}

func decodeMenuResult(t *testing.T, response *httptest.ResponseRecorder) []biz.ApplicationMenu {
	t.Helper()
	var body struct {
		Success bool `json:"success"`
		Result  struct {
			Items []biz.ApplicationMenu `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
	if !body.Success {
		t.Fatalf("response was not successful: %s", response.Body.String())
	}
	return body.Result.Items
}

func TestSubjectMenusServiceRouteUsesOnlyServiceCredential(t *testing.T) {
	server, menuID, childID := newSubjectMenusHTTPFixture(t)
	target := "/api/v1/subject-menus?app_key=books&subject=user%2Fone&domain=tenant%20a"

	for _, tc := range []struct {
		name       string
		authorize  string
		cookie     *http.Cookie
		wantStatus int
	}{
		{name: "service credential", authorize: "Bearer service-secret", cookie: &http.Cookie{Name: "nexusauthorization_session", Value: "management-session"}, wantStatus: http.StatusOK},
		{name: "missing credential", cookie: &http.Cookie{Name: "nexusauthorization_session", Value: "management-session"}, wantStatus: http.StatusUnauthorized},
		{name: "management bearer", authorize: "Bearer management-token", cookie: &http.Cookie{Name: "nexusauthorization_session", Value: "management-session"}, wantStatus: http.StatusUnauthorized},
		{name: "wrong service credential", authorize: "Bearer wrong", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Header.Set("Authorization", tc.authorize)
			if tc.cookie != nil {
				request.AddCookie(tc.cookie)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			if tc.wantStatus == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("service authentication challenge is missing")
			}
		})
	}

	serviceRequest := httptest.NewRequest(http.MethodGet, target, nil)
	serviceRequest.Header.Set("Authorization", "Bearer service-secret")
	serviceResponse := httptest.NewRecorder()
	server.ServeHTTP(serviceResponse, serviceRequest)
	items := decodeMenuResult(t, serviceResponse)
	if len(items) != 2 || items[0].ID.String() != menuID || items[1].ID.String() != childID {
		t.Fatalf("service menus = %+v", items)
	}

	managementRequest := httptest.NewRequest(http.MethodGet, "/api/v1/rbac-v1/subject-menus?app_key=books&subject=user%2Fone&domain=tenant%20a", nil)
	managementRequest.Header.Set("Authorization", "Bearer management-token")
	managementResponse := httptest.NewRecorder()
	server.ServeHTTP(managementResponse, managementRequest)
	if managementResponse.Code != http.StatusOK {
		t.Fatalf("management route status = %d body=%s", managementResponse.Code, managementResponse.Body.String())
	}
	if managementItems := decodeMenuResult(t, managementResponse); !reflect.DeepEqual(items, managementItems) {
		t.Fatalf("service menus %+v differ from management menus %+v", items, managementItems)
	}
}

func TestSubjectMenusManagementRouteRemainsProtected(t *testing.T) {
	server, _, _ := newSubjectMenusHTTPFixture(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/rbac-v1/subject-menus?app_key=books&subject=user%2Fone", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("management route status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestSubjectMenusServiceRouteValidatesRequiredQuery(t *testing.T) {
	server, _, _ := newSubjectMenusHTTPFixture(t)
	for _, target := range []string{
		"/api/v1/subject-menus?subject=user%2Fone",
		"/api/v1/subject-menus?app_key=books",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer service-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestSubjectMenusServiceRouteIsDisabledWithoutConfiguredCredential(t *testing.T) {
	allowManagement := func(next http.Handler) http.Handler { return next }
	server := NewServer(NewHandler(nil), allowManagement)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/subject-menus?app_key=books&subject=user/one", nil)
	request.Header.Set("Authorization", "Bearer arbitrary-service-token")
	request.AddCookie(&http.Cookie{Name: "nexusauthorization_session", Value: "management-session"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("status = %d challenge=%q body=%s", response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
	}
}

func TestOpenAPIDocumentsTrustedSubjectMenus(t *testing.T) {
	document := openAPIDocument()
	paths := document["paths"].(map[string]any)
	serviceOperation := paths["/api/v1/subject-menus"].(map[string]any)["get"].(map[string]any)
	security := serviceOperation["security"].([]map[string][]string)
	if !reflect.DeepEqual(security, []map[string][]string{{"serviceCredential": {}}}) {
		t.Fatalf("service menu security = %#v", security)
	}
	parameters := serviceOperation["parameters"].([]map[string]any)
	required := map[string]bool{"app_key": true, "subject": true, "domain": false}
	for _, parameter := range parameters {
		name, _ := parameter["name"].(string)
		if want, ok := required[name]; ok {
			if got, _ := parameter["required"].(bool); got != want {
				t.Fatalf("%s required = %v, want %v", name, got, want)
			}
			delete(required, name)
		}
	}
	if len(required) != 0 {
		t.Fatalf("missing OpenAPI parameters: %#v", required)
	}

	management := paths["/api/v1/rbac-v1/subject-menus"].(map[string]any)["get"].(map[string]any)
	if got := management["security"].([]map[string][]string); !reflect.DeepEqual(got, []map[string][]string{{"cookieAuth": {}}}) {
		t.Fatalf("management menu security = %#v", got)
	}
}
