package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type memoryAPIEndpointRepository struct {
	values map[uuid.UUID]*APIEndpoint
}

func newMemoryAPIEndpointRepository() *memoryAPIEndpointRepository {
	return &memoryAPIEndpointRepository{values: map[uuid.UUID]*APIEndpoint{}}
}

func (r *memoryAPIEndpointRepository) Create(_ context.Context, value *APIEndpoint) (*APIEndpoint, error) {
	if value.ID == uuid.Nil {
		value.ID = uuid.New()
	}
	for _, existing := range r.values {
		if !existing.IsDeleted && existing.ServiceResource == value.ServiceResource && existing.Method == value.Method && existing.PathTemplate == value.PathTemplate {
			return nil, ErrAlreadyExists
		}
	}
	r.values[value.ID] = value
	return value, nil
}

func (r *memoryAPIEndpointRepository) Get(_ context.Context, id uuid.UUID) (*APIEndpoint, error) {
	value, ok := r.values[id]
	if !ok || value.IsDeleted {
		return nil, ErrNotFound
	}
	return value, nil
}

func (r *memoryAPIEndpointRepository) ListByServiceResource(_ context.Context, serviceResource string) ([]*APIEndpoint, error) {
	values := make([]*APIEndpoint, 0)
	for _, value := range r.values {
		if value.ServiceResource == serviceResource && !value.IsDeleted {
			values = append(values, value)
		}
	}
	return values, nil
}

func (r *memoryAPIEndpointRepository) Update(_ context.Context, value *APIEndpoint) (*APIEndpoint, error) {
	if _, ok := r.values[value.ID]; !ok {
		return nil, ErrNotFound
	}
	r.values[value.ID] = value
	return value, nil
}

func (r *memoryAPIEndpointRepository) SoftDelete(_ context.Context, id uuid.UUID) error {
	value, ok := r.values[id]
	if !ok || value.IsDeleted {
		return ErrNotFound
	}
	value.IsDeleted = true
	value.Enabled = false
	return nil
}

type endpointServiceResourceCatalog struct {
	value *ServiceResource
}

func (c *endpointServiceResourceCatalog) List(context.Context) ([]*ServiceResource, error) {
	return []*ServiceResource{c.value}, nil
}

func (c *endpointServiceResourceCatalog) Get(context.Context, string) (*ServiceResource, error) {
	if c.value == nil {
		return nil, ErrNotFound
	}
	return c.value, nil
}

func TestAPIEndpointServiceCRUDValidatesScopeAndNormalizesMethod(t *testing.T) {
	repository := newMemoryAPIEndpointRepository()
	service := NewAPIEndpointService(repository).WithServiceResourceCatalog(&endpointServiceResourceCatalog{value: &ServiceResource{Name: "orders", IsActive: true}})
	ctx := WithAuditActor(context.Background(), AuditActor{ID: "operator", Name: "Operator"})
	created, err := service.CreateAPIEndpoint(ctx, &APIEndpoint{ServiceResource: " orders ", Controller: "Orders", Method: "get", PathTemplate: "/v1/orders", Summary: "List orders", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Method != "GET" || created.ServiceResource != "orders" || created.CreatedByID != "operator" {
		t.Fatalf("created endpoint = %#v", created)
	}
	updated, err := service.UpdateAPIEndpoint(ctx, &APIEndpoint{ID: created.ID, Controller: "Orders", Method: "post", PathTemplate: "/v1/orders", Summary: "Create orders", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Method != "POST" || updated.Enabled || updated.CreatedByID != "operator" {
		t.Fatalf("updated endpoint = %#v", updated)
	}
	if err := service.DeleteAPIEndpoint(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAPIEndpoint(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted endpoint error = %v", err)
	}
}

func TestAPIEndpointServiceRejectsDisabledServiceResource(t *testing.T) {
	service := NewAPIEndpointService(newMemoryAPIEndpointRepository()).WithServiceResourceCatalog(&endpointServiceResourceCatalog{value: &ServiceResource{Name: "orders", IsActive: false}})
	_, err := service.CreateAPIEndpoint(context.Background(), &APIEndpoint{ServiceResource: "orders", Controller: "Orders", Method: "GET", PathTemplate: "/v1/orders"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled resource error = %v", err)
	}
}
