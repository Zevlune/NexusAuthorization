package biz

import (
	"fmt"
	"reflect"
	"testing"
)

func TestRBACLargeFanoutSharesRoleWithoutExpandingUserAPIs(t *testing.T) {
	const users, apis = 100000, 10000
	state := fanoutPolicyFixture(apis, users, 1, 1, apis)
	policy := compileRBACPolicy(state)
	if len(policy.principals) != users || len(policy.endpoints) != apis+1 {
		t.Fatal("unexpected index cardinality")
	}
	shared := policy.principals[policySubject{"user-0", "tenant"}]
	for key, principal := range policy.principals {
		if principal != shared {
			t.Fatalf("role data duplicated for %v", key)
		}
	}
	if len(shared.roles.words) != 1 || len(shared.names) != 1 {
		t.Fatal("singleton role expanded")
	}
	words := 0
	for _, endpoint := range policy.endpoints {
		words += len(endpoint.roles.words)
	}
	if words != apis {
		t.Fatalf("API index stores %d words for %d grants", words, apis)
	}
	for _, path := range []string{"/fanout/0", "/fanout/1/42", fmt.Sprintf("/fanout/%d", apis)} {
		input := map[string]any{"subject": "user-99999", "domain": "tenant", "service_resource": "orders", "method": "GET", "path": path}
		got, want := policy.Authorize(input), evaluateCurrentPolicyLinear(state, input)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v want %+v", path, got, want)
		}
	}
}

func TestRBACSharedSingleRoleCopyOnAdditionalBinding(t *testing.T) {
	state := fanoutPolicyFixture(10, 10, 2, 1, 5)
	bindings := state.Data["subject_roles"].([]any)
	state.Data["subject_roles"] = append(bindings, map[string]any{"subject": "user-0", "domain": "tenant", "role_id": "role-1"})
	policy := compileRBACPolicy(state)
	first := policy.principals[policySubject{"user-0", "tenant"}]
	other := policy.principals[policySubject{"user-2", "tenant"}]
	if first == other || len(first.names) != 2 || len(other.names) != 1 {
		t.Fatal("additional role mutated shared principal")
	}
	for _, user := range []string{"user-0", "user-2"} {
		input := map[string]any{"subject": user, "domain": "tenant", "service_resource": "orders", "method": "GET", "path": "/fanout/6"}
		if got, want := policy.Authorize(input), evaluateCurrentPolicyLinear(state, input); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v want %+v", user, got, want)
		}
	}
}
