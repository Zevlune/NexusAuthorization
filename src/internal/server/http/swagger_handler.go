package httpserver

import (
	"net/http"
	"strings"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// SwaggerUI serves the bundled Swagger UI assets locally. The UI retrieves the
// OpenAPI document from this service, so it works without a browser-side CDN.
// SwaggerUI 返回内嵌静态资源的 Swagger UI 处理器。
func SwaggerUI() http.Handler {
	return httpSwagger.Handler(
		httpSwagger.URL("/swagger/openapi.json"),
		httpSwagger.DocExpansion("list"),
		httpSwagger.PersistAuthorization(true),
	)
}

// OpenAPIDocument 返回权限中心当前公开 HTTP API 的 OpenAPI 文档。
func (h *Handler) OpenAPIDocument(w http.ResponseWriter, _ *http.Request) {
	writeRawJSON(w, http.StatusOK, openAPIDocument())
}

type openAPIOperation struct {
	method  string
	path    string
	tag     string
	summary string
	body    bool
}

// openAPIDocument 生成当前 HTTP API 的 OpenAPI 文档。
func openAPIDocument() map[string]any {
	paths := map[string]any{}
	for _, operation := range openAPIOperations {
		pathItem, ok := paths[operation.path].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			paths[operation.path] = pathItem
		}
		definition := map[string]any{
			"tags":        []string{operation.tag},
			"summary":     operation.summary,
			"operationId": strings.ToLower(operation.method) + "-" + strings.Trim(strings.NewReplacer("/", "-", "{", "", "}", "", ":", "-").Replace(strings.TrimPrefix(operation.path, "/")), "-"),
			"responses": map[string]any{
				"200": map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/APIResponse"}}}},
				"400": map[string]any{"description": "Invalid request"},
				"401": map[string]any{"description": "Authentication required"},
			},
			"security": []map[string][]string{{"cookieAuth": {}}},
		}
		if operation.path == apiPrefix+"/v1/authorize" {
			definition["security"] = []map[string][]string{{"serviceCredential": {}}}
		}

		parameters := []map[string]any{}
		for _, segment := range strings.Split(operation.path, "/") {
			if strings.HasPrefix(segment, "{") {
				parameters = append(parameters, map[string]any{"name": strings.Trim(segment, "{}"), "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			}
		}
		if strings.HasPrefix(operation.path, apiPrefix+"/v1/rbac-v1/") {
			parameters = append(parameters, map[string]any{"name": "app_key", "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if strings.HasPrefix(operation.path, apiPrefix+"/v1/rbac-v1/endpoints") {
			parameters = append(parameters, map[string]any{"name": "service_resource", "in": "query", "required": operation.method != http.MethodGet, "schema": map[string]any{"type": "string"}})
		}
		if operation.path == apiPrefix+"/v1/rbac-v1/subject-menus" {
			for _, name := range []string{"subject"} {
				parameters = append(parameters, map[string]any{"name": name, "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
			}
			parameters = append(parameters, map[string]any{"name": "domain", "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": "Defaults to the empty default domain when omitted"})
		}
		if operation.path == apiPrefix+"/v1/users" {
			parameters = append(parameters,
				map[string]any{"name": "filter", "in": "query", "schema": map[string]any{"type": "string"}, "description": "NexusAuth SCIM filter, e.g. userName eq \"alice\""},
				map[string]any{"name": "startIndex", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "default": 1}},
				map[string]any{"name": "count", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0, "maximum": 200, "default": 100}},
			)
			definition["responses"].(map[string]any)["502"] = map[string]any{"description": "NexusAuth SCIM request failed"}
			definition["responses"].(map[string]any)["503"] = map[string]any{"description": "SCIM user directory is not configured"}
		}
		if len(parameters) > 0 {
			definition["parameters"] = parameters
		}
		if operation.method == http.MethodPost {
			definition["responses"].(map[string]any)["201"] = definition["responses"].(map[string]any)["200"]
		}
		if operation.body {
			definition["requestBody"] = map[string]any{
				"required": true,
				"content":  map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": true}}},
			}
		}

		if operation.path == apiPrefix+"/v1/authorize" {
			properties := map[string]any{
				"domain": map[string]any{"type": "string", "description": "Defaults to the empty default domain when omitted"},
			}
			required := []string{"appKey", "serviceResource", "subject", "method", "path"}
			for _, name := range required {
				properties[name] = map[string]any{"type": "string"}
			}
			definition["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": false}}}}
			definition["responses"].(map[string]any)["200"] = map[string]any{"description": "Authorization decision", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"allowed": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}}}}}}
		}
		pathItem[strings.ToLower(operation.method)] = definition
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Permission Center API", "version": "1.0.0", "description": "Permission Center management API."},
		"servers": []map[string]string{{"url": "/", "description": "Current server"}},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"cookieAuth":        map[string]any{"type": "apiKey", "in": "cookie", "name": "nexusauthorization_session"},
				"serviceCredential": map[string]any{"type": "http", "scheme": "bearer", "description": "Configured service-to-service credential for PEP calls."},
			},
			"schemas": map[string]any{"APIResponse": map[string]any{"type": "object", "properties": map[string]any{"success": map[string]any{"type": "boolean"}, "errorCode": map[string]any{"nullable": true}, "errorMessage": map[string]any{"nullable": true}, "result": map[string]any{}}}},
		},
	}
}

var openAPIOperations = []openAPIOperation{
	{http.MethodGet, apiPrefix + "/v1/users", "Users", "List NexusAuth SCIM users", false},
	{http.MethodGet, apiPrefix + "/v1/applications", "applications", "List business applications", false},
	{http.MethodPost, apiPrefix + "/v1/applications", "applications", "Create business application", true},
	{http.MethodPut, apiPrefix + "/v1/applications/{key}", "applications", "Update business application", true},
	{http.MethodGet, apiPrefix + "/v1/applications/{key}/service-resources", "applications", "List linked services", false},
	{http.MethodPut, apiPrefix + "/v1/applications/{key}/service-resources", "applications", "Replace linked services", true},
	{http.MethodGet, apiPrefix + "/v1/rbac-v1/menus", "menus", "List application menus", false},
	{http.MethodPost, apiPrefix + "/v1/rbac-v1/menus", "menus", "Save application menu", true},
	{http.MethodDelete, apiPrefix + "/v1/rbac-v1/menus/{id}", "menus", "Delete application menu", false},
	{http.MethodGet, apiPrefix + "/v1/rbac-v1/subject-menus", "menus", "Get currently granted menus for a subject", false},
	{"GET", apiPrefix + "/healthz", "System", "Health check", false},
	{"GET", apiPrefix + "/auth/config", "Authentication", "Get authentication configuration", false},
	{"GET", apiPrefix + "/auth/login", "Authentication", "Start sign-in", false},
	{"GET", apiPrefix + "/auth/me", "Authentication", "Get current user", false},
	{"POST", apiPrefix + "/auth/logout", "Authentication", "Sign out", false},
	{"POST", apiPrefix + "/v1/service-resources", "Service resources", "Create service resource", true}, {"GET", apiPrefix + "/v1/service-resources", "Service resources", "List service resources", false}, {"GET", apiPrefix + "/v1/service-resources/{key}", "Service resources", "Get service resource", false}, {"PUT", apiPrefix + "/v1/service-resources/{key}", "Service resources", "Update service resource", true}, {"DELETE", apiPrefix + "/v1/service-resources/{key}", "Service resources", "Delete service resource", false},
	{"POST", apiPrefix + "/v1/authorize", "Runtime authorization", "Authorize a verified service request", true},
	{"GET", apiPrefix + "/v1/rbac-v1/endpoints", "RBAC v1", "List API endpoints", false}, {"POST", apiPrefix + "/v1/rbac-v1/endpoints", "RBAC v1", "Create or update an API endpoint", true}, {"POST", apiPrefix + "/v1/rbac-v1/endpoints/import-swagger", "RBAC v1", "Import Swagger endpoints", true}, {"DELETE", apiPrefix + "/v1/rbac-v1/endpoints/{id}", "RBAC v1", "Delete an API endpoint", false},
	{"GET", apiPrefix + "/v1/rbac-v1/roles", "RBAC v1", "List roles and endpoint grants", false}, {"POST", apiPrefix + "/v1/rbac-v1/roles", "RBAC v1", "Create or update a role", true}, {"DELETE", apiPrefix + "/v1/rbac-v1/roles/{id}", "RBAC v1", "Delete a role", false},
	{"GET", apiPrefix + "/v1/rbac-v1/subject-roles", "RBAC v1", "List subject role bindings", false}, {"PUT", apiPrefix + "/v1/rbac-v1/subject-roles", "RBAC v1", "Replace subject role bindings", true},
	{"POST", apiPrefix + "/v1/rbac-v1/simulate", "RBAC v1", "Simulate an authorization decision", true},
}
