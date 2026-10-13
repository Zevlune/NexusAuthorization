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
		if operation.path == "/v1/authorize" {
			definition["security"] = []map[string][]string{{"serviceCredential": {}}}
		} else if supportsLocalAPIToken(operation.method, operation.path) {
			definition["security"] = []map[string][]string{{"cookieAuth": {}}, {"apiTokenAuth": {}}}
			definition["description"] = "API Token access requires the token's app_key and an explicit endpoint grant. Tenant/project-scoped tokens require a trusted business context; these local management routes do not supply it."
		}

		parameters := []map[string]any{}
		if operation.path == "/v1/applications" && operation.method == http.MethodGet || operation.path == "/v1/service-resources" && operation.method == http.MethodGet {
			parameters = append(parameters, map[string]any{"name": "app_key", "in": "query", "required": false, "description": "Required when authenticating with an API Token.", "schema": map[string]any{"type": "string"}})
		}
		for _, segment := range strings.Split(operation.path, "/") {
			if strings.HasPrefix(segment, "{") {
				parameters = append(parameters, map[string]any{"name": strings.Trim(segment, "{}"), "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			}
		}
		if strings.HasPrefix(operation.path, "/v1/rbac-v1/") {
			parameters = append(parameters, map[string]any{"name": "app_key", "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if strings.HasPrefix(operation.path, "/v1/api-tokens") {
			parameters = append(parameters, map[string]any{"name": "app_key", "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
			if operation.path == "/v1/api-tokens" && operation.method == http.MethodGet {
				parameters = append(parameters,
					map[string]any{"name": "search", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					map[string]any{"name": "status", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"active", "disabled", "revoked", "expired"}}},
					map[string]any{"name": "page", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1}},
					map[string]any{"name": "page_size", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1}},
				)
			}
		}
		if strings.HasPrefix(operation.path, "/v1/api-permission-groups") {
			parameters = append(parameters,
				map[string]any{"name": "app_key", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
				map[string]any{"name": "service_resource", "in": "query", "required": operation.method != http.MethodGet, "schema": map[string]any{"type": "string"}},
			)
			if operation.method == http.MethodPost {
				definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APIPermissionGroup")
			}
			definition["description"] = "Administrator identity required. Group mappings are shared by applications linked to the service and affect existing policy tokens."
		}
		if strings.HasPrefix(operation.path, "/v1/rbac-v1/endpoints") {
			parameters = append(parameters, map[string]any{"name": "service_resource", "in": "query", "required": operation.method != http.MethodGet, "schema": map[string]any{"type": "string"}})
		}
		if operation.path == "/v1/rbac-v1/subject-menus" {
			for _, name := range []string{"subject", "domain"} {
				parameters = append(parameters, map[string]any{"name": name, "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
			}
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
		switch {
		case operation.path == "/v1/api-permission-groups" && operation.method == http.MethodPost:
			definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APIPermissionGroup")
		case operation.path == "/v1/api-tokens" && operation.method == http.MethodPost:
			definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APITokenCreateInput")
		case operation.path == "/v1/api-tokens/{id}" && operation.method == http.MethodPut:
			definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APITokenInput")
		case operation.path == "/v1/api-tokens/{id}/status":
			definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APITokenStatusInput")
		case operation.path == "/v1/api-tokens/{id}/rotate":
			definition["requestBody"] = apiTokenRequestBody("#/components/schemas/APITokenRotateInput")
		}
		if strings.HasPrefix(operation.path, "/v1/api-tokens") {
			resultSchema := map[string]any{"$ref": "#/components/schemas/APIToken"}
			if operation.path == "/v1/api-tokens" && operation.method == http.MethodGet {
				resultSchema = map[string]any{"$ref": "#/components/schemas/APITokenPage"}
			} else if (operation.path == "/v1/api-tokens" && operation.method == http.MethodPost) || strings.HasSuffix(operation.path, "/rotate") {
				resultSchema = map[string]any{"$ref": "#/components/schemas/APITokenCreated"}
			}
			statusCode := "200"
			if operation.method == http.MethodPost && operation.path != "/v1/api-tokens/{id}/status" && operation.path != "/v1/api-tokens/{id}/revoke" {
				statusCode = "201"
			}
			definition["responses"].(map[string]any)[statusCode] = map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": apiResponseSchema(resultSchema)}}}
			if statusCode == "200" {
				delete(definition["responses"].(map[string]any), "201")
			}
		}

		if operation.path == "/v1/authorize" {
			properties := map[string]any{}
			required := []string{"appKey", "serviceResource", "subject", "domain", "method", "path"}
			for _, name := range required {
				properties[name] = map[string]any{"type": "string"}
			}
			definition["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": false}}}}
			definition["responses"].(map[string]any)["200"] = map[string]any{"description": "Authorization decision", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"allowed": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}, "release_version": map[string]any{"type": "integer"}}}}}}
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
				"apiTokenAuth":      map[string]any{"type": "http", "scheme": "bearer", "description": "Opaque nxa_ API Token for supported application-scoped APIs; cannot manage tokens or shared catalogs."},
				"serviceCredential": map[string]any{"type": "http", "scheme": "bearer", "description": "Configured service-to-service credential for PEP calls."},
			},
			"schemas": map[string]any{
				"APIResponse":         apiResponseSchema(map[string]any{}),
				"APITokenPermission":  apiTokenPermissionSchema(),
				"APITokenPolicy":      apiTokenPolicySchema(),
				"APIPermissionGroup":  apiPermissionGroupSchema(),
				"APIToken":            apiTokenSchema(),
				"APITokenInput":       apiTokenInputSchema(),
				"APITokenCreateInput": apiTokenCreateInputSchema(),
				"APITokenCreated":     apiTokenCreatedSchema(),
				"APITokenPage":        apiTokenPageSchema(),
				"APITokenStatusInput": map[string]any{"type": "object", "required": []string{"status"}, "properties": map[string]any{"status": map[string]any{"type": "string", "enum": []string{"active", "disabled"}}}, "additionalProperties": false},
				"APITokenRotateInput": map[string]any{"type": "object", "properties": map[string]any{"revoke_old": map[string]any{"type": "boolean", "default": false}}, "additionalProperties": false},
			},
		},
	}
}

func apiTokenRequestBody(ref string) map[string]any {
	return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": ref}}}}
}

func apiResponseSchema(result any) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"success": map[string]any{"type": "boolean"}, "errorCode": map[string]any{"nullable": true}, "errorMessage": map[string]any{"nullable": true}, "result": result}}
}

func apiTokenPermissionSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"endpoint_id", "service_resource", "action"}, "properties": map[string]any{
		"endpoint_id": map[string]any{"type": "string", "format": "uuid"}, "service_resource": map[string]any{"type": "string"},
		"action":    map[string]any{"type": "string", "enum": []string{"read", "write"}, "description": "Permission scope derived from the selected endpoint HTTP method."},
		"tenant_id": map[string]any{"type": "string"}, "project_id": map[string]any{"type": "string"},
		"method": map[string]any{"type": "string", "readOnly": true}, "path_template": map[string]any{"type": "string", "readOnly": true}, "summary": map[string]any{"type": "string", "readOnly": true},
	}, "additionalProperties": false}
}

func apiTokenSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "format": "uuid"}, "app_key": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "token_prefix": map[string]any{"type": "string"},
		"owner_type": map[string]any{"type": "string", "enum": []string{"user", "service"}}, "owner_id": map[string]any{"type": "string"},
		"status": map[string]any{"type": "string", "enum": []string{"active", "disabled", "revoked"}}, "expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
		"last_used_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "created_by": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}, "revoked_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
		"permissions": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/APITokenPermission"}},
		"policies":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/APITokenPolicy"}},
	}, "additionalProperties": false}
}

func apiTokenInputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"name"}, "properties": map[string]any{
		"name": map[string]any{"type": "string", "maxLength": 200}, "owner_type": map[string]any{"type": "string", "enum": []string{"user", "service"}}, "owner_id": map[string]any{"type": "string", "maxLength": 200},
		"expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "permissions": map[string]any{"type": "array", "minItems": 1, "maxItems": 1000, "items": map[string]any{"$ref": "#/components/schemas/APITokenPermission"}},
		"policies": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"$ref": "#/components/schemas/APITokenPolicy"}},
	}, "additionalProperties": false}
}

func apiTokenCreateInputSchema() map[string]any {
	schema := apiTokenInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["owner_type"] = map[string]any{"type": "string", "enum": []string{"service"}, "default": "service"}
	properties["owner_id"] = map[string]any{"type": "string", "description": "Compatibility field; management creation derives identity from app_key. No caller service selection is required."}
	schema["required"] = []string{"name"}
	schema["description"] = "Select maintained permission groups in policies. Legacy permissions-only requests remain supported. Do not mix both representations."
	return schema
}

func apiTokenPolicySchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"service_resource", "permission_group_ids"}, "properties": map[string]any{
		"id":               map[string]any{"type": "string", "format": "uuid"},
		"effect":           map[string]any{"type": "string", "enum": []string{"allow"}, "default": "allow"},
		"service_resource": map[string]any{"type": "string"},
		"tenant_id":        map[string]any{"type": "string"}, "project_id": map[string]any{"type": "string"},
		"permission_group_ids": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "format": "uuid"}},
	}, "additionalProperties": false}
}

func apiPermissionGroupSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"code", "name", "action", "endpoint_ids", "enabled"}, "properties": map[string]any{
		"id": map[string]any{"type": "string", "format": "uuid"}, "service_resource": map[string]any{"type": "string"},
		"code": map[string]any{"type": "string", "maxLength": 128}, "name": map[string]any{"type": "string", "maxLength": 200},
		"action": map[string]any{"type": "string", "enum": []string{"read", "write"}}, "enabled": map[string]any{"type": "boolean"},
		"endpoint_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 1000, "items": map[string]any{"type": "string", "format": "uuid"}},
		"created_at":   map[string]any{"type": "string", "format": "date-time", "readOnly": true}, "updated_at": map[string]any{"type": "string", "format": "date-time", "readOnly": true},
	}, "additionalProperties": false}
}

func apiTokenCreatedSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"token", "raw_token"}, "properties": map[string]any{
		"token":     map[string]any{"$ref": "#/components/schemas/APIToken"},
		"raw_token": map[string]any{"type": "string", "description": "The complete credential is returned only once by create or rotate. Store it securely; it is never returned by list or detail endpoints."},
	}, "additionalProperties": false}
}

func apiTokenPageSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"items", "total", "page", "page_size"}, "properties": map[string]any{
		"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/APIToken"}}, "total": map[string]any{"type": "integer"}, "page": map[string]any{"type": "integer"}, "page_size": map[string]any{"type": "integer"},
	}, "additionalProperties": false}
}

var openAPIOperations = []openAPIOperation{
	{http.MethodGet, "/v1/api-permission-groups", "API permission groups", "List permission groups for linked services", false},
	{http.MethodPost, "/v1/api-permission-groups", "API permission groups", "Create or update API permission group", true},
	{http.MethodDelete, "/v1/api-permission-groups/{id}", "API permission groups", "Delete unreferenced API permission group", false},
	{http.MethodGet, "/v1/applications", "applications", "List business applications", false},
	{http.MethodPost, "/v1/applications", "applications", "Create business application", true},
	{http.MethodPut, "/v1/applications/{key}", "applications", "Update business application", true},
	{http.MethodGet, "/v1/applications/{key}/service-resources", "applications", "List linked services", false},
	{http.MethodPut, "/v1/applications/{key}/service-resources", "applications", "Replace linked services", true},
	{http.MethodGet, "/v1/api-tokens", "API tokens", "List API tokens", false},
	{http.MethodPost, "/v1/api-tokens", "API tokens", "Create API token", true},
	{http.MethodGet, "/v1/api-tokens/{id}", "API tokens", "Get API token details", false},
	{http.MethodPut, "/v1/api-tokens/{id}", "API tokens", "Update API token", true},
	{http.MethodPost, "/v1/api-tokens/{id}/status", "API tokens", "Set API token status", true},
	{http.MethodPost, "/v1/api-tokens/{id}/revoke", "API tokens", "Revoke API token", false},
	{http.MethodPost, "/v1/api-tokens/{id}/rotate", "API tokens", "Rotate API token", true},
	{http.MethodGet, "/v1/rbac-v1/menus", "menus", "List application menus", false},
	{http.MethodPost, "/v1/rbac-v1/menus", "menus", "Save application menu", true},
	{http.MethodDelete, "/v1/rbac-v1/menus/{id}", "menus", "Delete application menu", false},
	{http.MethodGet, "/v1/rbac-v1/subject-menus", "menus", "Get published menus for a subject", false},
	{"GET", "/healthz", "System", "Health check", false},
	{"GET", "/auth/config", "Authentication", "Get authentication configuration", false},
	{"GET", "/auth/login", "Authentication", "Start sign-in", false},
	{"GET", "/auth/me", "Authentication", "Get current user", false},
	{"POST", "/auth/logout", "Authentication", "Sign out", false},
	{"POST", "/v1/service-resources", "Service resources", "Create service resource", true}, {"GET", "/v1/service-resources", "Service resources", "List service resources", false}, {"GET", "/v1/service-resources/{key}", "Service resources", "Get service resource", false}, {"PUT", "/v1/service-resources/{key}", "Service resources", "Update service resource", true}, {"DELETE", "/v1/service-resources/{key}", "Service resources", "Delete service resource", false},
	{"POST", "/v1/authorize", "Runtime authorization", "Authorize a verified service request", true},
	{"GET", "/v1/rbac-v1/endpoints", "RBAC v1", "List API endpoints", false}, {"POST", "/v1/rbac-v1/endpoints", "RBAC v1", "Create or update an API endpoint", true}, {"POST", "/v1/rbac-v1/endpoints/import-swagger", "RBAC v1", "Import Swagger endpoints", true}, {"DELETE", "/v1/rbac-v1/endpoints/{id}", "RBAC v1", "Delete an API endpoint", false},
	{"GET", "/v1/rbac-v1/roles", "RBAC v1", "List roles and endpoint grants", false}, {"POST", "/v1/rbac-v1/roles", "RBAC v1", "Create or update a role", true}, {"DELETE", "/v1/rbac-v1/roles/{id}", "RBAC v1", "Delete a role", false},
	{"GET", "/v1/rbac-v1/subject-roles", "RBAC v1", "List subject role bindings", false}, {"PUT", "/v1/rbac-v1/subject-roles", "RBAC v1", "Replace subject role bindings", true},
	{"POST", "/v1/rbac-v1/releases", "RBAC v1", "Publish an RBAC release", true}, {"GET", "/v1/rbac-v1/releases", "RBAC v1", "List RBAC releases", false}, {"POST", "/v1/rbac-v1/simulate", "RBAC v1", "Simulate an authorization decision", true},
	{"POST", "/v1/authorization/api-endpoints", "API endpoints", "Create API endpoint", true}, {"GET", "/v1/authorization/api-endpoints", "API endpoints", "List API endpoints", false}, {"POST", "/v1/authorization/api-endpoints/import-swagger", "API endpoints", "Import Swagger endpoints", true}, {"GET", "/v1/authorization/api-endpoints/{id}", "API endpoints", "Get API endpoint", false}, {"PUT", "/v1/authorization/api-endpoints/{id}", "API endpoints", "Update API endpoint", true}, {"DELETE", "/v1/authorization/api-endpoints/{id}", "API endpoints", "Delete API endpoint", false},
}
