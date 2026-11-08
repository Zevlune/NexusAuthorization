package biz

import (
	"testing"

	"github.com/google/uuid"
)

// routeGrantCase compiles a catalog where only the listed templates are
// granted, then authorizes one GET request through both compiled paths.
type routeGrantCase struct {
	name      string
	templates []string
	granted   []string
	path      string
	reason    string
}

func routeGrantCases() []routeGrantCase {
	return []routeGrantCase{
		{"exact case still allowed", []string{"/api/products/{id}", "/api/products/export"}, []string{"/api/products/{id}"}, "/api/products/1", "allowed"},
		{"exact case literal stays denied", []string{"/api/products/{id}", "/api/products/export"}, []string{"/api/products/{id}"}, "/api/products/export", "endpoint_not_granted"},
		// ASP.NET Core dispatches /EXPORT to the export action; the case-sensitive
		// match alone would pick the granted {id} route and allow it.
		{"case variant cannot reach ungranted literal", []string{"/api/products/{id}", "/api/products/export"}, []string{"/api/products/{id}"}, "/api/products/EXPORT", "ambiguous_endpoint"},
		{"case variant of the whole path", []string{"/api/products/{id}", "/api/products/export"}, []string{"/api/products/{id}"}, "/API/PRODUCTS/EXPORT", "ambiguous_endpoint"},
		// Go's ServeMux dispatches /report to {name}; the folded match alone
		// would pick the granted Report literal and allow it.
		{"folded literal cannot authorize a parameter route", []string{"/api/Report", "/api/{name}"}, []string{"/api/Report"}, "/api/report", "ambiguous_endpoint"},
		{"folded literal exact request allowed", []string{"/api/Report", "/api/{name}"}, []string{"/api/Report"}, "/api/Report", "allowed"},
		{"unicode fold variant denied", []string{"/api/{x}", "/api/settings"}, []string{"/api/{x}"}, "/api/ſettings", "ambiguous_endpoint"},
		{"wrong case without other route denied", []string{"/api/orders"}, []string{"/api/orders"}, "/API/ORDERS", "ambiguous_endpoint"},
		// A trailing slash must not raise a template's priority to tie a literal.
		{"trailing slash template does not tie literal", []string{"/items/{id}/", "/items/export"}, []string{"/items/export"}, "/items/export", "allowed"},
		{"trailing slash template still matches", []string{"/items/{id}/", "/items/export"}, []string{"/items/{id}/"}, "/items/1", "allowed"},
		{"empty braces is a literal", []string{"/items/{}", "/items/{id}"}, []string{"/items/{}"}, "/items/{}", "allowed"},
		{"leading double slash does not tie literal", []string{"//items/{id}", "/items/export"}, []string{"/items/export"}, "/items/export", "allowed"},
	}
}

func TestCompiledPolicyRouteCaseAndSpecificity(t *testing.T) {
	for _, tt := range routeGrantCases() {
		t.Run(tt.name, func(t *testing.T) {
			endpoints, grants := []any{}, []any{}
			for i, template := range tt.templates {
				id := uuid.NewSHA1(uuid.Nil, []byte{byte(i)}).String()
				endpoints = append(endpoints, map[string]any{"id": id, "service_resource": "svc", "method": "GET", "path_template": template, "enabled": true})
				for _, granted := range tt.granted {
					if granted == template {
						grants = append(grants, id)
					}
				}
			}
			state := CurrentPolicy{Data: map[string]any{"endpoints": endpoints, "roles": []any{map[string]any{"id": "r", "code": "reader", "enabled": true, "endpoint_ids": grants}}, "subject_roles": []any{map[string]any{"subject": "u", "domain": "", "role_id": "r"}}}}
			input := map[string]any{"subject": "u", "domain": "", "service_resource": "svc", "method": "GET", "path": tt.path}
			if decision := compileRBACPolicy(state).Authorize(input); decision.Reason != tt.reason {
				t.Fatalf("current policy decision %+v, want %s", decision, tt.reason)
			}
			if want := evaluateCurrentPolicyLinear(state, input); want.Reason != tt.reason {
				t.Fatalf("reference decision %+v, want %s", want, tt.reason)
			}
		})
	}
}

func TestSubjectAPIPolicyRouteCaseAndSpecificity(t *testing.T) {
	for _, tt := range routeGrantCases() {
		t.Run(tt.name, func(t *testing.T) {
			policy := SubjectAPIPolicy{SubjectAPIIdentity: SubjectAPIIdentity{Subject: "u", ServiceResource: "svc", ServiceActive: true}}
			for i, template := range tt.templates {
				endpoint := SubjectAPIEndpoint{ID: uuid.NewSHA1(uuid.Nil, []byte{byte(i)}), Method: "GET", PathTemplate: template, Enabled: true}
				policy.Catalog = append(policy.Catalog, endpoint)
				for _, granted := range tt.granted {
					if granted == template {
						policy.APIs = append(policy.APIs, endpoint)
					}
				}
			}
			input := map[string]any{"subject": "u", "domain": "", "service_resource": "svc", "method": "GET", "path": tt.path}
			if decision := compileSubjectAPIPolicy(policy).Authorize(input); decision.Reason != tt.reason {
				t.Fatalf("subject api decision %+v, want %s", decision, tt.reason)
			}
		})
	}
}
