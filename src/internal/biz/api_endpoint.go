package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	apiEndpointControllerMaxLength = 200
	apiEndpointPathMaxLength       = 500
)

// APIEndpoint 表示服务资源作用域内的 HTTP API 端点及其审计信息。
type APIEndpoint struct {
	BaseFields
	ID              uuid.UUID `json:"id"`
	ServiceResource string    `json:"service_resource"`
	Controller      string    `json:"controller"`
	Method          string    `json:"method"`
	PathTemplate    string    `json:"path_template"`
	Summary         string    `json:"summary"`
	Enabled         bool      `json:"enabled"`
}

// APIEndpointRepository 定义 API 端点的持久化操作。
type APIEndpointRepository interface {
	Create(context.Context, *APIEndpoint) (*APIEndpoint, error)
	Get(context.Context, uuid.UUID) (*APIEndpoint, error)
	ListByServiceResource(context.Context, string) ([]*APIEndpoint, error)
	Update(context.Context, *APIEndpoint) (*APIEndpoint, error)
	SoftDelete(context.Context, uuid.UUID) error
}

// APIEndpointService 提供 API 端点 CRUD 和 Swagger/OpenAPI 导入业务。
type APIEndpointService struct {
	repository       APIEndpointRepository
	serviceResources ServiceResourceCatalog
}

// NewAPIEndpointService 创建 API 端点业务服务。
func NewAPIEndpointService(repository APIEndpointRepository) *APIEndpointService {
	return &APIEndpointService{repository: repository}
}

// WithServiceResourceCatalog 配置服务资源目录作为端点作用域的权威来源。
func (s *APIEndpointService) WithServiceResourceCatalog(catalog ServiceResourceCatalog) *APIEndpointService {
	if s != nil {
		s.serviceResources = catalog
	}
	return s
}

// CreateAPIEndpoint 在指定服务资源下创建 API 端点。
func (s *APIEndpointService) CreateAPIEndpoint(ctx context.Context, value *APIEndpoint) (*APIEndpoint, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("%w: api endpoint is required", ErrInvalidArgument)
	}
	serviceResource, err := validateServiceResource(value.ServiceResource)
	if err != nil {
		return nil, err
	}
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	value.ServiceResource = serviceResource
	if err := normalizeAPIEndpoint(value); err != nil {
		return nil, err
	}
	value.BaseFields = NewBaseFields(AuditActorFromContext(ctx))
	return s.repository.Create(ctx, value)
}

// GetAPIEndpoint 按 UUID 返回一个未删除的 API 端点。
func (s *APIEndpointService) GetAPIEndpoint(ctx context.Context, id uuid.UUID) (*APIEndpoint, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("%w: api endpoint id is required", ErrInvalidArgument)
	}
	return s.repository.Get(ctx, id)
}

// ListAPIEndpoints 返回指定服务资源下的全部未删除 API 端点。
func (s *APIEndpointService) ListAPIEndpoints(ctx context.Context, serviceResource string) ([]*APIEndpoint, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	serviceResource, err := validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	return s.repository.ListByServiceResource(ctx, serviceResource)
}

// UpdateAPIEndpoint 更新 API 端点的控制器、路由、摘要和启用状态。
func (s *APIEndpointService) UpdateAPIEndpoint(ctx context.Context, value *APIEndpoint) (*APIEndpoint, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if value == nil || value.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: api endpoint id is required", ErrInvalidArgument)
	}
	existing, err := s.repository.Get(ctx, value.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNotFound
	}
	serviceResource := strings.TrimSpace(value.ServiceResource)
	if serviceResource == "" {
		serviceResource = existing.ServiceResource
	}
	serviceResource, err = validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	if serviceResource != strings.TrimSpace(existing.ServiceResource) {
		return nil, fmt.Errorf("%w: api endpoint service_resource cannot be changed", ErrConflict)
	}
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	updated := *value
	updated.ID = existing.ID
	updated.ServiceResource = serviceResource
	updated.BaseFields = existing.BaseFields
	if err := normalizeAPIEndpoint(&updated); err != nil {
		return nil, err
	}
	actor := AuditActorFromContext(ctx)
	updated.UpdatedByID = actor.ID
	updated.UpdatedByName = actor.Name
	return s.repository.Update(ctx, &updated)
}

// DeleteAPIEndpoint 软删除指定 API 端点。
func (s *APIEndpointService) DeleteAPIEndpoint(ctx context.Context, id uuid.UUID) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if id == uuid.Nil {
		return fmt.Errorf("%w: api endpoint id is required", ErrInvalidArgument)
	}
	return s.repository.SoftDelete(ctx, id)
}

func (s *APIEndpointService) ensureReady() error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("api endpoint repository is not configured")
	}
	return nil
}

func (s *APIEndpointService) ensureServiceResource(ctx context.Context, serviceResource string) error {
	if s.serviceResources == nil {
		return nil
	}
	value, err := s.serviceResources.Get(ctx, serviceResource)
	if err != nil {
		return err
	}
	if value == nil {
		return ErrNotFound
	}
	if !value.IsActive {
		return fmt.Errorf("%w: service resource is disabled", ErrConflict)
	}
	return nil
}

func normalizeAPIEndpoint(value *APIEndpoint) error {
	value.Controller = strings.TrimSpace(value.Controller)
	if value.Controller == "" || len([]rune(value.Controller)) > apiEndpointControllerMaxLength {
		return fmt.Errorf("%w: controller must be 1-%d characters", ErrInvalidArgument, apiEndpointControllerMaxLength)
	}
	value.Method = strings.ToUpper(strings.TrimSpace(value.Method))
	if !isAPIEndpointHTTPMethod(value.Method) {
		return fmt.Errorf("%w: unsupported HTTP method %q", ErrInvalidArgument, value.Method)
	}
	value.PathTemplate = strings.TrimSpace(value.PathTemplate)
	if value.PathTemplate == "" || len([]rune(value.PathTemplate)) > apiEndpointPathMaxLength || !strings.HasPrefix(value.PathTemplate, "/") {
		return fmt.Errorf("%w: path_template must be an absolute route path", ErrInvalidArgument)
	}
	value.Summary = strings.TrimSpace(value.Summary)
	return nil
}

func isAPIEndpointHTTPMethod(value string) bool {
	switch value {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}

// ImportSwaggerAPIEndpoints 将 Swagger/OpenAPI 文档中的 HTTP 操作导入为启用端点。
func (s *APIEndpointService) ImportSwaggerAPIEndpoints(ctx context.Context, request SwaggerImportRequest) (*SwaggerImportResult, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	serviceResource, err := validateServiceResource(request.ServiceResource)
	if err != nil {
		return nil, err
	}
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	document, err := fetchSwaggerDocument(ctx, request.SwaggerURL)
	if err != nil {
		return nil, err
	}
	operations, err := parseSwaggerOperations(document)
	if err != nil {
		return nil, err
	}
	existing, err := s.repository.ListByServiceResource(ctx, serviceResource)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]struct{}, len(existing))
	for _, endpoint := range existing {
		if endpoint == nil {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
		routes[method+"\x00"+strings.TrimSpace(endpoint.PathTemplate)] = struct{}{}
	}
	result := &SwaggerImportResult{Total: len(operations)}
	for _, operation := range operations {
		key := operation.Method + "\x00" + operation.PathTemplate
		if _, exists := routes[key]; exists {
			result.Skipped++
			continue
		}
		value := &APIEndpoint{
			ServiceResource: serviceResource,
			Controller:      operation.Controller,
			Method:          operation.Method,
			PathTemplate:    operation.PathTemplate,
			Summary:         operation.Summary,
			Enabled:         true,
		}
		if _, err := s.CreateAPIEndpoint(ctx, value); err != nil {
			if errors.Is(err, ErrAlreadyExists) {
				result.Skipped++
				continue
			}
			return nil, err
		}
		routes[key] = struct{}{}
		result.Created++
	}
	return result, nil
}
