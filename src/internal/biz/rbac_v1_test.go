package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type rbacV1FakeRepository struct{ active RBACRelease }

func (f *rbacV1FakeRepository) ListEndpoints(context.Context, string) ([]RBACEndpoint, error) {
	return nil, nil
}
func (f *rbacV1FakeRepository) ListEndpointsPage(context.Context, string, int, int) ([]RBACEndpoint, int, error) {
	return nil, 0, nil
}
func (f *rbacV1FakeRepository) SaveEndpoint(context.Context, string, RBACEndpoint) (RBACEndpoint, error) {
	return RBACEndpoint{}, nil
}
func (f *rbacV1FakeRepository) DeleteEndpoint(context.Context, string, uuid.UUID) error { return nil }
func (f *rbacV1FakeRepository) ListRoles(context.Context, string) ([]RBACRole, error) {
	return nil, nil
}
func (f *rbacV1FakeRepository) SaveRole(context.Context, string, RBACRole) (RBACRole, error) {
	return RBACRole{}, nil
}
func (f *rbacV1FakeRepository) DeleteRole(context.Context, string, uuid.UUID) error { return nil }
func (f *rbacV1FakeRepository) ListSubjectRoleBindings(context.Context, string) ([]SubjectRoleBinding, error) {
	return nil, nil
}
func (f *rbacV1FakeRepository) ReplaceSubjectRoles(context.Context, string, string, string, []uuid.UUID) error {
	return nil
}
func (f *rbacV1FakeRepository) BuildRBACSnapshot(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (f *rbacV1FakeRepository) CreateRBACRelease(_ context.Context, _ string, description, actor string, snapshot map[string]any) (RBACRelease, error) {
	f.active = RBACRelease{Version: f.active.Version + 1, Description: description, PublishedBy: actor, PublishedAt: time.Now(), Snapshot: snapshot}
	return f.active, nil
}
func (f *rbacV1FakeRepository) ListRBACReleases(context.Context, string) ([]RBACRelease, error) {
	return []RBACRelease{f.active}, nil
}
func (f *rbacV1FakeRepository) GetActiveRBACRelease(context.Context, string) (RBACRelease, error) {
	return f.active, nil
}

func directSnapshot(endpointEnabled, granted bool) map[string]any {
	endpointID, roleID := "endpoint-1", "role-1"
	endpointIDs := []any{}
	if granted {
		endpointIDs = []any{endpointID}
	}
	return map[string]any{
		"endpoints":     []any{map[string]any{"id": endpointID, "service_resource": "order-service", "method": "GET", "path_template": "/books", "enabled": endpointEnabled}},
		"roles":         []any{map[string]any{"id": roleID, "code": "reader", "enabled": true, "endpoint_ids": endpointIDs}},
		"subject_roles": []any{map[string]any{"subject": "user-1", "domain": "tenant-a", "role_id": roleID}},
	}
}

func TestRBACV1AuthorizeDirectRoleEndpoint(t *testing.T) {
	tests := []struct {
		name, reason                      string
		granted, endpointEnabled, allowed bool
	}{
		{"role endpoint allows", "allowed", true, true, true},
		{"missing endpoint denies", "endpoint_not_granted", false, true, false},
		{"disabled endpoint denies", "endpoint_disabled", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := RBACRelease{Version: 1, Snapshot: directSnapshot(tt.endpointEnabled, tt.granted)}
			decision, err := NewRBACV1Service(&rbacV1FakeRepository{active: release}).Authorize(context.Background(), "books", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != tt.allowed || decision.Reason != tt.reason {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestRBACV1AuthorizeMatchesOpenAPIPathParameter(t *testing.T) {
	snapshot := directSnapshot(true, true)
	snapshot["endpoints"].([]any)[0].(map[string]any)["path_template"] = "/v1/authorization/api-endpoints/{id}"
	service := NewRBACV1Service(&rbacV1FakeRepository{active: RBACRelease{Version: 1, Snapshot: snapshot}})

	allowed, err := service.Authorize(context.Background(), "test", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/v1/authorization/api-endpoints/1"})
	if err != nil || !allowed.Allowed {
		t.Fatalf("parameterized endpoint should allow: decision=%+v err=%v", allowed, err)
	}
	nested, err := service.Authorize(context.Background(), "test", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/v1/authorization/api-endpoints/1/extra"})
	if err != nil || nested.Allowed || nested.Reason != "endpoint_not_found" {
		t.Fatalf("template must not match multiple path segments: decision=%+v err=%v", nested, err)
	}
}

func TestAuthorizationSeparatesServicesAndApplication(t *testing.T) {
	roleID, endpointID := uuid.New(), uuid.New()
	snapshot := map[string]any{
		"endpoints":     []any{map[string]any{"id": endpointID.String(), "service_resource": "orders", "method": "GET", "path_template": "/items/{id}", "enabled": true}},
		"roles":         []any{map[string]any{"id": roleID.String(), "enabled": true, "endpoint_ids": []any{endpointID.String()}}},
		"subject_roles": []any{map[string]any{"subject": "user:1", "domain": "tenant-a", "role_id": roleID.String()}},
	}
	service := NewRBACV1Service(&rbacV1FakeRepository{active: RBACRelease{Snapshot: snapshot}})
	input := map[string]any{"service_resource": "orders", "method": "GET", "path": "/items/1", "subject": "user:1", "domain": "tenant-a"}
	decision, err := service.Authorize(context.Background(), "console", input)
	if err != nil || !decision.Allowed {
		t.Fatalf("expected orders allow: %+v %v", decision, err)
	}
	input["service_resource"] = "inventory"
	decision, err = service.Authorize(context.Background(), "console", input)
	if err != nil || decision.Allowed {
		t.Fatalf("service namespace leaked: %+v %v", decision, err)
	}
	input["service_resource"] = "orders"
	input["domain"] = "tenant-b"
	decision, err = service.Authorize(context.Background(), "console", input)
	if err != nil || decision.Allowed {
		t.Fatalf("tenant namespace leaked: %+v %v", decision, err)
	}
	if _, err = service.Authorize(context.Background(), "", input); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing application accepted: %v", err)
	}
	delete(input, "service_resource")
	if _, err = service.Authorize(context.Background(), "console", input); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing service accepted: %v", err)
	}
}
func TestSubjectMenusIncludesAncestorsAndRejectsDisabledBranch(t *testing.T) {
	parent, child, hidden, role := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	snapshot := map[string]any{
		"menus":         []any{map[string]any{"id": parent.String(), "enabled": true, "menu_type": "directory"}, map[string]any{"id": child.String(), "parent_id": parent.String(), "enabled": true}, map[string]any{"id": hidden.String(), "parent_id": parent.String(), "enabled": false}},
		"roles":         []any{map[string]any{"id": role.String(), "enabled": true, "menu_ids": []any{child.String(), hidden.String()}}},
		"subject_roles": []any{map[string]any{"subject": "u", "domain": "d", "role_id": role.String()}},
	}
	items := subjectMenusFromSnapshot(snapshot, "u", "d")
	if len(items) != 2 || items[0].ID != parent || items[1].ID != child {
		t.Fatalf("unexpected menus: %+v", items)
	}
	snapshot["menus"].([]any)[0].(map[string]any)["enabled"] = false
	if items = subjectMenusFromSnapshot(snapshot, "u", "d"); len(items) != 0 {
		t.Fatalf("disabled ancestor exposed children: %+v", items)
	}
	if items = subjectMenusFromSnapshot(snapshot, "another-user", "d"); len(items) != 0 {
		t.Fatalf("user menus leaked: %+v", items)
	}
}

func TestAuthorizationUsesMostSpecificEndpointBeforeGrants(t *testing.T) {
	dynamic, static, role := uuid.NewString(), uuid.NewString(), uuid.NewString()
	snapshot := map[string]any{
		"endpoints":     []any{map[string]any{"id": dynamic, "service_resource": "orders", "method": "GET", "path_template": "/items/{id}", "enabled": true}, map[string]any{"id": static, "service_resource": "orders", "method": "GET", "path_template": "/items/export", "enabled": true}},
		"roles":         []any{map[string]any{"id": role, "enabled": true, "endpoint_ids": []any{dynamic}}},
		"subject_roles": []any{map[string]any{"subject": "u", "domain": "d", "role_id": role}},
	}
	input := map[string]any{"subject": "u", "domain": "d", "service_resource": "orders", "method": "GET", "path": "/items/export"}
	if d := evaluateRBACSnapshot(RBACRelease{Snapshot: snapshot}, input); d.Allowed {
		t.Fatalf("dynamic grant bypassed explicit export permission: %+v", d)
	}
	snapshot["endpoints"].([]any)[1].(map[string]any)["enabled"] = false
	if d := evaluateRBACSnapshot(RBACRelease{Snapshot: snapshot}, input); d.Allowed || d.Reason != "endpoint_disabled" {
		t.Fatalf("disabled static endpoint bypassed: %+v", d)
	}
	input["path"] = "/items/123"
	if d := evaluateRBACSnapshot(RBACRelease{Snapshot: snapshot}, input); !d.Allowed {
		t.Fatalf("dynamic grant rejected: %+v", d)
	}
}

type rbacV1ImportRepository struct {
	*rbacV1FakeRepository
	endpoints []RBACEndpoint
	saved     []RBACEndpoint
}

func (r *rbacV1ImportRepository) ListEndpoints(context.Context, string) ([]RBACEndpoint, error) {
	return r.endpoints, nil
}
func (r *rbacV1ImportRepository) SaveEndpoint(_ context.Context, scope string, value RBACEndpoint) (RBACEndpoint, error) {
	value.ServiceResource = scope
	r.saved = append(r.saved, value)
	return value, nil
}

func TestRBACV1ServiceImportSwaggerFromDocument(t *testing.T) {
	repo := &rbacV1ImportRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}}
	service := NewRBACV1Service(repo)
	document := `{"openapi":"3.0.3","info":{"title":"Orders API"},"paths":{` +
		`"/orders":{"post":{"operationId":"Orders_create"}},` +
		`"/orders/{id}":{"get":{"tags":["Orders"],"summary":"Get order"}}}}`
	result, err := service.ImportSwagger(context.Background(), "order-service", "", document)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.Created != 2 || result.Skipped != 0 {
		t.Fatalf("import result = %#v", result)
	}
	if len(repo.saved) != 2 {
		t.Fatalf("saved = %#v", repo.saved)
	}
	byPath := map[string]RBACEndpoint{}
	for _, item := range repo.saved {
		byPath[item.PathTemplate] = item
	}
	if item := byPath["/orders"]; item.Method != "POST" || item.Summary != "Orders_create" || item.Source != "swagger" || !item.Enabled {
		t.Fatalf("operationId fallback endpoint = %#v", item)
	}
	if item := byPath["/orders/{id}"]; item.Method != "GET" || item.Summary != "Get order" {
		t.Fatalf("summary endpoint = %#v", item)
	}
}

func TestRBACV1ServiceImportSwaggerSkipsExistingRoutes(t *testing.T) {
	repo := &rbacV1ImportRepository{
		rbacV1FakeRepository: &rbacV1FakeRepository{},
		endpoints:            []RBACEndpoint{{Method: "GET", PathTemplate: "/orders/{id}"}},
	}
	document := `{"openapi":"3.0.3","paths":{"/orders/{id}":{"get":{"summary":"Get order"}},"/orders":{"post":{"summary":"Create"}}}}`
	result, err := NewRBACV1Service(repo).ImportSwagger(context.Background(), "order-service", "", document)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.Created != 1 || result.Skipped != 1 {
		t.Fatalf("import result = %#v", result)
	}
}

func TestRBACV1ServiceImportSwaggerKeepsURLCallCompatible(t *testing.T) {
	repo := &rbacV1ImportRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}}
	// 旧调用只传 URL 时仍走远程抓取分支；非绝对地址由 URL 校验拒绝。
	if _, err := NewRBACV1Service(repo).ImportSwagger(context.Background(), "order-service", "not-a-url"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("url-only import error = %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("unexpected endpoints saved: %#v", repo.saved)
	}
}

func TestRBACV1ServiceImportSwaggerRequiresSource(t *testing.T) {
	repo := &rbacV1ImportRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}}
	if _, err := NewRBACV1Service(repo).ImportSwagger(context.Background(), "order-service", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty source error = %v", err)
	}
}
