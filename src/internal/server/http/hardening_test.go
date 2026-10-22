package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

// rbacRepoWithErrors makes SaveRole fail and records role-subject updates.
type rbacRepoWithErrors struct {
	*testRBACRepo
	saveRoleErr  error
	roleSubjects []string
}

func (r *rbacRepoWithErrors) SaveRole(context.Context, string, biz.RBACRole) (biz.RBACRole, error) {
	return biz.RBACRole{}, r.saveRoleErr
}

func (r *rbacRepoWithErrors) UpdateRoleSubjects(_ context.Context, app string, roleID uuid.UUID, domain string, add, remove []string) error {
	r.roleSubjects = append(r.roleSubjects, fmt.Sprintf("%s|%s|%s|%v|%v", app, roleID, domain, add, remove))
	return nil
}

func decodeAPIResponse(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %s", response.Body.String())
	}
	return body
}

func TestJSONBodiesRequireJSONContentType(t *testing.T) {
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(&testRBACRepo{})))
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=admin", strings.NewReader(`{"code":"a","name":"A"}`))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnsupportedMediaType || decodeAPIResponse(t, response)["errorCode"] != "UNSUPPORTED_MEDIA_TYPE" {
			t.Fatalf("content type %q: status = %d body=%s", contentType, response.Code, response.Body.String())
		}
	}
	// Parameters on the media type are fine.
	request := newJSONRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=admin", `{"code":"a","name":"A"}`)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("json with charset: status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestRequestBodySizeIsLimited(t *testing.T) {
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(&testRBACRepo{})))
	body := `{"code":"a","name":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=admin", body))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "exceeds") {
		t.Fatalf("oversized body: status = %d body=%.200s", response.Code, response.Body.String())
	}
}

func TestUnexpectedErrorsDoNotLeakDetails(t *testing.T) {
	repository := &rbacRepoWithErrors{testRBACRepo: &testRBACRepo{}, saveRoleErr: errors.New(`pq: relation "secret_table" does not exist`)}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=admin", `{"code":"a","name":"A"}`))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret_table") {
		t.Fatalf("internal error leaked: status = %d body=%s", response.Code, response.Body.String())
	}
	if body := decodeAPIResponse(t, response); body["errorCode"] != "INTERNAL_ERROR" || body["errorMessage"] != "internal server error" {
		t.Fatalf("body = %v", body)
	}
}

func TestPublishPendingIsReportedAsServiceUnavailable(t *testing.T) {
	repository := &rbacRepoWithErrors{testRBACRepo: &testRBACRepo{}, saveRoleErr: fmt.Errorf("%w: redis: connection refused", biz.ErrPublishPending)}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=admin", `{"code":"a","name":"A"}`))
	body := decodeAPIResponse(t, response)
	if response.Code != http.StatusServiceUnavailable || body["errorCode"] != "PUBLISH_PENDING" || strings.Contains(response.Body.String(), "connection refused") {
		t.Fatalf("publish pending: status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestAuthorizeServiceCredential(t *testing.T) {
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(&testRBACRepo{})).WithAuthorizationServiceCredential("service-secret"))
	body := `{"appKey":"admin","serviceResource":"orders","subject":"u","method":"GET","path":"/orders"}`
	for name, header := range map[string]string{"missing": "", "wrong": "Bearer other", "basic": "Basic service-secret"} {
		request := newJSONRequest(http.MethodPost, "/api/v1/authorize", body)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || decodeAPIResponse(t, response)["errorCode"] != "UNAUTHENTICATED" || response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s credential: status = %d body=%s", name, response.Code, response.Body.String())
		}
	}
	// The auth scheme is case-insensitive.
	request := newJSONRequest(http.MethodPost, "/api/v1/authorize", body)
	request.Header.Set("Authorization", "bearer service-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("lower-case bearer: status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestUpdateRoleSubjectsRoute(t *testing.T) {
	repository := &rbacRepoWithErrors{testRBACRepo: &testRBACRepo{}}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))
	roleID := uuid.New()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPut, "/api/v1/rbac-v1/roles/"+roleID.String()+"/subjects?app_key=admin", `{"add":["alice","bob"],"remove":["carol"]}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	want := fmt.Sprintf("admin|%s||[alice bob]|[carol]", roleID)
	if len(repository.roleSubjects) != 1 || repository.roleSubjects[0] != want {
		t.Fatalf("calls = %v, want %s", repository.roleSubjects, want)
	}
	for name, target := range map[string]string{
		"bad role id":     "/api/v1/rbac-v1/roles/not-a-uuid/subjects?app_key=admin",
		"missing app_key": "/api/v1/rbac-v1/roles/" + roleID.String() + "/subjects",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPut, target, `{"add":["alice"]}`))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", name, response.Code)
		}
	}
}

func TestUpdateServiceResourceKeepsStatusWhenOmitted(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)
	create := httptest.NewRecorder()
	server.ServeHTTP(create, newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"billing","display_name":"账单服务"}`))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	update := httptest.NewRecorder()
	server.ServeHTTP(update, newJSONRequest(http.MethodPut, "/api/v1/service-resources/billing", `{"display_name":"账单中心"}`))
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", update.Code, update.Body.String())
	}
	if value := repository.values["billing"]; value == nil || !value.IsActive || value.DisplayName != "账单中心" {
		t.Fatalf("omitting is_active changed the status: %#v", value)
	}
}

func TestNewKeysMustBeSinglePathSegments(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)
	for _, key := range []string{"orders/v2", "orders v2", "orders%2F", "orders?x"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"`+key+`","display_name":"Orders"}`))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("key %q: status = %d body=%s", key, response.Code, response.Body.String())
		}
	}
}
