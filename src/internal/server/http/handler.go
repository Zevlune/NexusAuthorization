package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
)

type Handler struct {
	serviceResources               biz.ServiceResourceCatalog
	serviceResourceService         *biz.ServiceResourceService
	rbacV1                         *biz.RBACV1Service
	authorizationServiceCredential string
	auth                           *auth.Service
	users                          SCIMUserDirectory
}

func (h *Handler) WithAuthorizationServiceCredential(credential string) *Handler {
	h.authorizationServiceCredential = strings.TrimSpace(credential)
	return h
}

// WithServiceResourceCatalog 注入只读服务资源目录，供服务资源查询接口使用。
func (h *Handler) WithServiceResourceCatalog(catalog biz.ServiceResourceCatalog) *Handler {
	h.serviceResources = catalog
	return h
}

// WithServiceResourceService 注入服务资源可写业务服务。
func (h *Handler) WithServiceResourceService(service *biz.ServiceResourceService) *Handler {
	h.serviceResourceService = service
	return h
}

// CreateServiceResource 创建一条本地服务资源。
func (h *Handler) CreateServiceResource(w http.ResponseWriter, r *http.Request) {
	if h.serviceResourceService == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var request struct {
		Key         string `json:"key"`
		DisplayName string `json:"display_name"`
		Audience    string `json:"audience"`
		Description string `json:"description"`
		IsActive    *bool  `json:"is_active"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	isActive := true
	if request.IsActive != nil {
		isActive = *request.IsActive
	}
	value, err := h.serviceResourceService.Create(h.auditContext(r), &biz.ServiceResource{
		Key:         request.Key,
		DisplayName: request.DisplayName,
		Audience:    request.Audience,
		Description: request.Description,
		IsActive:    isActive,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

// UpdateServiceResource 更新本地服务资源的展示信息。
func (h *Handler) UpdateServiceResource(w http.ResponseWriter, r *http.Request) {
	if h.serviceResourceService == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var request struct {
		DisplayName string `json:"display_name"`
		Audience    string `json:"audience"`
		Description string `json:"description"`
		IsActive    bool   `json:"is_active"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	value, err := h.serviceResourceService.Update(h.auditContext(r), &biz.ServiceResource{
		Key:         r.PathValue("key"),
		DisplayName: request.DisplayName,
		Audience:    request.Audience,
		Description: request.Description,
		IsActive:    request.IsActive,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// DeleteServiceResource 软删除一条本地服务资源。
func (h *Handler) DeleteServiceResource(w http.ResponseWriter, r *http.Request) {
	if h.serviceResourceService == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	if err := h.serviceResourceService.Delete(h.auditContext(r), r.PathValue("key")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// ListServiceResources 返回当前身份平台发布的服务资源列表。
func (h *Handler) ListServiceResources(w http.ResponseWriter, r *http.Request) {
	if h.serviceResources == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	values, err := h.serviceResources.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writable := false
	if h.serviceResourceService != nil {
		writable = h.serviceResourceService.Writable()
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values, "writable": writable})
}

// GetServiceResource 按唯一 key 返回一个服务资源。
func (h *Handler) GetServiceResource(w http.ResponseWriter, r *http.Request) {
	if h.serviceResources == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	value, err := h.serviceResources.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// NewHandler 创建 HTTP 处理器，并注入可选的认证服务。
func NewHandler(authenticators ...*auth.Service) *Handler {
	for _, authenticator := range authenticators {
		if authenticator != nil {
			return &Handler{auth: authenticator}
		}
	}
	return &Handler{}
}

// auditContext 将当前登录人写入业务审计上下文。
func (h *Handler) auditContext(r *http.Request) context.Context {
	actor := biz.AuditActor{ID: "system", Name: "system"}
	if h.auth != nil {
		if h.auth.Enabled() {
			if user, ok := h.auth.Me(r); ok {
				actor = biz.AuditActor{ID: user.Subject, Name: user.Name}
			}
		} else {
			actor = biz.AuditActor{ID: "development", Name: "开发模式"}
		}
	}
	return biz.WithAuditActor(r.Context(), actor)
}

// decodeJSON 严格解码单个 JSON 请求体，并拒绝未知字段。
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body", biz.ErrInvalidArgument)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("%w: request body must contain one JSON value", biz.ErrInvalidArgument)
	}
	return nil
}

// queryServiceResource 读取请求中的服务资源作用域。
func queryServiceResource(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("service_resource"))
}
