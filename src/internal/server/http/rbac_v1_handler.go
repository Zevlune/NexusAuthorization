package httpserver

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

func rbacPageParams(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return page, pageSize
}

// AuthorizeRBAC evaluates currently saved permissions for a trusted service caller.
func (h *Handler) AuthorizeRBAC(w http.ResponseWriter, r *http.Request) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || h.authorizationServiceCredential == "" || subtle.ConstantTimeCompare([]byte(fields[1]), []byte(h.authorizationServiceCredential)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="authorize"`)
		writeRawJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "errorCode": "UNAUTHENTICATED", "errorMessage": "service credential required", "result": nil})
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
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(request.AppKey) == "" || strings.TrimSpace(request.ServiceResource) == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	result, err := h.rbacV1.Authorize(r.Context(), request.AppKey, map[string]any{"service_resource": request.ServiceResource, "subject": request.Subject, "domain": request.Domain, "method": request.Method, "path": request.Path})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeRawJSON(w, http.StatusOK, result)
}

// WithRBACV1Service wires the normalized RDS control-plane API.
func (h *Handler) WithRBACV1Service(service *biz.RBACV1Service) *Handler {
	h.rbacV1 = service
	return h
}

// ListRBACV1Endpoints lists the endpoints assigned to an application service.
// app_key and service_resource are required and the page is bounded by
// rbacPageParams.
func (h *Handler) ListRBACV1Endpoints(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	page, pageSize := rbacPageParams(r)
	result, err := h.rbacV1.ApplicationEndpoints(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("service_resource")), page, pageSize)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// SaveRBACV1Endpoint creates or updates an endpoint for an application service.
// The service must belong to the application; a missing endpoint id selects
// 201 Created, otherwise the response is 200 OK.
func (h *Handler) SaveRBACV1Endpoint(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	var value biz.RBACEndpoint
	if err := decodeJSON(r, &value); err != nil {
		writeError(w, r, err)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, r, err)
		return
	}
	item, err := h.rbacV1.SaveEndpoint(r.Context(), service, value)
	if err != nil {
		writeError(w, r, err)
		return
	}
	item.ServiceResource = service
	status := http.StatusOK
	if value.ID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}

// DeleteRBACV1Endpoint deletes an endpoint from an application service after
// validating the application/service scope and the path id.
func (h *Handler) DeleteRBACV1Endpoint(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if scope == "" || err != nil {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, r, err)
		return
	}
	if err = h.rbacV1.DeleteEndpoint(r.Context(), service, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// ImportRBACV1Swagger imports Swagger operations into an application service.
// The body must include swagger_url and the service must belong to the
// application; successful imports return 201 Created.
func (h *Handler) ImportRBACV1Swagger(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	var body struct {
		SwaggerURL string `json:"swagger_url"`
	}
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service_resource"))
	if err := h.rbacV1.RequireApplicationService(r.Context(), scope, service); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.rbacV1.ImportSwagger(r.Context(), service, body.SwaggerURL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// ListRBACV1Roles lists roles for the application scope supplied by app_key.
func (h *Handler) ListRBACV1Roles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListRoles(r.Context(), scope)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// SaveRBACV1Role creates or updates a role in the application scope. It returns
// 201 Created for a new role and 200 OK for an update.
func (h *Handler) SaveRBACV1Role(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	var value biz.RBACRole
	if err := decodeJSON(r, &value); err != nil {
		writeError(w, r, err)
		return
	}
	item, err := h.rbacV1.SaveRole(r.Context(), scope, value)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if value.ID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}

// DeleteRBACV1Role deletes a role from the application scope after validating
// the path id.
func (h *Handler) DeleteRBACV1Role(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if scope == "" || err != nil {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	if err = h.rbacV1.DeleteRole(r.Context(), scope, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// UpdateRBACV1RoleSubjects grants or revokes one role for a set of subjects in
// one domain. The body carries optional domain, add and remove subject lists.
func (h *Handler) UpdateRBACV1RoleSubjects(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if scope == "" || err != nil {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	var body struct {
		Domain string   `json:"domain"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.rbacV1.UpdateRoleSubjects(r.Context(), scope, id, body.Domain, body.Add, body.Remove); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// ListRBACV1SubjectRoles lists subject-to-role bindings for the application
// scope supplied by app_key.
func (h *Handler) ListRBACV1SubjectRoles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListSubjectRoleBindings(r.Context(), scope)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// SimulateRBACV1 evaluates a request body against the application's current
// saved policy. It does not change stored permissions or their invalidation
// identity.
func (h *Handler) SimulateRBACV1(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	var body map[string]any
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.rbacV1.Authorize(r.Context(), scope, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ReplaceRBACV1SubjectRoles atomically replaces one subject's roles in the
// application scope. The body carries subject, optional domain, and role_ids.
func (h *Handler) ReplaceRBACV1SubjectRoles(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if scope == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	var body struct {
		Subject string      `json:"subject"`
		Domain  string      `json:"domain"`
		RoleIDs []uuid.UUID `json:"role_ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.rbacV1.ReplaceSubjectRoles(r.Context(), scope, body.Subject, body.Domain, body.RoleIDs); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
