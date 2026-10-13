package httpserver

import "net/http"

// NewServer 注册健康检查、认证和权限管理路由。
func NewServer(handler *Handler, authMiddleware ...func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /swagger/", SwaggerUI())
	mux.HandleFunc("GET /swagger/openapi.json", handler.OpenAPIDocument)
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/index.html", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /auth/config", handler.AuthConfig)
	mux.HandleFunc("GET /auth/login", handler.AuthLogin)
	mux.HandleFunc("GET /signin-oidc", handler.AuthCallback)
	mux.HandleFunc("GET /auth/me", handler.AuthMe)
	mux.HandleFunc("POST /auth/logout", handler.AuthLogout)

	identity := func(next http.Handler) http.Handler {
		if len(authMiddleware) > 0 && authMiddleware[0] != nil {
			return authMiddleware[0](next)
		}
		return next
	}
	protect := func(next http.Handler) http.Handler { return handler.tokenOrIdentity(identity, next) }
	manageTokens := func(next http.Handler) http.Handler { return identity(handler.tokenManagement(next)) }
	mux.Handle("GET /v1/api-tokens", manageTokens(http.HandlerFunc(handler.ListAPITokens)))
	mux.Handle("POST /v1/api-tokens", manageTokens(http.HandlerFunc(handler.CreateAPIToken)))
	mux.Handle("GET /v1/api-tokens/{id}", manageTokens(http.HandlerFunc(handler.GetAPIToken)))
	mux.Handle("PUT /v1/api-tokens/{id}", manageTokens(http.HandlerFunc(handler.UpdateAPIToken)))
	mux.Handle("POST /v1/api-tokens/{id}/status", manageTokens(http.HandlerFunc(handler.SetAPITokenStatus)))
	mux.Handle("POST /v1/api-tokens/{id}/revoke", manageTokens(http.HandlerFunc(handler.RevokeAPIToken)))
	mux.Handle("POST /v1/api-tokens/{id}/rotate", manageTokens(http.HandlerFunc(handler.RotateAPIToken)))
	mux.Handle("GET /v1/api-permission-groups", manageTokens(http.HandlerFunc(handler.ListAPIPermissionGroups)))
	mux.Handle("POST /v1/api-permission-groups", manageTokens(http.HandlerFunc(handler.SaveAPIPermissionGroup)))
	mux.Handle("DELETE /v1/api-permission-groups/{id}", manageTokens(http.HandlerFunc(handler.DeleteAPIPermissionGroup)))
	mux.HandleFunc("POST /internal/v1/api-tokens/authorize", handler.AuthorizeAPIToken)
	mux.Handle("/v1/", protect(http.NotFoundHandler()))
	mux.Handle("POST /v1/authorize", http.HandlerFunc(handler.AuthorizeRBAC))
	mux.Handle("GET /v1/applications", protect(http.HandlerFunc(handler.ListApplications)))
	mux.Handle("POST /v1/applications", protect(http.HandlerFunc(handler.SaveApplication)))
	mux.Handle("PUT /v1/applications/{key}", protect(http.HandlerFunc(handler.SaveApplication)))
	mux.Handle("GET /v1/applications/{key}/service-resources", protect(http.HandlerFunc(handler.ListApplicationServices)))
	mux.Handle("PUT /v1/applications/{key}/service-resources", protect(http.HandlerFunc(handler.ReplaceApplicationServices)))
	mux.Handle("GET /v1/rbac-v1/menus", protect(http.HandlerFunc(handler.ListApplicationMenus)))
	mux.Handle("POST /v1/rbac-v1/menus", protect(http.HandlerFunc(handler.SaveApplicationMenu)))
	mux.Handle("DELETE /v1/rbac-v1/menus/{id}", protect(http.HandlerFunc(handler.DeleteApplicationMenu)))
	mux.Handle("GET /v1/rbac-v1/subject-menus", protect(http.HandlerFunc(handler.SubjectApplicationMenus)))
	mux.Handle("GET /v1/rbac-v1/endpoints", protect(http.HandlerFunc(handler.ListRBACV1Endpoints)))
	mux.Handle("POST /v1/rbac-v1/endpoints", protect(http.HandlerFunc(handler.SaveRBACV1Endpoint)))
	mux.Handle("POST /v1/rbac-v1/endpoints/import-swagger", protect(http.HandlerFunc(handler.ImportRBACV1Swagger)))
	mux.Handle("DELETE /v1/rbac-v1/endpoints/{id}", protect(http.HandlerFunc(handler.DeleteRBACV1Endpoint)))
	mux.Handle("GET /v1/rbac-v1/roles", protect(http.HandlerFunc(handler.ListRBACV1Roles)))
	mux.Handle("POST /v1/rbac-v1/roles", protect(http.HandlerFunc(handler.SaveRBACV1Role)))
	mux.Handle("DELETE /v1/rbac-v1/roles/{id}", protect(http.HandlerFunc(handler.DeleteRBACV1Role)))
	mux.Handle("GET /v1/rbac-v1/subject-roles", protect(http.HandlerFunc(handler.ListRBACV1SubjectRoles)))
	mux.Handle("PUT /v1/rbac-v1/subject-roles", protect(http.HandlerFunc(handler.ReplaceRBACV1SubjectRoles)))
	mux.Handle("POST /v1/rbac-v1/releases", protect(http.HandlerFunc(handler.PublishRBACV1)))
	mux.Handle("GET /v1/rbac-v1/releases", protect(http.HandlerFunc(handler.ListRBACV1Releases)))
	mux.Handle("POST /v1/rbac-v1/simulate", protect(http.HandlerFunc(handler.SimulateRBACV1)))
	mux.Handle("POST /v1/service-resources", protect(http.HandlerFunc(handler.CreateServiceResource)))
	mux.Handle("GET /v1/service-resources", protect(http.HandlerFunc(handler.ListServiceResources)))
	mux.Handle("GET /v1/service-resources/{key}", protect(http.HandlerFunc(handler.GetServiceResource)))
	mux.Handle("PUT /v1/service-resources/{key}", protect(http.HandlerFunc(handler.UpdateServiceResource)))
	mux.Handle("DELETE /v1/service-resources/{key}", protect(http.HandlerFunc(handler.DeleteServiceResource)))
	mux.Handle("POST /v1/authorization/api-endpoints/import-swagger", protect(http.HandlerFunc(handler.ImportSwaggerAPIEndpoints)))
	mux.Handle("POST /v1/authorization/api-endpoints", protect(http.HandlerFunc(handler.CreateAuthorizationAPIEndpoint)))
	mux.Handle("GET /v1/authorization/api-endpoints", protect(http.HandlerFunc(handler.ListAuthorizationAPIEndpoints)))
	mux.Handle("GET /v1/authorization/api-endpoints/{id}", protect(http.HandlerFunc(handler.GetAuthorizationAPIEndpoint)))
	mux.Handle("PUT /v1/authorization/api-endpoints/{id}", protect(http.HandlerFunc(handler.UpdateAuthorizationAPIEndpoint)))
	mux.Handle("DELETE /v1/authorization/api-endpoints/{id}", protect(http.HandlerFunc(handler.DeleteAuthorizationAPIEndpoint)))

	return mux
}
