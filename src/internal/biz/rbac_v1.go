package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RBACEndpoint struct {
	ID               uuid.UUID  `json:"id"`
	ServiceResource  string     `json:"service_resource"`
	EndpointKey      string     `json:"endpoint_key"`
	Method           string     `json:"method"`
	PathTemplate     string     `json:"path_template"`
	Module           string     `json:"module"`
	Summary          string     `json:"summary"`
	Source           string     `json:"source"`
	Enabled          bool       `json:"enabled"`
	LastDiscoveredAt *time.Time `json:"last_discovered_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RBACRole struct {
	ID            uuid.UUID   `json:"id"`
	ApplicationID uuid.UUID   `json:"application_id"`
	Code          string      `json:"code"`
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Enabled       bool        `json:"enabled"`
	EndpointIDs   []uuid.UUID `json:"endpoint_ids"`
	MenuIDs       []uuid.UUID `json:"menu_ids"`
}

type SubjectRoleBinding struct {
	ID            uuid.UUID `json:"id"`
	ApplicationID uuid.UUID `json:"application_id"`
	Subject       string    `json:"subject"`
	Domain        string    `json:"domain"`
	RoleID        uuid.UUID `json:"role_id"`
}

type RBACPage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type RBACRelease struct {
	ID            uuid.UUID      `json:"id"`
	ApplicationID uuid.UUID      `json:"application_id"`
	Version       int64          `json:"version"`
	Status        string         `json:"status"`
	Description   string         `json:"description"`
	Checksum      string         `json:"checksum"`
	PublishedBy   string         `json:"published_by"`
	PublishedAt   time.Time      `json:"published_at"`
	Snapshot      map[string]any `json:"snapshot,omitempty"`
}
type RBACDecision struct {
	Allowed            bool     `json:"allowed"`
	Effect             string   `json:"effect"`
	Reason             string   `json:"reason"`
	ReleaseVersion     int64    `json:"release_version"`
	MatchedPermissions []string `json:"matched_permissions,omitempty"`
	MatchedRoles       []string `json:"matched_roles,omitempty"`
}

// RBACV1Repository is the RDS control-plane boundary. All writes are scoped
// to an application; endpoint catalog entries remain service scoped.
type RBACV1Repository interface {
	ListEndpoints(context.Context, string) ([]RBACEndpoint, error)
	SaveEndpoint(context.Context, string, RBACEndpoint) (RBACEndpoint, error)
	DeleteEndpoint(context.Context, string, uuid.UUID) error
	ListRoles(context.Context, string) ([]RBACRole, error)
	SaveRole(context.Context, string, RBACRole) (RBACRole, error)
	DeleteRole(context.Context, string, uuid.UUID) error
	ListSubjectRoleBindings(context.Context, string) ([]SubjectRoleBinding, error)
	ReplaceSubjectRoles(context.Context, string, string, string, []uuid.UUID) error
	BuildRBACSnapshot(context.Context, string) (map[string]any, error)
	CreateRBACRelease(context.Context, string, string, string, map[string]any) (RBACRelease, error)
	ListRBACReleases(context.Context, string) ([]RBACRelease, error)
	GetActiveRBACRelease(context.Context, string) (RBACRelease, error)
}

type RBACV1Service struct{ repository RBACV1Repository }

type RBACV1SwaggerImportResult struct {
	Total   int `json:"total"`
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

func NewRBACV1Service(repository RBACV1Repository) *RBACV1Service {
	return &RBACV1Service{repository: repository}
}

func (s *RBACV1Service) SaveEndpoint(ctx context.Context, serviceResource string, value RBACEndpoint) (RBACEndpoint, error) {
	if strings.TrimSpace(value.Method) == "" || !strings.HasPrefix(strings.TrimSpace(value.PathTemplate), "/") {
		return RBACEndpoint{}, fmt.Errorf("%w: method and absolute path_template are required", ErrInvalidArgument)
	}
	return s.repository.SaveEndpoint(ctx, normalizeRBACScope(serviceResource), value)
}
func (s *RBACV1Service) DeleteEndpoint(ctx context.Context, serviceResource string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: endpoint id is required", ErrInvalidArgument)
	}
	return s.repository.DeleteEndpoint(ctx, normalizeRBACScope(serviceResource), id)
}
func (s *RBACV1Service) ImportSwagger(ctx context.Context, serviceResource, rawURL string) (RBACV1SwaggerImportResult, error) {
	scope := normalizeRBACScope(serviceResource)
	operations, err := ReadSwaggerEndpoints(ctx, rawURL)
	if err != nil {
		return RBACV1SwaggerImportResult{}, err
	}
	existing, err := s.repository.ListEndpoints(ctx, scope)
	if err != nil {
		return RBACV1SwaggerImportResult{}, err
	}
	routes := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		routes[item.Method+"\x00"+item.PathTemplate] = struct{}{}
	}
	result := RBACV1SwaggerImportResult{Total: len(operations)}
	for _, operation := range operations {
		key := operation.Method + "\x00" + operation.Path
		if _, exists := routes[key]; exists {
			result.Skipped++
			continue
		}
		if _, err := s.SaveEndpoint(ctx, scope, RBACEndpoint{Method: operation.Method, PathTemplate: operation.Path, Module: operation.Module, Summary: operation.Summary, Source: "swagger", Enabled: true}); err != nil {
			return result, err
		}
		routes[key] = struct{}{}
		result.Created++
	}
	return result, nil
}
func (s *RBACV1Service) ListRoles(ctx context.Context, serviceResource string) ([]RBACRole, error) {
	return s.repository.ListRoles(ctx, normalizeRBACScope(serviceResource))
}
func (s *RBACV1Service) SaveRole(ctx context.Context, serviceResource string, value RBACRole) (RBACRole, error) {
	if strings.TrimSpace(value.Code) == "" || strings.TrimSpace(value.Name) == "" {
		return RBACRole{}, fmt.Errorf("%w: role code and name are required", ErrInvalidArgument)
	}
	return s.repository.SaveRole(ctx, normalizeRBACScope(serviceResource), value)
}
func (s *RBACV1Service) DeleteRole(ctx context.Context, serviceResource string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: role id is required", ErrInvalidArgument)
	}
	return s.repository.DeleteRole(ctx, normalizeRBACScope(serviceResource), id)
}
func (s *RBACV1Service) ListSubjectRoleBindings(ctx context.Context, serviceResource string) ([]SubjectRoleBinding, error) {
	return s.repository.ListSubjectRoleBindings(ctx, normalizeRBACScope(serviceResource))
}
func (s *RBACV1Service) ReplaceSubjectRoles(ctx context.Context, serviceResource, subject, domain string, roleIDs []uuid.UUID) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(domain) == "" {
		return fmt.Errorf("%w: subject and domain are required", ErrInvalidArgument)
	}
	return s.repository.ReplaceSubjectRoles(ctx, normalizeRBACScope(serviceResource), strings.TrimSpace(subject), strings.TrimSpace(domain), roleIDs)
}

func (s *RBACV1Service) Publish(ctx context.Context, scope, description, actor string) (RBACRelease, error) {
	if strings.TrimSpace(scope) == "" {
		return RBACRelease{}, fmt.Errorf("%w: app_key is required", ErrInvalidArgument)
	}
	if repo, ok := s.repository.(interface {
		PublishApplication(context.Context, string, string, string) (RBACRelease, error)
	}); ok {
		return repo.PublishApplication(ctx, scope, strings.TrimSpace(description), actor)
	}
	snapshot, err := s.repository.BuildRBACSnapshot(ctx, normalizeRBACScope(scope))
	if err != nil {
		return RBACRelease{}, err
	}
	return s.repository.CreateRBACRelease(ctx, normalizeRBACScope(scope), strings.TrimSpace(description), actor, snapshot)
}
func (s *RBACV1Service) Releases(ctx context.Context, scope string) ([]RBACRelease, error) {
	return s.repository.ListRBACReleases(ctx, normalizeRBACScope(scope))
}
func (s *RBACV1Service) Authorize(ctx context.Context, scope string, input map[string]any) (RBACDecision, error) {
	if strings.TrimSpace(scope) == "" {
		return RBACDecision{}, fmt.Errorf("%w: app_key is required", ErrInvalidArgument)
	}
	service, _ := input["service_resource"].(string)
	subject, _ := input["subject"].(string)
	domain, _ := input["domain"].(string)
	method, _ := input["method"].(string)
	path, _ := input["path"].(string)
	if strings.TrimSpace(service) == "" || strings.TrimSpace(subject) == "" || strings.TrimSpace(domain) == "" || strings.TrimSpace(method) == "" || !strings.HasPrefix(path, "/") {
		return RBACDecision{}, fmt.Errorf("%w: service_resource, subject, domain, method and absolute path are required", ErrInvalidArgument)
	}
	release, err := s.repository.GetActiveRBACRelease(ctx, normalizeRBACScope(scope))
	if err != nil {
		return RBACDecision{}, err
	}
	return evaluateRBACSnapshot(release, input), nil
}

func evaluateRBACSnapshot(release RBACRelease, input map[string]any) RBACDecision {
	d := RBACDecision{Effect: "deny", ReleaseVersion: release.Version, Reason: "denied"}
	snap := release.Snapshot
	subject, _ := input["subject"].(string)
	domain, _ := input["domain"].(string)
	method, _ := input["method"].(string)
	path, _ := input["path"].(string)
	service, _ := input["service_resource"].(string)
	if service == "" {
		d.Reason = "service_resource_required"
		return d
	}
	endpoints, _ := snap["endpoints"].([]any)

	var endpointID string
	bestSpecificity := -1
	endpointEnabled := false
	ambiguous := false
	for _, raw := range endpoints {
		e, _ := raw.(map[string]any)
		template, _ := e["path_template"].(string)
		if e["service_resource"] != service || !strings.EqualFold(fmt.Sprint(e["method"]), method) || !matchesEndpointPath(template, path) {
			continue
		}
		specificity := 0
		for _, segment := range strings.Split(template, "/") {
			if !(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) {
				specificity++
			}
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			endpointID = fmt.Sprint(e["id"])
			endpointEnabled, _ = e["enabled"].(bool)
			ambiguous = false
		} else if specificity == bestSpecificity && endpointID != fmt.Sprint(e["id"]) {
			ambiguous = true
		}
	}
	if ambiguous {
		d.Reason = "ambiguous_endpoint"
		return d
	}
	if endpointID != "" && !endpointEnabled {
		d.Reason = "endpoint_disabled"
		return d
	}

	if endpointID == "" {
		d.Reason = "endpoint_not_found"
		return d
	}
	roles, _ := snap["roles"].([]any)
	bindings, _ := snap["subject_roles"].([]any)
	roleNames := []string{}
	ownedEndpoints := map[string]bool{}
	for _, raw := range bindings {
		b, _ := raw.(map[string]any)
		if fmt.Sprint(b["subject"]) != subject || fmt.Sprint(b["domain"]) != domain {
			continue
		}
		for _, rr := range roles {
			ro, _ := rr.(map[string]any)
			if fmt.Sprint(ro["id"]) == fmt.Sprint(b["role_id"]) {
				roleNames = append(roleNames, fmt.Sprint(ro["code"]))
				if enabled, _ := ro["enabled"].(bool); !enabled {
					continue
				}
				endpointIDs, _ := ro["endpoint_ids"].([]any)
				for _, rawEndpointID := range endpointIDs {
					ownedEndpoints[fmt.Sprint(rawEndpointID)] = true
				}
			}
		}
	}
	d.MatchedRoles = roleNames
	if !ownedEndpoints[endpointID] {
		d.Reason = "endpoint_not_granted"
		return d
	}
	d.Effect = "allow"
	d.Allowed = true
	d.Reason = "allowed"
	return d
}

// matchesEndpointPath recognizes OpenAPI path parameters as exactly one
// non-empty segment. It intentionally does not treat a parameter as a glob,
// so /orders/{id} cannot authorize /orders/1/items.
func matchesEndpointPath(template, path string) bool {
	template = strings.TrimSpace(template)
	path = strings.TrimSpace(path)
	if template == path {
		return true
	}
	templateParts := strings.Split(strings.Trim(template, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(templateParts) != len(pathParts) {
		return false
	}
	for index, part := range templateParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			if pathParts[index] == "" {
				return false
			}
			continue
		}
		if part != pathParts[index] {
			return false
		}
	}
	return true
}

func normalizeRBACScope(value string) string { return strings.TrimSpace(value) }
func normalizeRBACPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
