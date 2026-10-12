package httpserver

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/biz"
)

func rbacPageParams(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return page, pageSize
}

// AuthorizeRBAC evaluates the active release for a trusted service caller.
func (h *Handler) AuthorizeRBAC(w http.ResponseWriter, r *http.Request) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || fields[0] != "Bearer" || h.authorizationServiceCredential == "" || subtle.ConstantTimeCompare([]byte(fields[1]), []byte(h.authorizationServiceCredential)) != 1 {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var request struct {
		AppKey          string `json:"appKey"`
		ServiceResource string `json:"serviceResource"`
		Subject         string `json:"subject"`
		Domain          string `json:"domain"`
		Method          string `json:"method"`
		Path            string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil || (strings.TrimSpace(request.AppKey) == "" || strings.TrimSpace(request.ServiceResource) == "") {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	result, err := h.rbacV1.Authorize(r.Context(), request.AppKey, map[string]any{"service_resource": request.ServiceResource, "subject": request.Subject, "domain": request.Domain, "method": request.Method, "path": request.Path})
	if err != nil {
		writeError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, result)
}

// WithRBACV1Service wires the normalized RDS control-plane API.
func (h *Handler) WithRBACV1Service(service *biz.RBACV1Service) *Handler {
	h.rbacV1 = service
	return h
}

func (h *Handler) ListRBACV1Endpoints(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	page, pageSize := rbacPageParams(r)
	result, err := h.rbacV1.ApplicationEndpoints(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("service_resource")), page, pageSize)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) SaveRBACV1Endpoint(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	var value biz.RBACEndpoint
	if err := decodeJSON(r, &value); err != nil {
		writeError(w, err)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, err)
		return
	}
	item, err := h.rbacV1.SaveEndpoint(r.Context(), service, value)
	item.ServiceResource = service
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if value.ID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}
func (h *Handler) DeleteRBACV1Endpoint(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if scope == "" || err != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, err)
		return
	}
	if err = h.rbacV1.DeleteEndpoint(r.Context(), service, id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
func (h *Handler) ImportRBACV1Swagger(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	var body struct {
		SwaggerURL string `json:"swagger_url"`
	}
	if scope == "" || decodeJSON(r, &body) != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.rbacV1.ImportSwagger(r.Context(), service, body.SwaggerURL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) ListRBACV1Roles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListRoles(r.Context(), scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) SaveRBACV1Role(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	var value biz.RBACRole
	if err := decodeJSON(r, &value); err != nil {
		writeError(w, err)
		return
	}
	item, err := h.rbacV1.SaveRole(r.Context(), scope, value)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if value.ID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}
func (h *Handler) DeleteRBACV1Role(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if scope == "" || err != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	if err = h.rbacV1.DeleteRole(r.Context(), scope, id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

func (h *Handler) ListRBACV1SubjectRoles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListSubjectRoleBindings(r.Context(), scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) PublishRBACV1(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	var body struct {
		Description string `json:"description"`
	}
	if scope == "" || decodeJSON(r, &body) != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	if strings.TrimSpace(body.Description) == "" {
		body.Description = "RBAC configuration release"
	}
	v, err := h.rbacV1.Publish(r.Context(), scope, body.Description, biz.AuditActorFromContext(h.auditContext(r)).ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (h *Handler) ListRBACV1Releases(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	items, err := h.rbacV1.Releases(r.Context(), scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) SimulateRBACV1(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	var body map[string]any
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.rbacV1.Authorize(r.Context(), scope, body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) ReplaceRBACV1SubjectRoles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	var body struct {
		Subject string      `json:"subject"`
		Domain  string      `json:"domain"`
		RoleIDs []uuid.UUID `json:"role_ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := h.rbacV1.ReplaceSubjectRoles(r.Context(), scope, body.Subject, body.Domain, body.RoleIDs); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
