package httpserver

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/biz"
)

func (h *Handler) WithAPIPermissionGroups(service *biz.APIPermissionGroupService) *Handler {
	h.apiPermissionGroups = service
	return h
}

func (h *Handler) permissionGroupService(w http.ResponseWriter, r *http.Request, service string) bool {
	if h.apiPermissionGroups == nil || h.rbacV1 == nil {
		tokenHTTPError(w, 503, "PERMISSION_GROUP_SERVICE_UNAVAILABLE")
		return false
	}
	if err := h.rbacV1.RequireApplicationService(r.Context(), r.URL.Query().Get("app_key"), service); err != nil {
		tokenManagementError(w, err)
		return false
	}
	return true
}

func (h *Handler) ListAPIPermissionGroups(w http.ResponseWriter, r *http.Request) {
	if h.apiPermissionGroups == nil || h.rbacV1 == nil {
		tokenHTTPError(w, 503, "PERMISSION_GROUP_SERVICE_UNAVAILABLE")
		return
	}
	services, err := h.rbacV1.ListApplicationServices(r.Context(), r.URL.Query().Get("app_key"))
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	filter := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if filter != "" && !h.permissionGroupService(w, r, filter) {
		return
	}
	items := make([]biz.APIPermissionGroup, 0)
	for _, service := range services {
		if !service.IsActive || (filter != "" && filter != service.Key) {
			continue
		}
		groups, err := h.apiPermissionGroups.List(r.Context(), service.Key)
		if err != nil {
			tokenManagementError(w, err)
			return
		}
		items = append(items, groups...)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (h *Handler) SaveAPIPermissionGroup(w http.ResponseWriter, r *http.Request) {
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if !h.permissionGroupService(w, r, service) {
		return
	}
	var value biz.APIPermissionGroup
	if err := decodeJSON(r, &value); err != nil {
		writeError(w, err)
		return
	}
	if value.ServiceResource != "" && strings.TrimSpace(value.ServiceResource) != service {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	value.ServiceResource = service
	result, err := h.apiPermissionGroups.Save(h.auditContext(r), value)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	status := 200
	if value.ID == uuid.Nil {
		status = 201
	}
	writeJSON(w, status, result)
}

func (h *Handler) DeleteAPIPermissionGroup(w http.ResponseWriter, r *http.Request) {
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if !h.permissionGroupService(w, r, service) {
		return
	}
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	if err := h.apiPermissionGroups.Delete(h.auditContext(r), service, id); err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, nil)
}
