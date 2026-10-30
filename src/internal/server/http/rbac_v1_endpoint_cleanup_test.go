package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

type linkedEndpointHandlerRepository struct {
	*testRBACRepo
	endpoints []biz.RBACEndpoint
	deleted   []uuid.UUID
}

func (r *linkedEndpointHandlerRepository) RequireLinkedApplicationService(_ context.Context, app, service string) error {
	if app != "admin" || service != "orders" {
		return biz.ErrInvalidArgument
	}
	return nil
}

func (r *linkedEndpointHandlerRepository) ListApplicationEndpoints(context.Context, string, string, int, int) ([]biz.RBACEndpoint, int, error) {
	return r.endpoints, len(r.endpoints), nil
}

func (*linkedEndpointHandlerRepository) ListApplicationMenus(context.Context, string) ([]biz.ApplicationMenu, error) {
	return nil, nil
}

func (*linkedEndpointHandlerRepository) SaveApplicationMenu(context.Context, string, biz.ApplicationMenu) (biz.ApplicationMenu, error) {
	return biz.ApplicationMenu{}, nil
}

func (*linkedEndpointHandlerRepository) DeleteApplicationMenu(context.Context, string, uuid.UUID) error {
	return nil
}

func (r *linkedEndpointHandlerRepository) DeleteEndpoint(_ context.Context, service string, id uuid.UUID) error {
	if service != "orders" {
		return biz.ErrNotFound
	}
	r.deleted = append(r.deleted, id)
	return nil
}

func TestRBACV1EndpointListAndDeleteUsePersistedLink(t *testing.T) {
	endpointID := uuid.New()
	repository := &linkedEndpointHandlerRepository{
		testRBACRepo: &testRBACRepo{},
		endpoints: []biz.RBACEndpoint{{
			ID:              endpointID,
			ServiceResource: "orders",
			EndpointKey:     "orders.get",
			Method:          http.MethodGet,
			PathTemplate:    "/orders",
		}},
	}
	server := NewServer(NewHandler(nil).WithRBACV1Service(biz.NewRBACV1Service(repository)))

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/rbac-v1/endpoints?app_key=admin&service_resource=orders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list linked endpoints: status = %d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/rbac-v1/endpoints/"+endpointID.String()+"?app_key=admin&service_resource=orders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("delete linked endpoint: status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.deleted) != 1 || repository.deleted[0] != endpointID {
		t.Fatalf("deleted endpoints = %v", repository.deleted)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/rbac-v1/endpoints/"+endpointID.String()+"?app_key=admin&service_resource=unlinked", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("delete unlinked endpoint: status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.deleted) != 1 {
		t.Fatalf("unlinked delete reached repository: %v", repository.deleted)
	}
}
