package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
)

type Handler struct {
	serviceResources               biz.ServiceResourceCatalog
	serviceResourceService         *biz.ServiceResourceService
	permissions                    *biz.PermissionService
	apiEndpoints                   *biz.APIEndpointService
	rbacV1                         *biz.RBACV1Service
	authorizationServiceCredential string
	auth                           *auth.Service
}

func (h *Handler) WithAuthorizationServiceCredential(credential string) *Handler {
	h.authorizationServiceCredential = strings.TrimSpace(credential)
	return h
}

// WithServiceResourceCatalog 注入只读服务资源目录，供服务资源查询接口使用。
func (h *Handler) WithServiceResourceCatalog(catalog biz.ServiceResourceCatalog) *Handler {
	h.serviceResources = catalog
	if h.apiEndpoints != nil {
		h.apiEndpoints.WithServiceResourceCatalog(catalog)
	}
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

// NewHandler 创建 HTTP 处理器，并注入权限业务与可选的认证服务。
func NewHandler(permissions *biz.PermissionService, authenticators ...*auth.Service) *Handler {
	var authenticator *auth.Service
	if len(authenticators) > 0 {
		authenticator = authenticators[0]
	}
	return &Handler{permissions: permissions, auth: authenticator}
}

// CreateRole 在指定服务资源下创建角色。
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ServiceResource string `json:"service_resource"`
		Code            string `json:"code"`
		Name            string `json:"name"`
		Description     string `json:"description"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	role, err := h.permissions.CreateRole(h.auditContext(r), request.ServiceResource, request.Code, request.Name, request.Description)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

// UpdateRole 更新角色的编码、名称、描述和启用状态。
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Enabled     bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	role, err := h.permissions.UpdateRole(h.auditContext(r), r.PathValue("roleID"), request.Code, request.Name, request.Description, request.Enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

// DeleteRole 软删除指定角色。
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	if err := h.permissions.DeleteRole(h.auditContext(r), r.PathValue("roleID")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// ListRoles 返回指定服务资源下的角色列表。
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.permissions.ListRoles(r.Context(), queryServiceResource(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": roles})
}

// CreateMenu 在指定服务资源下创建菜单或按钮节点。
func (h *Handler) CreateMenu(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ServiceResource string             `json:"service_resource"`
		ParentID        *uuid.UUID         `json:"parent_id"`
		Code            string             `json:"code"`
		Name            string             `json:"name"`
		Description     string             `json:"description"`
		Type            biz.MenuType       `json:"type"`
		Path            string             `json:"path"`
		Component       string             `json:"component"`
		APIPath         string             `json:"api_path"`
		HTTPMethod      biz.MenuHTTPMethod `json:"http_method"`
		Icon            string             `json:"icon"`
		Sort            int                `json:"sort"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	menu, err := h.permissions.CreateMenu(h.auditContext(r), &biz.Menu{ServiceResource: request.ServiceResource, ParentID: request.ParentID, Code: request.Code, Name: request.Name, Description: request.Description, Type: request.Type, Path: request.Path, Component: request.Component, APIPath: request.APIPath, HTTPMethod: request.HTTPMethod, Icon: request.Icon, Sort: request.Sort})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, menu)
}

// UpdateMenu 更新菜单的编码、名称、描述、路由及启用状态。
func (h *Handler) UpdateMenu(w http.ResponseWriter, r *http.Request) {
	id, err := parseMenuUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var request struct {
		Code        string             `json:"code"`
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Path        string             `json:"path"`
		Component   string             `json:"component"`
		APIPath     string             `json:"api_path"`
		HTTPMethod  biz.MenuHTTPMethod `json:"http_method"`
		Icon        string             `json:"icon"`
		Sort        int                `json:"sort"`
		Enabled     bool               `json:"enabled"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	menu, err := h.permissions.UpdateMenu(h.auditContext(r), &biz.Menu{ID: id, Code: request.Code, Name: request.Name, Description: request.Description, Path: request.Path, Component: request.Component, APIPath: request.APIPath, HTTPMethod: request.HTTPMethod, Icon: request.Icon, Sort: request.Sort, Enabled: request.Enabled})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, menu)
}

// DeleteMenu 软删除指定菜单。
func (h *Handler) DeleteMenu(w http.ResponseWriter, r *http.Request) {
	id, err := parseMenuUUID(r.PathValue("id"))
	if err == nil {
		err = h.permissions.DeleteMenu(h.auditContext(r), id)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// MenuTree 返回指定服务资源下的菜单树。
func (h *Handler) MenuTree(w http.ResponseWriter, r *http.Request) {
	tree, err := h.permissions.MenuTree(r.Context(), queryServiceResource(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tree})
}

// GrantRoleMenus 原子替换角色拥有的菜单和按钮。
func (h *Handler) GrantRoleMenus(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("roleID")
	var request struct {
		MenuIDs []uuid.UUID `json:"menu_ids"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	if err := h.permissions.GrantRoleMenus(h.auditContext(r), roleID, request.MenuIDs); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// RoleMenus 返回角色拥有的菜单和按钮 ID。
func (h *Handler) RoleMenus(w http.ResponseWriter, r *http.Request) {
	ids, err := h.permissions.RoleMenus(r.Context(), r.PathValue("roleID"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"menu_ids": ids})
}

// ReplaceUserRoles 原子替换用户在服务资源下的角色。
func (h *Handler) ReplaceUserRoles(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RoleIDs []string `json:"role_ids"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	if err := h.permissions.ReplaceUserRoles(h.auditContext(r), r.PathValue("userID"), queryServiceResource(r), request.RoleIDs); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// UserRoles 返回用户在服务资源下拥有的角色 ID。
func (h *Handler) UserRoles(w http.ResponseWriter, r *http.Request) {
	roleIDs, err := h.permissions.UserRoleIDs(r.Context(), r.PathValue("userID"), queryServiceResource(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role_ids": roleIDs})
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

// parseMenuUUID 解析并校验菜单路径中的 UUID。
func parseMenuUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: menu_id must be a valid UUID", biz.ErrInvalidArgument)
	}
	return id, nil
}
