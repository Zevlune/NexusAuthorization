package httpserver

import "net/http"

// apiPrefix is the canonical prefix for every business, authentication, SCIM,
// and health route. Swagger keeps its own /swagger paths, and the OIDC callback
// stays at /signin-oidc (outside the prefix) so the identity provider's
// registered redirect URI is independent from the API surface.
const apiPrefix = "/api"

// maxRequestBodyBytes bounds every request body; the largest legitimate body is
// a role with thousands of endpoint and menu IDs.
const maxRequestBodyBytes = 4 << 20

// bodyLimitedMux caps request bodies so a client cannot exhaust memory. It
// embeds the mux so callers can still resolve route patterns via Handler.
type bodyLimitedMux struct{ *http.ServeMux }

func (m bodyLimitedMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	m.ServeMux.ServeHTTP(w, r)
}

// NewServer 注册健康检查、认证和权限管理路由。
func NewServer(handler *Handler, authMiddleware ...func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /swagger/", SwaggerUI())
	mux.HandleFunc("GET /swagger/openapi.json", handler.OpenAPIDocument)
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/index.html", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET "+apiPrefix+"/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET "+apiPrefix+"/auth/config", handler.AuthConfig)
	mux.HandleFunc("GET "+apiPrefix+"/auth/login", handler.AuthLogin)
	mux.HandleFunc("GET /signin-oidc", handler.AuthCallback)
	mux.HandleFunc("GET "+apiPrefix+"/auth/me", handler.AuthMe)
	mux.HandleFunc("POST "+apiPrefix+"/auth/logout", handler.AuthLogout)

	protect := func(next http.Handler) http.Handler {
		if len(authMiddleware) > 0 && authMiddleware[0] != nil {
			return authMiddleware[0](next)
		}
		return next
	}
	mux.Handle(apiPrefix+"/v1/", protect(http.NotFoundHandler()))
	mux.Handle("GET "+apiPrefix+"/v1/users", protect(http.HandlerFunc(handler.ListUsers)))
	mux.Handle("POST "+apiPrefix+"/v1/authorize", http.HandlerFunc(handler.AuthorizeRBAC))
	mux.Handle("GET "+apiPrefix+"/v1/applications", protect(http.HandlerFunc(handler.ListApplications)))
	mux.Handle("POST "+apiPrefix+"/v1/applications", protect(http.HandlerFunc(handler.SaveApplication)))
	mux.Handle("PUT "+apiPrefix+"/v1/applications/{key}", protect(http.HandlerFunc(handler.SaveApplication)))
	mux.Handle("GET "+apiPrefix+"/v1/applications/{key}/service-resources", protect(http.HandlerFunc(handler.ListApplicationServices)))
	mux.Handle("PUT "+apiPrefix+"/v1/applications/{key}/service-resources", protect(http.HandlerFunc(handler.ReplaceApplicationServices)))
	mux.Handle("GET "+apiPrefix+"/v1/rbac-v1/menus", protect(http.HandlerFunc(handler.ListApplicationMenus)))
	mux.Handle("POST "+apiPrefix+"/v1/rbac-v1/menus", protect(http.HandlerFunc(handler.SaveApplicationMenu)))
	mux.Handle("DELETE "+apiPrefix+"/v1/rbac-v1/menus/{id}", protect(http.HandlerFunc(handler.DeleteApplicationMenu)))
	mux.Handle("GET "+apiPrefix+"/v1/rbac-v1/subject-menus", protect(http.HandlerFunc(handler.SubjectApplicationMenus)))
	mux.Handle("GET "+apiPrefix+"/v1/rbac-v1/endpoints", protect(http.HandlerFunc(handler.ListRBACV1Endpoints)))
	mux.Handle("POST "+apiPrefix+"/v1/rbac-v1/endpoints", protect(http.HandlerFunc(handler.SaveRBACV1Endpoint)))
	mux.Handle("POST "+apiPrefix+"/v1/rbac-v1/endpoints/import-swagger", protect(http.HandlerFunc(handler.ImportRBACV1Swagger)))
	mux.Handle("DELETE "+apiPrefix+"/v1/rbac-v1/endpoints/{id}", protect(http.HandlerFunc(handler.DeleteRBACV1Endpoint)))
	mux.Handle("GET "+apiPrefix+"/v1/rbac-v1/roles", protect(http.HandlerFunc(handler.ListRBACV1Roles)))
	mux.Handle("POST "+apiPrefix+"/v1/rbac-v1/roles", protect(http.HandlerFunc(handler.SaveRBACV1Role)))
	mux.Handle("DELETE "+apiPrefix+"/v1/rbac-v1/roles/{id}", protect(http.HandlerFunc(handler.DeleteRBACV1Role)))
	mux.Handle("PUT "+apiPrefix+"/v1/rbac-v1/roles/{id}/subjects", protect(http.HandlerFunc(handler.UpdateRBACV1RoleSubjects)))
	mux.Handle("GET "+apiPrefix+"/v1/rbac-v1/subject-roles", protect(http.HandlerFunc(handler.ListRBACV1SubjectRoles)))
	mux.Handle("PUT "+apiPrefix+"/v1/rbac-v1/subject-roles", protect(http.HandlerFunc(handler.ReplaceRBACV1SubjectRoles)))
	mux.Handle("POST "+apiPrefix+"/v1/rbac-v1/simulate", protect(http.HandlerFunc(handler.SimulateRBACV1)))
	mux.Handle("POST "+apiPrefix+"/v1/service-resources", protect(http.HandlerFunc(handler.CreateServiceResource)))
	mux.Handle("GET "+apiPrefix+"/v1/service-resources", protect(http.HandlerFunc(handler.ListServiceResources)))
	mux.Handle("GET "+apiPrefix+"/v1/service-resources/{key}", protect(http.HandlerFunc(handler.GetServiceResource)))
	mux.Handle("PUT "+apiPrefix+"/v1/service-resources/{key}", protect(http.HandlerFunc(handler.UpdateServiceResource)))
	mux.Handle("DELETE "+apiPrefix+"/v1/service-resources/{key}", protect(http.HandlerFunc(handler.DeleteServiceResource)))
	return bodyLimitedMux{mux}
}
