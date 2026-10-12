package biz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type serviceResourceRoundTripper func(*http.Request) (*http.Response, error)

func (f serviceResourceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNexusAuthServiceResourceCatalogList(t *testing.T) {
	client := &http.Client{Transport: serviceResourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://nexus.example/openapi/v1/service-resources" {
			t.Fatalf("request URL = %q", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer directory-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`[{"id":"resource-id","name":"orders-api","displayName":"订单服务","audience":"orders","description":"订单资源","isActive":true,"createdAt":"2026-09-04T10:00:00Z"}]`)),
		}, nil
	})}
	catalog, err := NewNexusAuthServiceResourceCatalogWithClient("https://nexus.example", "directory-token", time.Second, client)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Key != "orders-api" || resources[0].Name != "订单服务" || !resources[0].IsActive {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Source != ServiceResourceSourceNexusAuth {
		t.Fatalf("resource source = %q", resources[0].Source)
	}
}

type memoryServiceResourceRepository struct {
	values     map[string]*ServiceResource
	readCalls  int
	writeCalls int
}

func (r *memoryServiceResourceRepository) Create(_ context.Context, resource *ServiceResource) (*ServiceResource, error) {
	r.writeCalls++
	if _, exists := r.values[resource.Key]; exists {
		return nil, ErrAlreadyExists
	}
	value := cloneServiceResource(resource)
	value.Source = ServiceResourceSourceLocal
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	r.values[value.Key] = value
	return cloneServiceResource(value), nil
}

func (r *memoryServiceResourceRepository) Get(_ context.Context, key string) (*ServiceResource, error) {
	r.readCalls++
	value, exists := r.values[key]
	if !exists || value == nil {
		return nil, ErrNotFound
	}
	return cloneServiceResource(value), nil
}

func (r *memoryServiceResourceRepository) List(_ context.Context) ([]*ServiceResource, error) {
	r.readCalls++
	values := make([]*ServiceResource, 0, len(r.values))
	for _, value := range r.values {
		if value != nil {
			values = append(values, cloneServiceResource(value))
		}
	}
	return values, nil
}

func (r *memoryServiceResourceRepository) GetBySource(_ context.Context, key, source string) (*ServiceResource, error) {
	r.readCalls++
	value, exists := r.values[key]
	if !exists || value == nil || value.Source != source {
		return nil, ErrNotFound
	}
	return cloneServiceResource(value), nil
}

func (r *memoryServiceResourceRepository) ListBySource(_ context.Context, source string) ([]*ServiceResource, error) {
	r.readCalls++
	values := make([]*ServiceResource, 0, len(r.values))
	for _, value := range r.values {
		if value != nil && value.Source == source {
			values = append(values, cloneServiceResource(value))
		}
	}
	return values, nil
}

func (r *memoryServiceResourceRepository) Update(_ context.Context, resource *ServiceResource) (*ServiceResource, error) {
	r.writeCalls++
	value, exists := r.values[resource.Key]
	if !exists {
		return nil, ErrNotFound
	}
	updated := cloneServiceResource(resource)
	updated.Source = value.Source
	updated.ID = value.ID
	updated.CreatedAt = value.CreatedAt
	r.values[resource.Key] = updated
	return cloneServiceResource(updated), nil
}

func (r *memoryServiceResourceRepository) SoftDelete(_ context.Context, key string) error {
	r.writeCalls++
	value, exists := r.values[key]
	if !exists {
		return ErrNotFound
	}
	delete(r.values, key)
	value.IsActive = false
	return nil
}

func (r *memoryServiceResourceRepository) UpsertNexusAuth(_ context.Context, resource *ServiceResource) (*ServiceResource, error) {
	r.writeCalls++
	if current, exists := r.values[resource.Key]; exists && current.Source == ServiceResourceSourceLocal {
		return cloneServiceResource(current), nil
	}
	value := cloneServiceResource(resource)
	value.Source = ServiceResourceSourceNexusAuth
	r.values[value.Key] = value
	return cloneServiceResource(value), nil
}

type memoryRemoteServiceResourceCatalog struct {
	values    []*ServiceResource
	listErr   error
	getErr    error
	called    bool
	getCalled bool
}

func (r *memoryRemoteServiceResourceCatalog) List(context.Context) ([]*ServiceResource, error) {
	r.called = true
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.values, nil
}

func (r *memoryRemoteServiceResourceCatalog) Get(_ context.Context, key string) (*ServiceResource, error) {
	r.getCalled = true
	if r.getErr != nil {
		return nil, r.getErr
	}
	for _, value := range r.values {
		if value != nil && value.Key == key {
			return value, nil
		}
	}
	return nil, ErrNotFound
}

func cloneServiceResource(value *ServiceResource) *ServiceResource {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func TestServiceResourceCatalogLocalReadsRepositoryOnly(t *testing.T) {
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{
		"local":  {Key: "local", Name: "本地服务", DisplayName: "本地服务", Source: ServiceResourceSourceLocal, IsActive: true},
		"remote": {Key: "remote", Name: "远程服务", DisplayName: "远程服务", Source: ServiceResourceSourceNexusAuth, IsActive: true},
	}}
	remote := &memoryRemoteServiceResourceCatalog{}
	catalog, err := NewServiceResourceCatalog(repository, remote, "local")
	if err != nil {
		t.Fatal(err)
	}
	values, err := catalog.List(context.Background())
	if err != nil || len(values) != 1 || values[0].Key != "local" {
		t.Fatalf("local values = %#v, %v", values, err)
	}
	if _, err := catalog.Get(context.Background(), "remote"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("local catalog returned remote resource: %v", err)
	}
	if remote.called {
		t.Fatal("local catalog called remote directory")
	}
}

func TestServiceResourceCatalogNexusAuthReadsRemoteOnly(t *testing.T) {
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{
		"local": {Key: "local", Name: "local name", Source: ServiceResourceSourceLocal, DisplayName: "local name", IsActive: true},
	}}
	remote := &memoryRemoteServiceResourceCatalog{values: []*ServiceResource{
		{Key: "remote-disabled", Name: "remote disabled", DisplayName: "remote disabled", Source: ServiceResourceSourceNexusAuth, IsActive: false},
		{Key: "remote", Name: "remote", DisplayName: "remote", IsActive: true},
	}}
	catalog, err := NewServiceResourceCatalog(repository, remote, ServiceResourceSourceNexusAuth)
	if err != nil {
		t.Fatal(err)
	}
	values, err := catalog.List(context.Background())
	if err != nil || len(values) != 2 {
		t.Fatalf("remote values = %#v, %v", values, err)
	}
	if values[0].Source != ServiceResourceSourceNexusAuth || values[1].Source != ServiceResourceSourceNexusAuth {
		t.Fatalf("remote resource sources = %#v", values)
	}
	remoteValue, err := catalog.Get(context.Background(), "remote")
	if err != nil {
		t.Fatal(err)
	}
	if remoteValue.DisplayName != "remote" || remoteValue.Source != ServiceResourceSourceNexusAuth || !remoteValue.IsActive {
		t.Fatalf("remote record = %#v", remoteValue)
	}
	if !remote.called {
		t.Fatal("nexusauth catalog did not call remote directory")
	}
	if !remote.getCalled {
		t.Fatal("nexusauth catalog did not call remote get")
	}
	if repository.readCalls != 0 || repository.writeCalls != 0 {
		t.Fatalf("nexusauth repository calls = reads:%d writes:%d", repository.readCalls, repository.writeCalls)
	}
	if value := repository.values["local"]; value.DisplayName != "local name" || value.Source != ServiceResourceSourceLocal {
		t.Fatalf("local repository record changed = %#v", value)
	}

	remote.values[1].DisplayName = "changed remotely"
	remoteValue, err = catalog.Get(context.Background(), "remote")
	if err != nil {
		t.Fatal(err)
	}
	if remoteValue.DisplayName != "changed remotely" {
		t.Fatalf("remote changes were not returned directly = %#v", remoteValue)
	}
}

func TestServiceResourceCatalogNexusAuthPropagatesRemoteErrors(t *testing.T) {
	listErr := errors.New("remote list failed")
	getErr := errors.New("remote get failed")
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{}}
	remote := &memoryRemoteServiceResourceCatalog{listErr: listErr, getErr: getErr}
	catalog, err := NewServiceResourceCatalog(repository, remote, ServiceResourceSourceNexusAuth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.List(context.Background()); err != listErr {
		t.Fatalf("remote list error = %v", err)
	}
	if _, err := catalog.Get(context.Background(), "remote"); err != getErr {
		t.Fatalf("remote get error = %v", err)
	}
	if repository.readCalls != 0 || repository.writeCalls != 0 {
		t.Fatalf("nexusauth repository calls after remote errors = reads:%d writes:%d", repository.readCalls, repository.writeCalls)
	}
}

func TestServiceResourceWritableBySource(t *testing.T) {
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{}}
	local, err := NewServiceResourceCatalog(repository, nil, "LOCAL")
	if err != nil {
		t.Fatal(err)
	}
	if !local.Writable() {
		t.Fatal("local service resource should be writable")
	}
	remote, err := NewServiceResourceCatalog(repository, &memoryRemoteServiceResourceCatalog{}, "NEXUSAUTH")
	if err != nil {
		t.Fatal(err)
	}
	if remote.Writable() {
		t.Fatal("nexusauth service resource should be read-only")
	}
}

func TestServiceResourceServiceRejectsNexusAuthMutation(t *testing.T) {
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{
		"remote": {Key: "remote", Name: "remote", Source: ServiceResourceSourceNexusAuth, IsActive: true},
	}}
	service, err := NewServiceResourceCatalog(repository, nil, ServiceResourceSourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), &ServiceResource{Key: "remote", DisplayName: "changed"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("remote update error = %v", err)
	}
	if err := service.Delete(context.Background(), "remote"); !errors.Is(err, ErrConflict) {
		t.Fatalf("remote delete error = %v", err)
	}
}

func TestServiceResourceJSONIncludesSource(t *testing.T) {
	value := ServiceResource{Key: "orders", Name: "订单服务", DisplayName: "订单服务", Source: ServiceResourceSourceLocal, IsActive: true}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"source":"local"`) {
		t.Fatalf("json = %s", encoded)
	}
	if !strings.Contains(string(encoded), `"key":"orders"`) {
		t.Fatalf("json = %s", encoded)
	}
}

func TestServiceResourceNexusAuthModeRejectsAllWrites(t *testing.T) {
	repository := &memoryServiceResourceRepository{values: map[string]*ServiceResource{}}
	remote := &memoryRemoteServiceResourceCatalog{}
	service, err := NewServiceResourceCatalog(repository, remote, ServiceResourceSourceNexusAuth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), &ServiceResource{Key: "orders"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("nexusauth create error = %v", err)
	}
	if _, err := service.Update(context.Background(), &ServiceResource{Key: "orders"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("nexusauth update error = %v", err)
	}
	if err := service.Delete(context.Background(), "orders"); !errors.Is(err, ErrConflict) {
		t.Fatalf("nexusauth delete error = %v", err)
	}
}
