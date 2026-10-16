package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type recordingRBACV1Repository struct {
	rbacV1FakeRepository
	subject string
	domain  string
}

func (r *recordingRBACV1Repository) ReplaceSubjectRoles(_ context.Context, _, subject, domain string, _ []uuid.UUID) error {
	r.subject, r.domain = subject, domain
	return nil
}

type rbacV1FakeRepository struct{ active CurrentPolicy }

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
func (f *rbacV1FakeRepository) GetPolicyMetadata(context.Context, string) (CurrentPolicy, error) {
	p := f.active
	p.Data = nil
	return p, nil
}
func (f *rbacV1FakeRepository) GetCurrentPolicy(context.Context, CurrentPolicy) (CurrentPolicy, error) {
	return f.active, nil
}

func directPolicyData(endpointEnabled, granted bool) map[string]any {
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
			state := CurrentPolicy{Data: directPolicyData(tt.endpointEnabled, tt.granted)}
			decision, err := NewRBACV1Service(&rbacV1FakeRepository{active: state}).Authorize(context.Background(), "books", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"})
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
	records := directPolicyData(true, true)
	records["endpoints"].([]any)[0].(map[string]any)["path_template"] = "/v1/authorization/api-endpoints/{id}"
	service := NewRBACV1Service(&rbacV1FakeRepository{active: CurrentPolicy{Data: records}})

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
	records := map[string]any{
		"endpoints":     []any{map[string]any{"id": endpointID.String(), "service_resource": "orders", "method": "GET", "path_template": "/items/{id}", "enabled": true}},
		"roles":         []any{map[string]any{"id": roleID.String(), "enabled": true, "endpoint_ids": []any{endpointID.String()}}},
		"subject_roles": []any{map[string]any{"subject": "user:1", "domain": "tenant-a", "role_id": roleID.String()}},
	}
	service := NewRBACV1Service(&rbacV1FakeRepository{active: CurrentPolicy{Data: records}})
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
	records := map[string]any{
		"menus":         []any{map[string]any{"id": parent.String(), "enabled": true, "menu_type": "directory"}, map[string]any{"id": child.String(), "parent_id": parent.String(), "enabled": true}, map[string]any{"id": hidden.String(), "parent_id": parent.String(), "enabled": false}},
		"roles":         []any{map[string]any{"id": role.String(), "enabled": true, "menu_ids": []any{child.String(), hidden.String()}}},
		"subject_roles": []any{map[string]any{"subject": "u", "domain": "d", "role_id": role.String()}},
	}
	items := subjectMenusFromPolicy(records, "u", "d")
	if len(items) != 2 || items[0].ID != parent || items[1].ID != child {
		t.Fatalf("unexpected menus: %+v", items)
	}
	records["menus"].([]any)[0].(map[string]any)["enabled"] = false
	if items = subjectMenusFromPolicy(records, "u", "d"); len(items) != 0 {
		t.Fatalf("disabled ancestor exposed children: %+v", items)
	}
	if items = subjectMenusFromPolicy(records, "another-user", "d"); len(items) != 0 {
		t.Fatalf("user menus leaked: %+v", items)
	}
}

func TestAuthorizationUsesMostSpecificEndpointBeforeGrants(t *testing.T) {
	dynamic, static, role := uuid.NewString(), uuid.NewString(), uuid.NewString()
	records := map[string]any{
		"endpoints":     []any{map[string]any{"id": dynamic, "service_resource": "orders", "method": "GET", "path_template": "/items/{id}", "enabled": true}, map[string]any{"id": static, "service_resource": "orders", "method": "GET", "path_template": "/items/export", "enabled": true}},
		"roles":         []any{map[string]any{"id": role, "enabled": true, "endpoint_ids": []any{dynamic}}},
		"subject_roles": []any{map[string]any{"subject": "u", "domain": "d", "role_id": role}},
	}
	input := map[string]any{"subject": "u", "domain": "d", "service_resource": "orders", "method": "GET", "path": "/items/export"}
	if d := evaluateCurrentPolicy(CurrentPolicy{Data: records}, input); d.Allowed {
		t.Fatalf("dynamic grant bypassed explicit export permission: %+v", d)
	}
	records["endpoints"].([]any)[1].(map[string]any)["enabled"] = false
	if d := evaluateCurrentPolicy(CurrentPolicy{Data: records}, input); d.Allowed || d.Reason != "endpoint_disabled" {
		t.Fatalf("disabled static endpoint bypassed: %+v", d)
	}
	input["path"] = "/items/123"
	if d := evaluateCurrentPolicy(CurrentPolicy{Data: records}, input); !d.Allowed {
		t.Fatalf("dynamic grant rejected: %+v", d)
	}
}

func TestNormalizeRBACDomain(t *testing.T) {
	for value, want := range map[string]string{
		"":         "",
		"   ":      "",
		"global":   "",
		" global ": "",
		"tenant-a": "tenant-a",
		"Global":   "Global",
	} {
		if got := NormalizeRBACDomain(value); got != want {
			t.Errorf("NormalizeRBACDomain(%q) = %q, want %q", value, got, want)
		}
	}
}

func defaultDomainPolicyData(domain string) map[string]any {
	endpointID, roleID := "endpoint-1", "role-1"
	return map[string]any{
		"endpoints":     []any{map[string]any{"id": endpointID, "service_resource": "order-service", "method": "GET", "path_template": "/books", "enabled": true}},
		"roles":         []any{map[string]any{"id": roleID, "code": "reader", "enabled": true, "endpoint_ids": []any{endpointID}}},
		"subject_roles": []any{map[string]any{"subject": "user-1", "domain": domain, "role_id": roleID}},
	}
}

func TestAuthorizeFoldsDefaultDomainAliases(t *testing.T) {
	service := NewRBACV1Service(&rbacV1FakeRepository{active: CurrentPolicy{Data: defaultDomainPolicyData("")}})
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"domain omitted", map[string]any{"subject": "user-1", "service_resource": "order-service", "method": "GET", "path": "/books"}},
		{"domain empty", map[string]any{"subject": "user-1", "domain": "", "service_resource": "order-service", "method": "GET", "path": "/books"}},
		{"domain blank", map[string]any{"subject": "user-1", "domain": "   ", "service_resource": "order-service", "method": "GET", "path": "/books"}},
		{"domain legacy global", map[string]any{"subject": "user-1", "domain": "global", "service_resource": "order-service", "method": "GET", "path": "/books"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := service.Authorize(context.Background(), "books", tc.input)
			if err != nil || !decision.Allowed {
				t.Fatalf("default-domain binding should allow: decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestAuthorizeKeepsDefaultAndNamedDomainsIsolated(t *testing.T) {
	defaultBinding := NewRBACV1Service(&rbacV1FakeRepository{active: CurrentPolicy{Data: defaultDomainPolicyData("")}})
	decision, err := defaultBinding.Authorize(context.Background(), "books", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("default binding leaked into a named domain: %+v", decision)
	}

	namedBinding := NewRBACV1Service(&rbacV1FakeRepository{active: CurrentPolicy{Data: defaultDomainPolicyData("tenant-a")}})
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"default omitted", map[string]any{"subject": "user-1", "service_resource": "order-service", "method": "GET", "path": "/books"}},
		{"legacy global alias", map[string]any{"subject": "user-1", "domain": "global", "service_resource": "order-service", "method": "GET", "path": "/books"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := namedBinding.Authorize(context.Background(), "books", tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed || decision.Reason != "endpoint_not_granted" {
				t.Fatalf("named binding leaked into the default domain: %+v", decision)
			}
		})
	}
	allowed, err := namedBinding.Authorize(context.Background(), "books", map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"})
	if err != nil || !allowed.Allowed {
		t.Fatalf("named binding should allow its own domain: %+v err=%v", allowed, err)
	}
}

func TestSubjectMenusFoldsDefaultDomainAliases(t *testing.T) {
	role := uuid.New()
	menu := uuid.New()
	records := map[string]any{
		"menus":         []any{map[string]any{"id": menu.String(), "enabled": true, "menu_type": "menu"}},
		"roles":         []any{map[string]any{"id": role.String(), "enabled": true, "menu_ids": []any{menu.String()}}},
		"subject_roles": []any{map[string]any{"subject": "u", "domain": "", "role_id": role.String()}},
	}
	for _, domain := range []string{"", "   ", "global"} {
		if items := subjectMenusFromPolicy(records, "u", domain); len(items) != 1 || items[0].ID != menu {
			t.Fatalf("default-domain menus for %q = %+v", domain, items)
		}
	}
	if items := subjectMenusFromPolicy(records, "u", "tenant-a"); len(items) != 0 {
		t.Fatalf("default-domain menus leaked into a named domain: %+v", items)
	}
	records["subject_roles"] = []any{map[string]any{"subject": "u", "domain": "global", "role_id": role.String()}}
	if items := subjectMenusFromPolicy(records, "u", ""); len(items) != 1 {
		t.Fatalf("legacy global menus did not fold into the default domain: %+v", items)
	}
}

func TestReplaceSubjectRolesNormalizesAndValidatesDomain(t *testing.T) {
	for _, domain := range []string{"", "   ", "global"} {
		repository := &recordingRBACV1Repository{}
		if err := NewRBACV1Service(repository).ReplaceSubjectRoles(context.Background(), "books", "user-1", domain, []uuid.UUID{uuid.New()}); err != nil {
			t.Fatal(err)
		}
		if repository.subject != "user-1" || repository.domain != "" {
			t.Fatalf("domain %q recorded as subject=%q domain=%q", domain, repository.subject, repository.domain)
		}
	}
	named := &recordingRBACV1Repository{}
	if err := NewRBACV1Service(named).ReplaceSubjectRoles(context.Background(), "books", "user-1", "tenant-a", nil); err != nil {
		t.Fatal(err)
	}
	if named.domain != "tenant-a" {
		t.Fatalf("named domain recorded as %q", named.domain)
	}
	if err := NewRBACV1Service(&recordingRBACV1Repository{}).ReplaceSubjectRoles(context.Background(), "books", "  ", "global", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank subject accepted: %v", err)
	}
}
