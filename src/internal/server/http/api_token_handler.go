package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/config"
)

func (h *Handler) WithAPITokens(service *biz.APITokenService, cfg config.APITokenConfig, requireAdmin bool) *Handler {
	h.apiTokens, h.apiTokenConfig, h.apiTokenRequireAdmin = service, cfg, requireAdmin
	if h.apiTokenConfig.ServiceResource == "" {
		h.apiTokenConfig.ServiceResource = "nexusauthorization"
	}
	return h
}

func tokenHTTPError(w http.ResponseWriter, status int, code string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeRawJSON(w, status, map[string]any{"success": false, "errorCode": code, "errorMessage": code, "result": nil})
}

func (h *Handler) tokenManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.apiTokens == nil {
			tokenHTTPError(w, 503, "TOKEN_SERVICE_UNAVAILABLE")
			return
		}
		// Token management uses human/admin identity. A long-lived API Token
		// cannot create another token or escalate its own delegated authority.
		if raw, ok := apiTokenBearer(r); ok && strings.HasPrefix(raw, "nxa_") {
			tokenHTTPError(w, 403, "TOKEN_MANAGEMENT_REQUIRES_ADMIN_IDENTITY")
			return
		}
		if h.apiTokenRequireAdmin {
			user, ok := auth.UserFromContext(r.Context())
			if !ok && h.auth != nil {
				user, ok = h.auth.Me(r)
			}
			if !ok {
				tokenHTTPError(w, 401, "AUTHENTICATION_REQUIRED")
				return
			}
			allowed := false
			for _, subject := range h.apiTokenConfig.AdminSubjects {
				if subject == user.Subject {
					allowed = true
					break
				}
			}
			if !allowed {
				tokenHTTPError(w, 403, "TOKEN_ADMIN_REQUIRED")
				return
			}
		}
		if strings.TrimSpace(r.URL.Query().Get("app_key")) == "" {
			tokenHTTPError(w, 400, "APP_KEY_REQUIRED")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	result, err := h.apiTokens.List(r.Context(), biz.APITokenFilter{AppKey: r.URL.Query().Get("app_key"), Search: r.URL.Query().Get("search"), Status: r.URL.Query().Get("status"), Page: page, PageSize: pageSize})
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func tokenPathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		tokenHTTPError(w, 400, "INVALID_TOKEN_ID")
		return id, false
	}
	return id, true
}

func tokenManagementError(w http.ResponseWriter, err error) {
	if errors.Is(err, biz.ErrInvalidArgument) || errors.Is(err, biz.ErrNotFound) || errors.Is(err, biz.ErrConflict) || errors.Is(err, biz.ErrAlreadyExists) {
		writeError(w, err)
		return
	}
	// Never serialize SQL diagnostics or submitted credential parameters.
	tokenHTTPError(w, 503, "TOKEN_SERVICE_UNAVAILABLE")
}

func (h *Handler) GetAPIToken(w http.ResponseWriter, r *http.Request) {
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	result, err := h.apiTokens.Get(r.Context(), r.URL.Query().Get("app_key"), id)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

// Management-created tokens are application-scoped service credentials. Target
// services belong in permission policies, not in a separate owner selector.
func (h *Handler) normalizeServiceOwner(appKey string, in *biz.APITokenInput) error {
	ownerType := strings.ToLower(strings.TrimSpace(in.OwnerType))
	switch ownerType {
	case "":
		ownerType = "service"
	case "service":
	case "user":
		return fmt.Errorf("%w: api tokens must be owned by a service, not a user", biz.ErrInvalidArgument)
	default:
		return fmt.Errorf("%w: owner_type must be service", biz.ErrInvalidArgument)
	}
	in.OwnerType, in.OwnerID = "service", strings.TrimSpace(appKey)
	return nil
}

func (h *Handler) CreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var in biz.APITokenInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := h.normalizeServiceOwner(r.URL.Query().Get("app_key"), &in); err != nil {
		tokenManagementError(w, err)
		return
	}
	result, err := h.apiTokens.Create(h.auditContext(r), r.URL.Query().Get("app_key"), in)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 201, result)
}

func (h *Handler) UpdateAPIToken(w http.ResponseWriter, r *http.Request) {
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	var in biz.APITokenInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.apiTokens.Update(h.auditContext(r), r.URL.Query().Get("app_key"), id, in)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) SetAPITokenStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if in.Status != "active" && in.Status != "disabled" {
		tokenHTTPError(w, 400, "INVALID_STATUS")
		return
	}
	result, err := h.apiTokens.SetStatus(h.auditContext(r), r.URL.Query().Get("app_key"), id, in.Status)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) RevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	result, err := h.apiTokens.SetStatus(h.auditContext(r), r.URL.Query().Get("app_key"), id, "revoked")
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) RotateAPIToken(w http.ResponseWriter, r *http.Request) {
	id, ok := tokenPathID(w, r)
	if !ok {
		return
	}
	var in struct {
		RevokeOld bool `json:"revoke_old"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.apiTokens.Rotate(h.auditContext(r), r.URL.Query().Get("app_key"), id, in.RevokeOld)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	writeJSON(w, 201, result)
}

// Internal token validation uses an independent startup credential, never an
// API Token or a remote identity call. Each caller can inspect only its service.
func (h *Handler) AuthorizeAPIToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	provided := r.Header.Get("X-Internal-Service-Key")
	service := ""
	if provided != "" {
		providedHash := sha256.Sum256([]byte(provided))
		for _, caller := range h.apiTokenConfig.InternalCallers {
			keyHash := sha256.Sum256([]byte(caller.Key))
			if caller.Key != "" && subtle.ConstantTimeCompare(providedHash[:], keyHash[:]) == 1 {
				service = caller.ServiceResource
			}
		}
	}
	if service == "" {
		tokenHTTPError(w, 401, "INTERNAL_AUTHENTICATION_REQUIRED")
		return
	}
	if h.apiTokens == nil {
		tokenHTTPError(w, 503, "TOKEN_SERVICE_UNAVAILABLE")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var in struct {
		Token string `json:"token"`
		biz.APITokenTarget
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if in.ServiceResource != service {
		tokenHTTPError(w, 403, "INTERNAL_SERVICE_SCOPE_DENIED")
		return
	}
	decision, err := h.apiTokens.Authorize(r.Context(), in.Token, in.APITokenTarget)
	if err != nil {
		tokenManagementError(w, err)
		return
	}
	// Caller authentication errors are HTTP errors; a successfully evaluated
	// external credential always gets a decision, including invalid/forbidden.
	writeRawJSON(w, 200, decision)
}

type apiTokenContextKey struct{}

func apiTokenFromRequest(r *http.Request) (biz.APITokenDecision, bool) {
	v, ok := r.Context().Value(apiTokenContextKey{}).(biz.APITokenDecision)
	return v, ok
}

func apiTokenBearer(r *http.Request) (string, bool) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return fields[1], true
	}
	return "", false
}

func supportsLocalAPIToken(method, path string) bool {
	read := method == http.MethodGet || method == http.MethodHead
	if !read && strings.HasPrefix(path, "/v1/rbac-v1/endpoints") {
		return false // Shared catalog writes affect other applications.
	}
	return strings.HasPrefix(path, "/v1/rbac-v1/") || strings.HasPrefix(path, "/v1/applications/") || (read && (path == "/v1/applications" || path == "/v1/service-resources"))
}

// API Token support wraps only app-scoped APIs. Global mutations and token
// management remain protected by the existing human identity middleware.
func (h *Handler) tokenOrIdentity(identity func(http.Handler) http.Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, bearer := apiTokenBearer(r)
		if !bearer || !strings.HasPrefix(raw, "nxa_") {
			identity(next).ServeHTTP(w, r)
			return
		}
		if h.apiTokens == nil {
			tokenHTTPError(w, 503, "TOKEN_SERVICE_UNAVAILABLE")
			return
		}
		// Only these handlers enforce application boundaries. Legacy/global
		// catalog writes must not be authorized using an app-scoped token.
		path := r.URL.Path
		appKey := r.URL.Query().Get("app_key")
		appRoute := strings.HasPrefix(path, "/v1/applications/")
		if appRoute {
			appKey = r.PathValue("key")
		}
		if !supportsLocalAPIToken(r.Method, path) {
			tokenHTTPError(w, 403, "API_TOKEN_ROUTE_NOT_SUPPORTED")
			return
		}
		decision, err := h.apiTokens.Authorize(r.Context(), raw, biz.APITokenTarget{AppKey: appKey, ServiceResource: h.apiTokenConfig.ServiceResource, Method: r.Method, Path: r.URL.EscapedPath()})
		if err != nil {
			tokenManagementError(w, err)
			return
		}
		if !decision.Active {
			tokenHTTPError(w, 401, "INVALID_API_TOKEN")
			return
		}
		if !decision.Allowed {
			tokenHTTPError(w, 403, "API_TOKEN_PERMISSION_DENIED")
			return
		}
		ctx := context.WithValue(r.Context(), apiTokenContextKey{}, decision)
		ctx = biz.WithAuditActor(ctx, biz.AuditActor{ID: "api-token:" + decision.TokenID.String(), Name: decision.OwnerID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
