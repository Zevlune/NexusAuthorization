package httpserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/biz"
)

// WithAPIEndpointService 注入 API 端点业务服务。
func (h *Handler) WithAPIEndpointService(service *biz.APIEndpointService) *Handler {
	h.apiEndpoints = service
	if service != nil && h.serviceResources != nil {
		service.WithServiceResourceCatalog(h.serviceResources)
	}
	return h
}

type apiEndpointRequest struct {
	ServiceResource string  `json:"service_resource"`
	Controller      string  `json:"controller"`
	Method          string  `json:"method"`
	PathTemplate    string  `json:"path_template"`
	Summary         *string `json:"summary"`
	Enabled         *bool   `json:"enabled"`
}

// CreateAuthorizationAPIEndpoint 创建一个服务资源作用域内的 API 端点。
func (h *Handler) CreateAuthorizationAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var request apiEndpointRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	summary := ""
	if request.Summary != nil {
		summary = *request.Summary
	}
	value, err := h.apiEndpoints.CreateAPIEndpoint(h.auditContext(r), &biz.APIEndpoint{
		ServiceResource: request.ServiceResource,
		Controller:      request.Controller,
		Method:          request.Method,
		PathTemplate:    request.PathTemplate,
		Summary:         summary,
		Enabled:         enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

// ImportSwaggerAPIEndpoints 导入 Swagger/OpenAPI 文档中的 API 端点。
func (h *Handler) ImportSwaggerAPIEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var request biz.SwaggerImportRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.apiEndpoints.ImportSwaggerAPIEndpoints(h.auditContext(r), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ListAuthorizationAPIEndpoints 返回服务资源下全部 API 端点。
func (h *Handler) ListAuthorizationAPIEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	values, err := h.apiEndpoints.ListAPIEndpoints(r.Context(), queryServiceResource(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

// GetAuthorizationAPIEndpoint 按 UUID 返回一个 API 端点。
func (h *Handler) GetAuthorizationAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	id, err := parseAPIEndpointUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	value, err := h.apiEndpoints.GetAPIEndpoint(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// UpdateAuthorizationAPIEndpoint 更新 API 端点的可变字段。
func (h *Handler) UpdateAuthorizationAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	id, err := parseAPIEndpointUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	existing, err := h.apiEndpoints.GetAPIEndpoint(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	var request apiEndpointRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	value := *existing
	if request.ServiceResource != "" {
		value.ServiceResource = request.ServiceResource
	}
	if request.Controller != "" {
		value.Controller = request.Controller
	}
	if request.Method != "" {
		value.Method = request.Method
	}
	if request.PathTemplate != "" {
		value.PathTemplate = request.PathTemplate
	}
	if request.Summary != nil {
		value.Summary = *request.Summary
	}
	if request.Enabled != nil {
		value.Enabled = *request.Enabled
	}
	value.ID = id
	updated, err := h.apiEndpoints.UpdateAPIEndpoint(h.auditContext(r), &value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeleteAuthorizationAPIEndpoint 软删除一个 API 端点。
func (h *Handler) DeleteAuthorizationAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.apiEndpoints == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	id, err := parseAPIEndpointUUID(r.PathValue("id"))
	if err == nil {
		err = h.apiEndpoints.DeleteAPIEndpoint(h.auditContext(r), id)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

func parseAPIEndpointUUID(value string) (uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.Nil, fmtAPIEndpointIDError()
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmtAPIEndpointIDError()
	}
	return id, nil
}

func fmtAPIEndpointIDError() error {
	return fmt.Errorf("%w: api_endpoint_id must be a valid UUID", biz.ErrInvalidArgument)
}
