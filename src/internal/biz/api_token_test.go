package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memoryTokenRepository struct {
	tokens    map[uuid.UUID]APIToken
	endpoints []RBACEndpoint
	appKey    string
	err       error
}

func newMemoryTokenRepository() *memoryTokenRepository {
	return &memoryTokenRepository{tokens: map[uuid.UUID]APIToken{}, appKey: "console", endpoints: []RBACEndpoint{{ID: uuid.New(), ServiceResource: "nexusauth", Method: "GET", PathTemplate: "/scim/v2/Users/{id}", Enabled: true}}}
}
func (r *memoryTokenRepository) List(_ context.Context, f APITokenFilter) (RBACPage[APIToken], error) {
	items := []APIToken{}
	for _, v := range r.tokens {
		if v.AppKey == f.AppKey {
			items = append(items, v)
		}
	}
	return RBACPage[APIToken]{Items: items, Total: len(items), Page: f.Page, PageSize: f.PageSize}, r.err
}
func (r *memoryTokenRepository) Get(ctx context.Context, app string, id uuid.UUID) (APIToken, error) {
	v, err := r.Find(ctx, id)
	if err == nil && v.AppKey != app {
		return APIToken{}, ErrNotFound
	}
	return v, err
}
func (r *memoryTokenRepository) Find(_ context.Context, id uuid.UUID) (APIToken, error) {
	if r.err != nil {
		return APIToken{}, r.err
	}
	v, ok := r.tokens[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (r *memoryTokenRepository) Create(_ context.Context, v APIToken) (APIToken, error) {
	if r.err != nil {
		return APIToken{}, r.err
	}
	r.tokens[v.ID] = v
	return v, nil
}
func (r *memoryTokenRepository) Update(ctx context.Context, v APIToken) (APIToken, error) {
	return r.Create(ctx, v)
}
func (r *memoryTokenRepository) SetStatus(ctx context.Context, app string, id uuid.UUID, status string) (APIToken, error) {
	v, err := r.Get(ctx, app, id)
	if err != nil {
		return v, err
	}
	if v.Status == "revoked" && status != "revoked" {
		return v, ErrConflict
	}
	v.Status = status
	if status == "revoked" {
		now := time.Now()
		v.RevokedAt = &now
	}
	r.tokens[id] = v
	return v, nil
}
func (r *memoryTokenRepository) Rotate(ctx context.Context, app string, id uuid.UUID, v APIToken, revoke bool) (APIToken, error) {
	if revoke {
		if _, err := r.SetStatus(ctx, app, id, "revoked"); err != nil {
			return APIToken{}, err
		}
	}
	return r.Create(ctx, v)
}
func (r *memoryTokenRepository) Endpoints(_ context.Context, app, service string) ([]RBACEndpoint, error) {
	if r.err != nil {
		return nil, r.err
	}
	if app != r.appKey {
		return nil, ErrNotFound
	}
	out := []RBACEndpoint{}
	for _, v := range r.endpoints {
		if v.ServiceResource == service {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *memoryTokenRepository) RecordUse(context.Context, uuid.UUID) error { return nil }

func tokenTestInput(r *memoryTokenRepository) APITokenInput {
	e := r.endpoints[0]
	return APITokenInput{Name: "SCIM reader", OwnerType: "service", OwnerID: "directory-sync", Permissions: []APITokenPermission{{EndpointID: e.ID, ServiceResource: e.ServiceResource, Action: "read", TenantID: "tenant-a", ProjectID: "project-1"}}}
}
func tokenTestTarget() APITokenTarget {
	return APITokenTarget{AppKey: "console", ServiceResource: "nexusauth", Method: "GET", Path: "/scim/v2/Users/100", TenantID: "tenant-a", ProjectID: "project-1"}
}

func TestAPITokenCreateAndAuthorizeBoundaries(t *testing.T) {
	r := newMemoryTokenRepository()
	s := NewAPITokenService(r)
	ctx := WithAuditActor(context.Background(), AuditActor{ID: "admin"})
	created, err := s.Create(ctx, "console", tokenTestInput(r))
	if err != nil {
		t.Fatal(err)
	}
	if len(created.RawToken) != 84 || created.Token.TokenHash == created.RawToken || created.Token.CreatedBy != "admin" {
		t.Fatal("invalid token creation")
	}
	encoded, _ := json.Marshal(created.Token)
	if strings.Contains(string(encoded), created.RawToken) || strings.Contains(string(encoded), created.Token.TokenHash) {
		t.Fatal("secret or hash leaked in summary")
	}
	decision, err := s.Authorize(ctx, created.RawToken, tokenTestTarget())
	if err != nil || !decision.Active || !decision.Allowed {
		t.Fatalf("valid token denied: %+v %v", decision, err)
	}
	for _, tc := range []struct {
		name   string
		modify func(*APITokenTarget)
	}{
		{"tenant", func(v *APITokenTarget) { v.TenantID = "other" }},
		{"missing tenant", func(v *APITokenTarget) { v.TenantID = "" }},
		{"project", func(v *APITokenTarget) { v.ProjectID = "other" }},
		{"application", func(v *APITokenTarget) { v.AppKey = "other" }},
		{"service", func(v *APITokenTarget) { v.ServiceResource = "other" }},
		{"write", func(v *APITokenTarget) { v.Method = "DELETE" }},
		{"deeper path", func(v *APITokenTarget) { v.Path = "/scim/v2/Users/100/password" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tokenTestTarget()
			tc.modify(&target)
			d, err := s.Authorize(ctx, created.RawToken, target)
			if err != nil || !d.Active || d.Allowed {
				t.Fatalf("scope escaped: %+v %v", d, err)
			}
		})
	}
	wrong := created.RawToken[:len(created.RawToken)-1] + "!"
	d, err := s.Authorize(ctx, wrong, tokenTestTarget())
	if err != nil || d.Active || d.Allowed {
		t.Fatal("wrong secret accepted")
	}
}

func TestAPITokenImmediateLifecycle(t *testing.T) {
	r := newMemoryTokenRepository()
	s := NewAPITokenService(r)
	ctx := context.Background()
	c, err := s.Create(ctx, "console", tokenTestInput(r))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, "console", c.Token.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Authorize(ctx, c.RawToken, tokenTestTarget())
	if d.Active {
		t.Fatal("disabled token active")
	}
	if _, err = s.SetStatus(ctx, "console", c.Token.ID, "active"); err != nil {
		t.Fatal(err)
	}
	rotated, err := s.Rotate(ctx, "console", c.Token.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	d, _ = s.Authorize(ctx, c.RawToken, tokenTestTarget())
	if d.Active {
		t.Fatal("old token active after rotation")
	}
	d, _ = s.Authorize(ctx, rotated.RawToken, tokenTestTarget())
	if !d.Allowed {
		t.Fatal("new token denied")
	}
	if _, err = s.SetStatus(ctx, "console", c.Token.ID, "active"); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked token resurrected")
	}
	if _, err = s.Update(ctx, "console", c.Token.ID, tokenTestInput(r)); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked token edited")
	}
	v := r.tokens[rotated.Token.ID]
	past := time.Now().Add(-time.Second)
	v.ExpiresAt = &past
	r.tokens[v.ID] = v
	d, _ = s.Authorize(ctx, rotated.RawToken, tokenTestTarget())
	if d.Active {
		t.Fatal("expired token active")
	}
}

func TestAPITokenEndpointChangesFailClosed(t *testing.T) {
	r := newMemoryTokenRepository()
	s := NewAPITokenService(r)
	ctx := context.Background()
	c, err := s.Create(ctx, "console", tokenTestInput(r))
	if err != nil {
		t.Fatal(err)
	}
	r.endpoints = append(r.endpoints, RBACEndpoint{ID: uuid.New(), ServiceResource: "nexusauth", Method: "GET", PathTemplate: "/scim/v2/Users/admin", Enabled: true})
	target := tokenTestTarget()
	target.Path = "/scim/v2/Users/admin"
	d, _ := s.Authorize(ctx, c.RawToken, target)
	if d.Allowed {
		t.Fatal("parameter grant bypassed static route")
	}
	target.Path = "/scim/v2/Users/%61dmin"
	d, _ = s.Authorize(ctx, c.RawToken, target)
	if d.Allowed {
		t.Fatal("encoded static route bypassed its explicit grant boundary")
	}
	target.Path = "/scim/v2/Users/admin"
	r.endpoints[1].Enabled = false
	d, _ = s.Authorize(ctx, c.RawToken, target)
	if d.Allowed {
		t.Fatal("disabled static route fell back to parameter grant")
	}
	r.endpoints = r.endpoints[:1]
	r.endpoints[0].PathTemplate = "/scim/v2/Administrators/{id}"
	target.Path = "/scim/v2/Administrators/100"
	d, _ = s.Authorize(ctx, c.RawToken, target)
	if d.Allowed {
		t.Fatal("edited endpoint expanded old grant")
	}
	r.endpoints[0].PathTemplate = "/scim/v2/Users/{id}"
	r.endpoints = append(r.endpoints, RBACEndpoint{ID: uuid.New(), ServiceResource: "nexusauth", Method: "GET", PathTemplate: "/scim/v2/Users/{other}", Enabled: true})
	d, _ = s.Authorize(ctx, c.RawToken, tokenTestTarget())
	if d.Allowed {
		t.Fatal("ambiguous route accepted")
	}
}

func TestAPITokenRejectsInvalidTargetsAndCreation(t *testing.T) {
	for _, path := range []string{"", "relative", "//users", "/users/../admin", "/users/%2e%2e/admin", "/users/%2Fadmin", "/users/%5cadmin", "/users?tenant=b", "/users#x", "/users/%00"} {
		target := tokenTestTarget()
		target.Path = path
		if err := ValidateAPITokenTarget(target); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	r := newMemoryTokenRepository()
	s := NewAPITokenService(r)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		modify func(*APITokenInput)
	}{
		{"empty permissions", func(v *APITokenInput) { v.Permissions = nil }},
		{"action", func(v *APITokenInput) { v.Permissions[0].Action = "write" }},
		{"cross service", func(v *APITokenInput) { v.Permissions[0].ServiceResource = "other" }},
		{"foreign endpoint", func(v *APITokenInput) { v.Permissions[0].EndpointID = uuid.New() }},
		{"expired", func(v *APITokenInput) { past := time.Now().Add(-time.Second); v.ExpiresAt = &past }},
		{"duplicate", func(v *APITokenInput) { v.Permissions = append(v.Permissions, v.Permissions[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tokenTestInput(r)
			tc.modify(&in)
			result, err := s.Create(ctx, "console", in)
			if err == nil || result.RawToken != "" {
				t.Fatal("invalid creation returned usable token")
			}
		})
	}
	r.err = errors.New("database unavailable")
	_, err := s.Authorize(ctx, "nxa_"+uuid.NewString()+"_"+strings.Repeat("a", 43), tokenTestTarget())
	if err == nil {
		t.Fatal("DB failure reported as invalid token")
	}
}
