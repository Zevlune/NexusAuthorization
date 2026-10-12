package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// NexusAuth 当前将 API 资源名称限制为 128 个字符；本地和远程目录共用该校验上限。
	maxServiceResourceLength      = 128
	serviceResourceDefaultTimeout = 5 * time.Second

	// ServiceResourceSourceLocal 表示由权限中心维护的服务资源。
	ServiceResourceSourceLocal = "local"
	// ServiceResourceSourceNexusAuth 表示从 NexusAuth 同步的服务资源。
	ServiceResourceSourceNexusAuth = "nexusauth"
)

// ServiceResource 表示身份平台发布的权限服务资源命名空间。
// Permission Center 只消费其元数据，不负责管理 NexusAuth 服务资源。
type ServiceResource struct {
	ID          string    `json:"id,omitempty"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Audience    string    `json:"audience"`
	Description string    `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

// ServiceResourceCatalog 提供只读的服务资源目录。
// 它用于校验权限作用域并填充登录后的作用域选择器，资源变更由目录提供方负责。
type ServiceResourceCatalog interface {
	List(context.Context) ([]*ServiceResource, error)
	Get(context.Context, string) (*ServiceResource, error)
}

// ServiceResourceRepository 定义本地服务资源的持久化操作。
type ServiceResourceRepository interface {
	Create(context.Context, *ServiceResource) (*ServiceResource, error)
	Get(context.Context, string) (*ServiceResource, error)
	List(context.Context) ([]*ServiceResource, error)
	Update(context.Context, *ServiceResource) (*ServiceResource, error)
	SoftDelete(context.Context, string) error
}

// ServiceResourceSourceRepository 定义按来源过滤服务资源的可选仓储能力。
// PostgreSQL 仓储实现该接口，业务服务优先使用数据库过滤；轻量测试仓储可只实现基础接口。
type ServiceResourceSourceRepository interface {
	GetBySource(context.Context, string, string) (*ServiceResource, error)
	ListBySource(context.Context, string) ([]*ServiceResource, error)
}

// ServiceResourceService 提供本地资源 CRUD，以及按配置来源读取服务资源。
type ServiceResourceService struct {
	repository ServiceResourceRepository
	remote     ServiceResourceCatalog
	source     string
}

var _ ServiceResourceCatalog = (*ServiceResourceService)(nil)

// Writable 返回当前服务资源来源是否支持本地写操作。
func (s *ServiceResourceService) Writable() bool {
	return s != nil && s.source == ServiceResourceSourceLocal
}

// NewServiceResourceCatalog 创建按配置来源工作的服务资源服务。
// local 来源读取仓储并支持 CRUD；nexusauth 来源直接读取远程目录。
func NewServiceResourceCatalog(repository ServiceResourceRepository, remote ServiceResourceCatalog, source string) (*ServiceResourceService, error) {
	if repository == nil {
		return nil, fmt.Errorf("service resource repository is required")
	}
	source, err := normalizeServiceResourceSource(source)
	if err != nil {
		return nil, err
	}
	if source == ServiceResourceSourceNexusAuth && remote == nil {
		return nil, fmt.Errorf("nexusauth service resource client is required")
	}
	return &ServiceResourceService{repository: repository, remote: remote, source: source}, nil
}

// Create 创建一条本地服务资源；来源、外部 ID 和创建时间由服务端控制。
func (s *ServiceResourceService) Create(ctx context.Context, resource *ServiceResource) (*ServiceResource, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("service resource repository is not configured")
	}
	if s.source == ServiceResourceSourceNexusAuth {
		return nil, fmt.Errorf("%w: NexusAuth service resources cannot be created", ErrConflict)
	}
	if resource == nil {
		return nil, fmt.Errorf("%w: service resource is required", ErrInvalidArgument)
	}
	resourceKey, err := validateServiceResource(resource.Key)
	if err != nil {
		return nil, err
	}
	if source := strings.TrimSpace(resource.Source); source != "" && !strings.EqualFold(source, ServiceResourceSourceLocal) {
		return nil, fmt.Errorf("%w: NexusAuth service resources cannot be created locally", ErrConflict)
	}
	created := *resource
	created.Key = resourceKey
	created.Source = ServiceResourceSourceLocal
	created.ID = ""
	return s.repository.Create(ctx, &created)
}

// Update 更新本地服务资源的展示信息、受众、描述和启用状态。
// 业务键、来源和外部 ID 不接受客户端修改。
func (s *ServiceResourceService) Update(ctx context.Context, resource *ServiceResource) (*ServiceResource, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("service resource repository is not configured")
	}
	if s.source == ServiceResourceSourceNexusAuth {
		return nil, fmt.Errorf("%w: NexusAuth service resources cannot be updated", ErrConflict)
	}
	if resource == nil {
		return nil, fmt.Errorf("%w: service resource is required", ErrInvalidArgument)
	}
	resourceKey, err := validateServiceResource(resource.Key)
	if err != nil {
		return nil, err
	}
	if source := strings.TrimSpace(resource.Source); source != "" && !strings.EqualFold(source, ServiceResourceSourceLocal) {
		return nil, fmt.Errorf("%w: NexusAuth service resources cannot be updated", ErrConflict)
	}
	existing, err := s.repository.Get(ctx, resourceKey)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNotFound
	}
	if existing.Source != ServiceResourceSourceLocal {
		return nil, fmt.Errorf("%w: NexusAuth service resources cannot be updated", ErrConflict)
	}
	updated := *existing
	updated.Key = existing.Key
	updated.DisplayName = strings.TrimSpace(resource.DisplayName)
	updated.Audience = strings.TrimSpace(resource.Audience)
	updated.Description = strings.TrimSpace(resource.Description)
	updated.IsActive = resource.IsActive
	return s.repository.Update(ctx, &updated)
}

// Delete 软删除本地服务资源，并拒绝删除 NexusAuth 来源记录。
func (s *ServiceResourceService) Delete(ctx context.Context, resourceKey string) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("service resource repository is not configured")
	}
	if s.source == ServiceResourceSourceNexusAuth {
		return fmt.Errorf("%w: NexusAuth service resources cannot be deleted", ErrConflict)
	}
	resourceKey, err := validateServiceResource(resourceKey)
	if err != nil {
		return err
	}
	existing, err := s.repository.Get(ctx, resourceKey)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	if existing.Source != ServiceResourceSourceLocal {
		return fmt.Errorf("%w: NexusAuth service resources cannot be deleted", ErrConflict)
	}
	return s.repository.SoftDelete(ctx, resourceKey)
}

// List 返回目录中的当前未删除服务资源。
func (s *ServiceResourceService) List(ctx context.Context) ([]*ServiceResource, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("service resource repository is not configured")
	}
	if s.source == ServiceResourceSourceNexusAuth {
		return s.listRemote(ctx)
	}
	return s.listBySource(ctx, s.source)
}

// Get 返回指定业务键对应的当前未删除服务资源。
func (s *ServiceResourceService) Get(ctx context.Context, resourceKey string) (*ServiceResource, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("service resource repository is not configured")
	}
	resourceKey, err := validateServiceResource(resourceKey)
	if err != nil {
		return nil, err
	}
	if s.source == ServiceResourceSourceNexusAuth {
		return s.getRemote(ctx, resourceKey)
	}
	return s.getBySource(ctx, resourceKey, s.source)
}

func (s *ServiceResourceService) listRemote(ctx context.Context) ([]*ServiceResource, error) {
	if s.remote == nil {
		return nil, fmt.Errorf("nexusauth service resource client is not configured")
	}
	resources, err := s.remote.List(ctx)
	if err != nil {
		return nil, err
	}
	normalized := make([]*ServiceResource, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		value, err := normalizeRemoteServiceResource(resource)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func (s *ServiceResourceService) getRemote(ctx context.Context, resourceKey string) (*ServiceResource, error) {
	if s.remote == nil {
		return nil, fmt.Errorf("nexusauth service resource client is not configured")
	}
	resource, err := s.remote.Get(ctx, resourceKey)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, ErrNotFound
	}
	normalized, err := normalizeRemoteServiceResource(resource)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// listBySource 按来源读取服务资源；支持按来源查询的仓储直接在数据库中过滤。
func (s *ServiceResourceService) listBySource(ctx context.Context, source string) ([]*ServiceResource, error) {
	if repository, ok := s.repository.(ServiceResourceSourceRepository); ok {
		return repository.ListBySource(ctx, source)
	}
	values, err := s.repository.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]*ServiceResource, 0, len(values))
	for _, value := range values {
		if value != nil && strings.EqualFold(strings.TrimSpace(value.Source), source) {
			filtered = append(filtered, value)
		}
	}
	return filtered, nil
}

// getBySource 按来源读取单个服务资源；错误来源统一视为不存在。
func (s *ServiceResourceService) getBySource(ctx context.Context, resourceKey, source string) (*ServiceResource, error) {
	if repository, ok := s.repository.(ServiceResourceSourceRepository); ok {
		value, err := repository.GetBySource(ctx, resourceKey, source)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, ErrNotFound
		}
		return value, nil
	}
	value, err := s.repository.Get(ctx, resourceKey)
	if err != nil {
		return nil, err
	}
	if value == nil || !strings.EqualFold(strings.TrimSpace(value.Source), source) {
		return nil, ErrNotFound
	}
	return value, nil
}

func normalizeRemoteServiceResource(resource *ServiceResource) (*ServiceResource, error) {
	if resource == nil {
		return nil, fmt.Errorf("%w: service resource is required", ErrInvalidArgument)
	}
	resourceKey, err := validateServiceResource(resource.Key)
	if err != nil {
		return nil, err
	}
	value := *resource
	value.Key = resourceKey
	value.Source = ServiceResourceSourceNexusAuth
	value.ID = strings.TrimSpace(value.ID)
	value.Name = strings.TrimSpace(value.Name)
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	if value.Name == "" {
		value.Name = value.DisplayName
	}
	value.Audience = strings.TrimSpace(value.Audience)
	value.Description = strings.TrimSpace(value.Description)
	return &value, nil
}

func normalizeServiceResourceSource(source string) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = ServiceResourceSourceNexusAuth
	}
	if source != ServiceResourceSourceLocal && source != ServiceResourceSourceNexusAuth {
		return "", fmt.Errorf("%w: service_resource.source must be local or nexusauth", ErrInvalidArgument)
	}
	return source, nil
}

// NexusAuthServiceResourceCatalog 使用专用 bearer 凭据读取 NexusAuth OpenAPI 服务资源目录。
// 凭据不会被返回或写入日志。
type NexusAuthServiceResourceCatalog struct {
	baseURL    string
	credential string
	client     *http.Client
}

// NewNexusAuthServiceResourceCatalog 创建读取 NexusAuth 服务资源目录的客户端。
func NewNexusAuthServiceResourceCatalog(baseURL, credential string, timeout time.Duration) (*NexusAuthServiceResourceCatalog, error) {
	return NewNexusAuthServiceResourceCatalogWithClient(baseURL, credential, timeout, nil)
}

// NewNexusAuthServiceResourceCatalogWithClient 创建可注入 HTTP 客户端的 NexusAuth 服务资源目录客户端。
// baseURL 必须是绝对的 HTTP 或 HTTPS 地址，credential 不能为空；非正 timeout 使用默认值。
func NewNexusAuthServiceResourceCatalogWithClient(baseURL, credential string, timeout time.Duration, client *http.Client) (*NexusAuthServiceResourceCatalog, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("nexusauth service-resource base URL must be an absolute http or https URL")
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil, fmt.Errorf("nexusauth service-resource credential is required")
	}
	if timeout <= 0 {
		timeout = serviceResourceDefaultTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &NexusAuthServiceResourceCatalog{baseURL: baseURL, credential: credential, client: client}, nil
}

// List 从 NexusAuth 服务资源目录读取全部服务资源。
func (c *NexusAuthServiceResourceCatalog) List(ctx context.Context) ([]*ServiceResource, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("service resource client is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/openapi/v1/service-resources", nil)
	if err != nil {
		return nil, fmt.Errorf("create NexusAuth service-resource request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.credential)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request NexusAuth service-resource directory: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read NexusAuth service-resource directory: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("NexusAuth service-resource directory returned HTTP %d", resp.StatusCode)
	}
	return decodeNexusAuthServiceResources(body)
}

// Get 从 NexusAuth 服务资源目录按业务键查找服务资源。
func (c *NexusAuthServiceResourceCatalog) Get(ctx context.Context, key string) (*ServiceResource, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: service_resource is required", ErrInvalidArgument)
	}
	resources, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		if resource != nil && resource.Key == key {
			return resource, nil
		}
	}
	return nil, ErrNotFound
}

type nexusAuthServiceResource struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Audience    string    `json:"audience"`
	Description string    `json:"description"`
	IsActive    bool      `json:"isActive"`
	CreatedAt   time.Time `json:"createdAt"`
}

// decodeNexusAuthServiceResources 将 NexusAuth 服务资源目录响应解码为业务模型。
func decodeNexusAuthServiceResources(body []byte) ([]*ServiceResource, error) {
	var values []nexusAuthServiceResource
	if err := json.Unmarshal(body, &values); err != nil {
		var envelope struct {
			Items []nexusAuthServiceResource `json:"items"`
		}
		if envelopeErr := json.Unmarshal(body, &envelope); envelopeErr != nil {
			return nil, fmt.Errorf("decode NexusAuth service-resource directory: %w", err)
		}
		values = envelope.Items
	}
	resources := make([]*ServiceResource, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value.Name)
		if key == "" {
			return nil, fmt.Errorf("decode NexusAuth service-resource directory: resource name is empty")
		}
		displayName := strings.TrimSpace(value.DisplayName)
		resources = append(resources, &ServiceResource{
			ID: value.ID, Key: key, Name: displayName, DisplayName: displayName, Audience: value.Audience,
			Description: value.Description, IsActive: value.IsActive, Source: ServiceResourceSourceNexusAuth, CreatedAt: value.CreatedAt,
		})
	}
	return resources, nil
}

// validateServiceResource 校验服务资源名称非空且不超过平台允许的长度。
func validateServiceResource(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > maxServiceResourceLength {
		return "", fmt.Errorf("%w: service_resource must be 1-%d characters", ErrInvalidArgument, maxServiceResourceLength)
	}
	return value, nil
}
