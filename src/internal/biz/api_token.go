package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type APITokenPermission struct {
	EndpointID      uuid.UUID `json:"endpoint_id"`
	ServiceResource string    `json:"service_resource"`
	Action          string    `json:"action"`
	TenantID        string    `json:"tenant_id"`
	ProjectID       string    `json:"project_id"`
	Method          string    `json:"method"`
	PathTemplate    string    `json:"path_template"`
	Summary         string    `json:"summary"`
}

// APITokenPolicy references maintained permission groups and constrains their
// resource scope. Endpoint grants are resolved locally from the live catalog.
type APITokenPolicy struct {
	ID                 uuid.UUID   `json:"id"`
	Effect             string      `json:"effect"`
	ServiceResource    string      `json:"service_resource"`
	TenantID           string      `json:"tenant_id"`
	ProjectID          string      `json:"project_id"`
	PermissionGroupIDs []uuid.UUID `json:"permission_group_ids"`
}

type APIToken struct {
	ID          uuid.UUID            `json:"id"`
	AppKey      string               `json:"app_key"`
	Name        string               `json:"name"`
	TokenPrefix string               `json:"token_prefix"`
	TokenHash   string               `json:"-"`
	OwnerType   string               `json:"owner_type"`
	OwnerID     string               `json:"owner_id"`
	Status      string               `json:"status"`
	ExpiresAt   *time.Time           `json:"expires_at"`
	LastUsedAt  *time.Time           `json:"last_used_at"`
	CreatedBy   string               `json:"created_by"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	RevokedAt   *time.Time           `json:"revoked_at"`
	Permissions []APITokenPermission `json:"permissions"`
	Policies    []APITokenPolicy     `json:"policies"`
}

type APITokenInput struct {
	Name        string               `json:"name"`
	OwnerType   string               `json:"owner_type"`
	OwnerID     string               `json:"owner_id"`
	ExpiresAt   *time.Time           `json:"expires_at"`
	Permissions []APITokenPermission `json:"permissions"`
	Policies    []APITokenPolicy     `json:"policies"`
}

type APITokenCreated struct {
	Token    APIToken `json:"token"`
	RawToken string   `json:"raw_token"`
}

type APITokenFilter struct {
	AppKey, Search, Status string
	Page, PageSize         int
}

type APITokenTarget struct {
	AppKey          string `json:"app_key"`
	ServiceResource string `json:"service_resource"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	TenantID        string `json:"tenant_id"`
	ProjectID       string `json:"project_id"`
}

type APITokenDecision struct {
	Active    bool      `json:"active"`
	Allowed   bool      `json:"allowed"`
	Reason    string    `json:"reason"`
	TokenID   uuid.UUID `json:"token_id,omitempty"`
	AppKey    string    `json:"app_key,omitempty"`
	OwnerType string    `json:"owner_type,omitempty"`
	OwnerID   string    `json:"owner_id,omitempty"`
}

// Repository writes token metadata, resolved endpoint grants and audit in one
// transaction. Runtime methods must use only local SQL, never a remote catalog.
type APITokenRepository interface {
	List(context.Context, APITokenFilter) (RBACPage[APIToken], error)
	Get(context.Context, string, uuid.UUID) (APIToken, error)
	Find(context.Context, uuid.UUID) (APIToken, error)
	Create(context.Context, APIToken) (APIToken, error)
	Update(context.Context, APIToken) (APIToken, error)
	SetStatus(context.Context, string, uuid.UUID, string) (APIToken, error)
	Rotate(context.Context, string, uuid.UUID, APIToken, bool) (APIToken, error)
	Endpoints(context.Context, string, string) ([]RBACEndpoint, error)
	RecordUse(context.Context, uuid.UUID) error
}

type APITokenService struct{ repository APITokenRepository }

func NewAPITokenService(repository APITokenRepository) *APITokenService {
	return &APITokenService{repository: repository}
}

func (s *APITokenService) List(ctx context.Context, f APITokenFilter) (RBACPage[APIToken], error) {
	f.AppKey, f.Search = strings.TrimSpace(f.AppKey), strings.TrimSpace(f.Search)
	if f.AppKey == "" || len(f.Search) > 200 {
		return RBACPage[APIToken]{}, fmt.Errorf("%w: app_key is required and search must be at most 200 bytes", ErrInvalidArgument)
	}
	switch f.Status {
	case "", "active", "disabled", "revoked", "expired":
	default:
		return RBACPage[APIToken]{}, fmt.Errorf("%w: invalid token status", ErrInvalidArgument)
	}
	f.Page, f.PageSize = normalizeRBACPage(f.Page, f.PageSize)
	return s.repository.List(ctx, f)
}

func (s *APITokenService) Get(ctx context.Context, appKey string, id uuid.UUID) (APIToken, error) {
	return s.repository.Get(ctx, strings.TrimSpace(appKey), id)
}

func (s *APITokenService) normalize(ctx context.Context, appKey string, in APITokenInput) (APITokenInput, error) {
	in.Name, in.OwnerID = strings.TrimSpace(in.Name), strings.TrimSpace(in.OwnerID)
	if strings.TrimSpace(appKey) == "" || in.Name == "" || len([]rune(in.Name)) > 200 || in.OwnerID == "" || len(in.OwnerID) > 200 || (in.OwnerType != "user" && in.OwnerType != "service") {
		return in, fmt.Errorf("%w: app_key, name and a user/service owner are required", ErrInvalidArgument)
	}
	if in.ExpiresAt != nil {
		v := in.ExpiresAt.UTC()
		in.ExpiresAt = &v
		if !v.After(time.Now()) {
			return in, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidArgument)
		}
	}
	if len(in.Policies) > 0 {
		if len(in.Policies) > 100 || len(in.Permissions) > 0 {
			return in, fmt.Errorf("%w: use at most 100 policies and do not mix policies with legacy permissions", ErrInvalidArgument)
		}
		for i := range in.Policies {
			p := &in.Policies[i]
			p.ServiceResource, p.TenantID, p.ProjectID = strings.TrimSpace(p.ServiceResource), strings.TrimSpace(p.TenantID), strings.TrimSpace(p.ProjectID)
			if p.ID == uuid.Nil {
				p.ID = uuid.New()
			}
			if p.Effect == "" {
				p.Effect = "allow"
			}
			if p.Effect != "allow" || p.ServiceResource == "" || len(p.ServiceResource) > 128 || len(p.TenantID) > 200 || len(p.ProjectID) > 200 || len(p.PermissionGroupIDs) == 0 || len(p.PermissionGroupIDs) > 1000 {
				return in, fmt.Errorf("%w: invalid policy, permission groups or resource scope", ErrInvalidArgument)
			}
			seen := map[uuid.UUID]bool{}
			for _, id := range p.PermissionGroupIDs {
				if id == uuid.Nil || seen[id] {
					return in, fmt.Errorf("%w: invalid or duplicate permission group", ErrInvalidArgument)
				}
				seen[id] = true
			}
		}
		resolver, ok := s.repository.(interface {
			ResolvePolicies(context.Context, string, []APITokenPolicy) ([]APITokenPermission, error)
		})
		if !ok {
			return in, fmt.Errorf("permission group resolver is unavailable")
		}
		var err error
		in.Permissions, err = resolver.ResolvePolicies(ctx, appKey, in.Policies)
		if err != nil {
			return in, err
		}
	}
	if len(in.Permissions) == 0 || len(in.Permissions) > 1000 {
		return in, fmt.Errorf("%w: select between 1 and 1000 API permissions", ErrInvalidArgument)
	}
	seen := map[string]bool{}
	catalog := map[string][]RBACEndpoint{}
	for i := range in.Permissions {
		p := &in.Permissions[i]
		p.ServiceResource, p.TenantID, p.ProjectID = strings.TrimSpace(p.ServiceResource), strings.TrimSpace(p.TenantID), strings.TrimSpace(p.ProjectID)
		if p.EndpointID == uuid.Nil || p.ServiceResource == "" || len(p.ServiceResource) > 128 || len(p.TenantID) > 200 || len(p.ProjectID) > 200 {
			return in, fmt.Errorf("%w: invalid permission or resource scope", ErrInvalidArgument)
		}
		endpoints, ok := catalog[p.ServiceResource]
		if !ok {
			var err error
			endpoints, err = s.repository.Endpoints(ctx, appKey, p.ServiceResource)
			if err != nil {
				return in, err
			}
			catalog[p.ServiceResource] = endpoints
		}
		found := false
		for _, e := range endpoints {
			if e.ID == p.EndpointID && e.Enabled {
				action := APITokenAction(e.Method)
				if action == "" || p.Action != action {
					return in, fmt.Errorf("%w: permission action does not match API method", ErrInvalidArgument)
				}
				p.Method, p.PathTemplate, p.Summary = e.Method, e.PathTemplate, e.Summary
				found = true
				break
			}
		}
		if !found {
			return in, fmt.Errorf("%w: API must be enabled and belong to an application-linked service", ErrConflict)
		}
		key := p.EndpointID.String() + "\x00" + p.TenantID + "\x00" + p.ProjectID
		if seen[key] {
			return in, fmt.Errorf("%w: duplicate permission", ErrInvalidArgument)
		}
		seen[key] = true
	}
	return in, nil
}

func newAPIToken(appKey string, in APITokenInput) (APIToken, string, error) {
	id := uuid.New()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return APIToken{}, "", err
	}
	raw := "nxa_" + id.String() + "_" + base64.RawURLEncoding.EncodeToString(secret)
	now := time.Now().UTC()
	return APIToken{ID: id, AppKey: appKey, Name: in.Name, TokenPrefix: "nxa_" + id.String()[:8], TokenHash: APITokenHash(raw), OwnerType: in.OwnerType, OwnerID: in.OwnerID, Status: "active", ExpiresAt: in.ExpiresAt, Permissions: in.Permissions, Policies: in.Policies, CreatedAt: now, UpdatedAt: now}, raw, nil
}

func (s *APITokenService) Create(ctx context.Context, appKey string, in APITokenInput) (APITokenCreated, error) {
	if in.OwnerType == "" {
		in.OwnerType = "service"
	}
	if in.OwnerID == "" {
		in.OwnerID = strings.TrimSpace(appKey)
	}
	in, err := s.normalize(ctx, appKey, in)
	if err != nil {
		return APITokenCreated{}, err
	}
	token, raw, err := newAPIToken(strings.TrimSpace(appKey), in)
	if err != nil {
		return APITokenCreated{}, err
	}
	token.CreatedBy = AuditActorFromContext(ctx).ID
	token, err = s.repository.Create(ctx, token)
	if err != nil {
		return APITokenCreated{}, err
	}
	return APITokenCreated{Token: token, RawToken: raw}, nil
}

func (s *APITokenService) Update(ctx context.Context, appKey string, id uuid.UUID, in APITokenInput) (APIToken, error) {
	existing, err := s.Get(ctx, appKey, id)
	if err != nil {
		return APIToken{}, err
	}
	if existing.Status == "revoked" {
		return APIToken{}, fmt.Errorf("%w: revoked tokens cannot be edited", ErrConflict)
	}
	if in.OwnerType == "" {
		in.OwnerType = existing.OwnerType
	}
	if in.OwnerID == "" {
		in.OwnerID = existing.OwnerID
	}
	if existing.OwnerType != in.OwnerType || existing.OwnerID != strings.TrimSpace(in.OwnerID) {
		return APIToken{}, fmt.Errorf("%w: token ownership is immutable", ErrInvalidArgument)
	}
	in, err = s.normalize(ctx, appKey, in)
	if err != nil {
		return APIToken{}, err
	}
	existing.Name, existing.ExpiresAt, existing.Permissions, existing.Policies = in.Name, in.ExpiresAt, in.Permissions, in.Policies
	return s.repository.Update(ctx, existing)
}

func (s *APITokenService) SetStatus(ctx context.Context, appKey string, id uuid.UUID, status string) (APIToken, error) {
	if status != "active" && status != "disabled" && status != "revoked" {
		return APIToken{}, fmt.Errorf("%w: invalid token status", ErrInvalidArgument)
	}
	return s.repository.SetStatus(ctx, appKey, id, status)
}

func (s *APITokenService) Rotate(ctx context.Context, appKey string, id uuid.UUID, revokeOld bool) (APITokenCreated, error) {
	old, err := s.Get(ctx, appKey, id)
	if err != nil {
		return APITokenCreated{}, err
	}
	if old.Status != "active" || (old.ExpiresAt != nil && !old.ExpiresAt.After(time.Now())) {
		return APITokenCreated{}, fmt.Errorf("%w: only an active, unexpired token can rotate", ErrConflict)
	}
	input := APITokenInput{Name: old.Name, OwnerType: old.OwnerType, OwnerID: old.OwnerID, ExpiresAt: old.ExpiresAt, Policies: old.Policies}
	if len(old.Policies) == 0 {
		input.Permissions = old.Permissions
	}
	in, err := s.normalize(ctx, appKey, input)
	if err != nil {
		return APITokenCreated{}, err
	}
	token, raw, err := newAPIToken(appKey, in)
	if err != nil {
		return APITokenCreated{}, err
	}
	token.CreatedBy = AuditActorFromContext(ctx).ID
	token, err = s.repository.Rotate(ctx, appKey, id, token, revokeOld)
	if err != nil {
		return APITokenCreated{}, err
	}
	return APITokenCreated{Token: token, RawToken: raw}, nil
}

func APITokenHash(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func APITokenAction(method string) string {
	switch strings.ToUpper(method) {
	case "GET", "HEAD":
		return "read"
	case "POST", "PUT", "PATCH", "DELETE":
		return "write"
	default:
		return ""
	}
}

func (s *APITokenService) Authenticate(ctx context.Context, raw string) (APIToken, bool, error) {
	parts := strings.Split(raw, "_")
	if len(parts) < 3 || !strings.HasPrefix(raw, "nxa_") || len(raw) != 84 {
		return APIToken{}, false, nil
	}
	id, err := uuid.Parse(parts[1])
	if err != nil || id == uuid.Nil {
		return APIToken{}, false, nil
	}
	token, err := s.repository.Find(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return APIToken{}, false, nil
	}
	if err != nil {
		return APIToken{}, false, err
	}
	if subtle.ConstantTimeCompare([]byte(APITokenHash(raw)), []byte(token.TokenHash)) != 1 || token.Status != "active" || token.RevokedAt != nil || (token.ExpiresAt != nil && !token.ExpiresAt.After(time.Now())) {
		return APIToken{}, false, nil
	}
	return token, true, nil
}

func (s *APITokenService) Authorize(ctx context.Context, raw string, target APITokenTarget) (APITokenDecision, error) {
	if err := ValidateAPITokenTarget(target); err != nil {
		return APITokenDecision{}, err
	}
	token, active, err := s.Authenticate(ctx, raw)
	if err != nil {
		return APITokenDecision{}, err
	}
	if !active {
		return APITokenDecision{Reason: "invalid_token"}, nil
	}
	d := APITokenDecision{Active: true, Reason: "permission_denied", TokenID: token.ID, AppKey: token.AppKey, OwnerType: token.OwnerType, OwnerID: token.OwnerID}
	if token.AppKey != target.AppKey {
		return d, nil
	}
	// Match against every live endpoint before checking grants: a grant on a
	// parameter route must not override a more specific ungranted static route.
	endpoints, err := s.repository.Endpoints(ctx, target.AppKey, target.ServiceResource)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return d, nil
		}
		return APITokenDecision{}, err
	}
	var matched *RBACEndpoint
	best := -1
	ambiguous := false
	for i := range endpoints {
		e := &endpoints[i]
		if e.Method != target.Method {
			continue
		}
		score, ok := matchAPITokenPath(e.PathTemplate, target.Path)
		if !ok {
			continue
		}
		if score > best {
			matched, best, ambiguous = e, score, false
		} else if score == best {
			ambiguous = true
		}
	}
	if matched == nil || ambiguous || !matched.Enabled {
		return d, nil
	}
	for _, p := range token.Permissions {
		if p.EndpointID == matched.ID && p.ServiceResource == target.ServiceResource && p.Action == APITokenAction(target.Method) && p.Method == matched.Method && p.PathTemplate == matched.PathTemplate && (p.TenantID == "" || p.TenantID == target.TenantID) && (p.ProjectID == "" || p.ProjectID == target.ProjectID) {
			d.Allowed, d.Reason = true, "allowed"
			// Usage metadata is best effort and throttled by the repository; an
			// unrelated usage write must not turn a correct decision into a deny.
			_ = s.repository.RecordUse(ctx, token.ID)
			return d, nil
		}
	}
	return d, nil
}

func ValidateAPITokenTarget(t APITokenTarget) error {
	if strings.TrimSpace(t.AppKey) == "" || strings.TrimSpace(t.ServiceResource) == "" || len(t.AppKey) > 128 || len(t.ServiceResource) > 128 || len(t.TenantID) > 200 || len(t.ProjectID) > 200 || APITokenAction(t.Method) == "" || t.Method != strings.ToUpper(t.Method) || len(t.Path) > 2048 {
		return fmt.Errorf("%w: invalid API authorization target", ErrInvalidArgument)
	}
	if _, ok := apiTokenPathSegments(t.Path); !ok {
		return fmt.Errorf("%w: path must be a canonical absolute path without query or fragment", ErrInvalidArgument)
	}
	return nil
}

func apiTokenPathSegments(path string) ([]string, bool) {
	if path == "" || path[0] != '/' || strings.ContainsAny(path, "?#\\\x00\r\n") || strings.Contains(path, "//") {
		return nil, false
	}
	if path == "/" {
		return []string{}, true
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\\x00") {
			return nil, false
		}
		// Routers match decoded path segments. Encoded unreserved characters
		// must not hide a static route behind a broader parameter grant.
		segments[i] = decoded
	}
	return segments, true
}

func matchAPITokenPath(template, path string) (int, bool) {
	templateParts, ok := apiTokenPathSegments(template)
	if !ok {
		return 0, false
	}
	pathParts, ok := apiTokenPathSegments(path)
	if !ok || len(templateParts) != len(pathParts) {
		return 0, false
	}
	score := 0
	for i, part := range templateParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			if pathParts[i] == "" {
				return 0, false
			}
			continue
		}
		if part != pathParts[i] {
			return 0, false
		}
		score++
	}
	return score, true
}
