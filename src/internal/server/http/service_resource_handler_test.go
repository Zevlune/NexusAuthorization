package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luck/nexus-authorization/internal/biz"
)

type serviceResourceHandlerRepository struct {
	values  map[string]*biz.ServiceResource
	actors  []biz.AuditActor
	deleted []string
	writes  int
}

func (r *serviceResourceHandlerRepository) Create(ctx context.Context, resource *biz.ServiceResource) (*biz.ServiceResource, error) {
	r.recordActor(ctx)
	r.writes++
	if _, exists := r.values[resource.Key]; exists {
		return nil, biz.ErrAlreadyExists
	}
	value := cloneHandlerServiceResource(resource)
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Unix(0, 0).UTC()
	}
	r.values[value.Key] = value
	return cloneHandlerServiceResource(value), nil
}

func (r *serviceResourceHandlerRepository) Get(ctx context.Context, name string) (*biz.ServiceResource, error) {
	r.recordActor(ctx)
	value, exists := r.values[name]
	if !exists {
		return nil, biz.ErrNotFound
	}
	return cloneHandlerServiceResource(value), nil
}

func (r *serviceResourceHandlerRepository) List(ctx context.Context) ([]*biz.ServiceResource, error) {
	r.recordActor(ctx)
	values := make([]*biz.ServiceResource, 0, len(r.values))
	for _, value := range r.values {
		values = append(values, cloneHandlerServiceResource(value))
	}
	return values, nil
}

func (r *serviceResourceHandlerRepository) Update(ctx context.Context, resource *biz.ServiceResource) (*biz.ServiceResource, error) {
	r.recordActor(ctx)
	r.writes++
	if _, exists := r.values[resource.Key]; !exists {
		return nil, biz.ErrNotFound
	}
	value := cloneHandlerServiceResource(resource)
	r.values[value.Key] = value
	return cloneHandlerServiceResource(value), nil
}

func (r *serviceResourceHandlerRepository) SoftDelete(ctx context.Context, name string) error {
	r.recordActor(ctx)
	r.writes++
	if _, exists := r.values[name]; !exists {
		return biz.ErrNotFound
	}
	delete(r.values, name)
	r.deleted = append(r.deleted, name)
	return nil
}

func (r *serviceResourceHandlerRepository) UpsertNexusAuth(context.Context, *biz.ServiceResource) (*biz.ServiceResource, error) {
	return nil, errors.New("not used in HTTP handler test")
}

type serviceResourceHandlerRemoteCatalog struct {
	values    map[string]*biz.ServiceResource
	listCalls int
	getCalls  int
}

func (r *serviceResourceHandlerRemoteCatalog) List(context.Context) ([]*biz.ServiceResource, error) {
	r.listCalls++
	values := make([]*biz.ServiceResource, 0, len(r.values))
	for _, value := range r.values {
		values = append(values, cloneHandlerServiceResource(value))
	}
	return values, nil
}

func (r *serviceResourceHandlerRemoteCatalog) Get(_ context.Context, key string) (*biz.ServiceResource, error) {
	r.getCalls++
	value, exists := r.values[key]
	if !exists {
		return nil, biz.ErrNotFound
	}
	return cloneHandlerServiceResource(value), nil
}

func (r *serviceResourceHandlerRepository) recordActor(ctx context.Context) {
	r.actors = append(r.actors, biz.AuditActorFromContext(ctx))
}

func cloneHandlerServiceResource(value *biz.ServiceResource) *biz.ServiceResource {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func newServiceResourceHandlerServer(t *testing.T, repository *serviceResourceHandlerRepository) http.Handler {
	t.Helper()
	service, err := biz.NewServiceResourceCatalog(repository, nil, biz.ServiceResourceSourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(NewHandler(nil).
		WithServiceResourceCatalog(service).
		WithServiceResourceService(service))
}

func newNexusAuthServiceResourceHandlerServer(t *testing.T, repository *serviceResourceHandlerRepository, remote biz.ServiceResourceCatalog) http.Handler {
	t.Helper()
	service, err := biz.NewServiceResourceCatalog(repository, remote, biz.ServiceResourceSourceNexusAuth)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(NewHandler(nil).
		WithServiceResourceCatalog(service).
		WithServiceResourceService(service))
}

func TestServiceResourceCRUDHandlers(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)

	createResponse := httptest.NewRecorder()
	server.ServeHTTP(createResponse, newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"orders","display_name":"订单服务","audience":"orders-api","description":"订单权限","is_active":true}`))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	if !strings.Contains(createResponse.Body.String(), `"key":"orders"`) || !strings.Contains(createResponse.Body.String(), `"source":"local"`) {
		t.Fatalf("create body = %s", createResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/service-resources", nil))
	var listEnvelope struct {
		Result struct {
			Items    []*biz.ServiceResource `json:"items"`
			Writable bool                   `json:"writable"`
			Source   json.RawMessage        `json:"source"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatalf("list body = %s: %v", listResponse.Body.String(), err)
	}
	listBody := listEnvelope.Result
	if listResponse.Code != http.StatusOK || len(listBody.Items) != 1 || listBody.Items[0].Key != "orders" || !listBody.Writable {
		t.Fatalf("list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if listBody.Source != nil {
		t.Fatalf("list response unexpectedly contains top-level source: %s", listResponse.Body.String())
	}

	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/service-resources/orders", nil))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"display_name":"订单服务"`) {
		t.Fatalf("get status = %d body=%s", getResponse.Code, getResponse.Body.String())
	}

	updateResponse := httptest.NewRecorder()
	server.ServeHTTP(updateResponse, newJSONRequest(http.MethodPut, "/api/v1/service-resources/orders", `{"display_name":"订单中心","audience":"orders-web","description":"更新后的订单权限","is_active":false}`))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	updated := repository.values["orders"]
	if updated.DisplayName != "订单中心" || updated.Audience != "orders-web" || updated.Description != "更新后的订单权限" || updated.IsActive {
		t.Fatalf("updated resource = %#v", updated)
	}

	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/service-resources/orders", nil))
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if len(repository.deleted) != 1 || repository.deleted[0] != "orders" {
		t.Fatalf("deleted resources = %#v", repository.deleted)
	}
	if len(repository.actors) < 3 {
		t.Fatalf("audit actors = %#v", repository.actors)
	}
	for _, actor := range repository.actors {
		if actor.ID != "system" || actor.Name != "system" {
			t.Fatalf("audit actor = %#v", actor)
		}
	}
}

func TestCreateServiceResourceDefaultsToActive(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{}}
	server := newServiceResourceHandlerServer(t, repository)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/service-resources", `{"key":"billing","display_name":"账单服务"}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	if value := repository.values["billing"]; value == nil || !value.IsActive {
		t.Fatalf("created resource = %#v", value)
	}
}

func TestServiceResourceNexusAuthWriteProtection(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{
		"remote": {Key: "remote", Source: biz.ServiceResourceSourceNexusAuth, IsActive: true},
	}}
	server := newServiceResourceHandlerServer(t, repository)

	updateResponse := httptest.NewRecorder()
	server.ServeHTTP(updateResponse, newJSONRequest(http.MethodPut, "/api/v1/service-resources/remote", `{"display_name":"禁止修改","audience":"remote","description":"","is_active":true}`))
	if updateResponse.Code != http.StatusConflict {
		t.Fatalf("remote update status = %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}

	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/service-resources/remote", nil))
	if deleteResponse.Code != http.StatusConflict {
		t.Fatalf("remote delete status = %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if !strings.Contains(updateResponse.Body.String(), `"errorCode":"CONFLICT"`) || !strings.Contains(deleteResponse.Body.String(), `"errorCode":"CONFLICT"`) {
		t.Fatalf("remote write responses = update:%s delete:%s", updateResponse.Body.String(), deleteResponse.Body.String())
	}
}

func TestServiceResourceNexusAuthConfiguredModeHandlers(t *testing.T) {
	repository := &serviceResourceHandlerRepository{values: map[string]*biz.ServiceResource{
		"local": {Key: "local", Source: biz.ServiceResourceSourceLocal, DisplayName: "本地服务", IsActive: true},
	}}
	remote := &serviceResourceHandlerRemoteCatalog{values: map[string]*biz.ServiceResource{
		"remote": {Key: "remote", Name: "远程服务", DisplayName: "远程服务", Source: biz.ServiceResourceSourceNexusAuth, IsActive: true},
	}}
	server := newNexusAuthServiceResourceHandlerServer(t, repository, remote)

	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/service-resources", nil))
	var listEnvelope struct {
		Result struct {
			Items    []*biz.ServiceResource `json:"items"`
			Writable bool                   `json:"writable"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatalf("list body = %s: %v", listResponse.Body.String(), err)
	}
	if listResponse.Code != http.StatusOK || len(listEnvelope.Result.Items) != 1 || listEnvelope.Result.Items[0].Key != "remote" || listEnvelope.Result.Writable {
		t.Fatalf("nexusauth list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if remote.listCalls != 1 {
		t.Fatalf("remote list calls = %d", remote.listCalls)
	}

	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/service-resources/remote", nil))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"display_name":"远程服务"`) {
		t.Fatalf("nexusauth get status = %d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if remote.getCalls != 1 {
		t.Fatalf("remote get calls = %d", remote.getCalls)
	}

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/v1/service-resources", body: `{"key":"local","display_name":"禁止创建"}`},
		{method: http.MethodPut, path: "/api/v1/service-resources/remote", body: `{"display_name":"禁止修改","is_active":true}`},
		{method: http.MethodDelete, path: "/api/v1/service-resources/remote"},
		{method: http.MethodPut, path: "/api/v1/service-resources/local", body: `{"display_name":"禁止修改本地键","is_active":true}`},
		{method: http.MethodDelete, path: "/api/v1/service-resources/local"},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(request.method, request.path, request.body))
		if response.Code != http.StatusConflict {
			t.Fatalf("nexusauth %s %s status = %d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	if repository.writes != 0 {
		t.Fatalf("nexusauth repository writes = %d", repository.writes)
	}
}
