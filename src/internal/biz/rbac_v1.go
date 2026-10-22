package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RBACEndpoint describes one API operation in a shared service resource
// catalog. The endpoint is referenced by roles, while its catalog record is
// scoped by ServiceResource rather than by an individual application.
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

// RBACRole is an application-scoped permission role. EndpointIDs and MenuIDs
// are the grants issued by the role; ID is the role identity returned to clients.
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

// SubjectRoleBinding maps one subject in an optional domain to a role. An empty
// Domain is the default domain; callers should normalize with NormalizeRBACDomain.
type SubjectRoleBinding struct {
	ID            uuid.UUID `json:"id"`
	ApplicationID uuid.UUID `json:"application_id"`
	Subject       string    `json:"subject"`
	Domain        string    `json:"domain"`
	RoleID        uuid.UUID `json:"role_id"`
}

// RBACPage is a bounded page of RBAC list results reported alongside its total
// item count.
type RBACPage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// CurrentPolicy contains current normalized records read consistently from the database.
// Token is an internal cache invalidation identity, never a published version.
type CurrentPolicy struct {
	ApplicationID uuid.UUID      `json:"application_id"`
	Token         string         `json:"token"`
	ServiceKeys   []string       `json:"-"`
	Data          map[string]any `json:"data,omitempty"`
}

// RBACDecision is the authorization outcome returned to callers. MatchedRoles
// is diagnostic only and never widens or narrows the allow/deny result.
type RBACDecision struct {
	Allowed            bool     `json:"allowed"`
	Effect             string   `json:"effect"`
	Reason             string   `json:"reason"`
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
	GetPolicyMetadata(context.Context, string) (CurrentPolicy, error)
	GetCurrentPolicy(context.Context, CurrentPolicy) (CurrentPolicy, error)
}

// RBACV1Service coordinates application, service-resource, role, and binding
// administration, and authorizes requests against the current compiled policy.
type RBACV1Service struct {
	repository            RBACV1Repository
	policies              *rbacPolicyCache
	subjectAPIs           *subjectAPICache
	databaseAuthorization bool
	swaggerFetch          SwaggerFetchPolicy
}

// MaxEndpointKeyLength is the endpoint_key column width.
const MaxEndpointKeyLength = 160

const maxEndpointPathLength = 500

// endpointMethods are the HTTP methods an endpoint may declare.
var endpointMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true, "TRACE": true,
}

// RouteShape reduces a path template to the requests it matches: surrounding
// whitespace and slashes are ignored and every parameter segment is the same
// placeholder. Templates with equal shapes (for example /orders/{id} and
// /orders/{orderId}/) are ambiguous at runtime, so only one may exist per
// service and method.
func RouteShape(template string) string {
	parts := strings.Split(normalizedPolicyPath(template), "/")
	for i, part := range parts {
		if parameterSegment(part) {
			parts[i] = "\x00"
		}
	}
	return strings.Join(parts, "/")
}

// RBACV1SwaggerImportResult reports the endpoint import counts for a Swagger
// operation; Skipped counts operations already present for the service.
type RBACV1SwaggerImportResult struct {
	Total   int `json:"total"`
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

// NewRBACV1Service creates the RBAC control-plane service with a bounded
// per-process cache of compiled current policies.
func NewRBACV1Service(repository RBACV1Repository) *RBACV1Service {
	return &RBACV1Service{repository: repository, policies: newRBACPolicyCache(64), subjectAPIs: newSubjectAPICache(1024)}
}

// WithSwaggerFetchPolicy restricts which addresses Swagger imports may fetch.
func (s *RBACV1Service) WithSwaggerFetchPolicy(policy SwaggerFetchPolicy) *RBACV1Service {
	s.swaggerFetch = policy
	return s
}

// WithDatabaseAuthorization selects current database records for API decisions.
// Configure this at startup only when Redis is explicitly disabled; Redis errors
// must never cause an automatic change of authorization source.
func (s *RBACV1Service) WithDatabaseAuthorization() *RBACV1Service {
	s.databaseAuthorization = true
	return s
}

// SaveEndpoint creates or updates an endpoint in the service catalog. A
// standard HTTP method and an absolute PathTemplate are required; the service
// scope is trimmed.
func (s *RBACV1Service) SaveEndpoint(ctx context.Context, serviceResource string, value RBACEndpoint) (RBACEndpoint, error) {
	if !endpointMethods[strings.ToUpper(strings.TrimSpace(value.Method))] || !strings.HasPrefix(strings.TrimSpace(value.PathTemplate), "/") {
		return RBACEndpoint{}, fmt.Errorf("%w: a standard HTTP method and an absolute path_template are required", ErrInvalidArgument)
	}
	if len([]rune(strings.TrimSpace(value.PathTemplate))) > maxEndpointPathLength {
		return RBACEndpoint{}, fmt.Errorf("%w: path_template must be at most %d characters", ErrInvalidArgument, maxEndpointPathLength)
	}
	if len([]rune(strings.TrimSpace(value.EndpointKey))) > MaxEndpointKeyLength {
		return RBACEndpoint{}, fmt.Errorf("%w: endpoint_key must be at most %d characters", ErrInvalidArgument, MaxEndpointKeyLength)
	}
	return s.repository.SaveEndpoint(ctx, normalizeRBACScope(serviceResource), value)
}

// DeleteEndpoint removes an endpoint from the service catalog by id.
func (s *RBACV1Service) DeleteEndpoint(ctx context.Context, serviceResource string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: endpoint id is required", ErrInvalidArgument)
	}
	return s.repository.DeleteEndpoint(ctx, normalizeRBACScope(serviceResource), id)
}

// ImportSwagger discovers operations from rawURL and saves only endpoints whose
// route shape is not already present for the service. It returns an error on
// the first save failure, with counts accumulated so far.
func (s *RBACV1Service) ImportSwagger(ctx context.Context, serviceResource, rawURL string) (RBACV1SwaggerImportResult, error) {
	scope := normalizeRBACScope(serviceResource)
	operations, err := ReadSwaggerEndpoints(ctx, rawURL, s.swaggerFetch)
	if err != nil {
		return RBACV1SwaggerImportResult{}, err
	}
	existing, err := s.repository.ListEndpoints(ctx, scope)
	if err != nil {
		return RBACV1SwaggerImportResult{}, err
	}
	routes := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		routes[item.Method+"\x00"+RouteShape(item.PathTemplate)] = struct{}{}
	}
	result := RBACV1SwaggerImportResult{Total: len(operations)}
	pending := make([]RBACEndpoint, 0, len(operations))
	for _, operation := range operations {
		key := operation.Method + "\x00" + RouteShape(operation.Path)
		if _, exists := routes[key]; exists {
			result.Skipped++
			continue
		}
		routes[key] = struct{}{}
		pending = append(pending, RBACEndpoint{Method: operation.Method, PathTemplate: operation.Path, Module: operation.Module, Summary: operation.Summary, Source: "swagger", Enabled: true})
	}
	if len(pending) == 0 {
		return result, nil
	}
	// Publish once for the whole import rather than once per endpoint.
	if batch, ok := s.repository.(endpointBatchRepository); ok {
		saved, err := batch.SaveEndpoints(ctx, scope, pending)
		result.Created = len(saved)
		return result, err
	}
	for _, endpoint := range pending {
		if _, err := s.SaveEndpoint(ctx, scope, endpoint); err != nil {
			return result, err
		}
		result.Created++
	}
	return result, nil
}

// endpointBatchRepository saves several endpoints and publishes once.
type endpointBatchRepository interface {
	SaveEndpoints(context.Context, string, []RBACEndpoint) ([]RBACEndpoint, error)
}

// ListRoles returns the roles configured for the application scope.
func (s *RBACV1Service) ListRoles(ctx context.Context, appKey string) ([]RBACRole, error) {
	return s.repository.ListRoles(ctx, normalizeRBACScope(appKey))
}

// SaveRole creates or updates a role. Role code and name are required; the
// application scope and role identity are normalized by the repository.
func (s *RBACV1Service) SaveRole(ctx context.Context, appKey string, value RBACRole) (RBACRole, error) {
	if strings.TrimSpace(value.Code) == "" || strings.TrimSpace(value.Name) == "" {
		return RBACRole{}, fmt.Errorf("%w: role code and name are required", ErrInvalidArgument)
	}
	return s.repository.SaveRole(ctx, normalizeRBACScope(appKey), value)
}

// DeleteRole removes a role by id.
func (s *RBACV1Service) DeleteRole(ctx context.Context, appKey string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: role id is required", ErrInvalidArgument)
	}
	return s.repository.DeleteRole(ctx, normalizeRBACScope(appKey), id)
}

// ListSubjectRoleBindings returns the subject-to-role bindings for the
// application scope.
func (s *RBACV1Service) ListSubjectRoleBindings(ctx context.Context, appKey string) ([]SubjectRoleBinding, error) {
	return s.repository.ListSubjectRoleBindings(ctx, normalizeRBACScope(appKey))
}

// ReplaceSubjectRoles atomically replaces the role set for a subject in a
// domain. Subject is required; an empty domain selects the default domain.
func (s *RBACV1Service) ReplaceSubjectRoles(ctx context.Context, appKey, subject, domain string, roleIDs []uuid.UUID) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("%w: subject is required", ErrInvalidArgument)
	}
	return s.repository.ReplaceSubjectRoles(ctx, normalizeRBACScope(appKey), strings.TrimSpace(subject), NormalizeRBACDomain(domain), roleIDs)
}

// roleSubjectRepository is implemented by repositories that can change one
// role's subjects without replacing each subject's whole role set.
type roleSubjectRepository interface {
	UpdateRoleSubjects(context.Context, string, uuid.UUID, string, []string, []string) error
}

// UpdateRoleSubjects grants one role to the subjects in add and revokes it from
// the subjects in remove, within one domain and one transaction. Unlike
// ReplaceSubjectRoles it never touches a subject's other roles, so concurrent
// assignments of different roles cannot overwrite each other.
func (s *RBACV1Service) UpdateRoleSubjects(ctx context.Context, appKey string, roleID uuid.UUID, domain string, add, remove []string) error {
	if roleID == uuid.Nil {
		return fmt.Errorf("%w: role id is required", ErrInvalidArgument)
	}
	normalize := func(values []string) ([]string, error) {
		seen := make(map[string]struct{}, len(values))
		subjects := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, fmt.Errorf("%w: subjects must not be empty", ErrInvalidArgument)
			}
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				subjects = append(subjects, value)
			}
		}
		return subjects, nil
	}
	added, err := normalize(add)
	if err != nil {
		return err
	}
	removed, err := normalize(remove)
	if err != nil {
		return err
	}
	removing := make(map[string]struct{}, len(removed))
	for _, subject := range removed {
		removing[subject] = struct{}{}
	}
	for _, subject := range added {
		if _, ok := removing[subject]; ok {
			return fmt.Errorf("%w: subject %q is both added and removed", ErrInvalidArgument, subject)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	repository, ok := s.repository.(roleSubjectRepository)
	if !ok {
		return ErrNotFound
	}
	return repository.UpdateRoleSubjects(ctx, normalizeRBACScope(appKey), roleID, NormalizeRBACDomain(domain), added, removed)
}

// Authorize evaluates an authorization input against the currently saved
// policy. Scope is the application identity and must be non-empty; input values
// provide service_resource, subject, method, path, and an optional domain.
func (s *RBACV1Service) Authorize(ctx context.Context, scope string, input map[string]any) (RBACDecision, error) {
	if strings.TrimSpace(scope) == "" {
		return RBACDecision{}, fmt.Errorf("%w: app_key is required", ErrInvalidArgument)
	}
	service, _ := input["service_resource"].(string)
	subject, _ := input["subject"].(string)
	method, _ := input["method"].(string)
	path, _ := input["path"].(string)
	if strings.TrimSpace(service) == "" || strings.TrimSpace(subject) == "" || strings.TrimSpace(method) == "" || !strings.HasPrefix(path, "/") {
		return RBACDecision{}, fmt.Errorf("%w: service_resource, subject, method and absolute path are required", ErrInvalidArgument)
	}
	if repository, ok := s.repository.(SubjectAPIRepository); ok && !s.databaseAuthorization {
		return s.authorizeSubjectAPI(ctx, scope, input, repository)
	}
	policy, err := s.activeCompiledPolicy(ctx, normalizeRBACScope(scope))
	if err != nil {
		return RBACDecision{}, err
	}
	return policy.Authorize(input), nil
}

func evaluateCurrentPolicy(policy CurrentPolicy, input map[string]any) RBACDecision {
	return compileRBACPolicy(policy).Authorize(input)
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

// NormalizeRBACDomain canonicalizes a domain selector. The default domain is
// the empty string; the retired literal "global" is kept as a compatibility
// alias that folds onto the same default. Named domains are preserved exactly,
// so normalization never widens isolation between domains.
func NormalizeRBACDomain(value string) string {
	domain := strings.TrimSpace(value)
	if domain == "" || domain == "global" {
		return ""
	}
	return domain
}

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
