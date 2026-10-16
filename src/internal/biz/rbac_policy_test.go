package biz

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestCompiledPolicyMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	templates := []string{"/", "/items", "/items/{id}", "/items/export", "/{kind}/export", "/{kind}/{id}", "/items/{id}/detail", "/{kind}/fixed/detail", "/items/fixed/{part}", "/items//detail", "/items/{}", "//items/export/", "/items/{id}/"}
	paths := []string{"/", "/items", "/items/1", "/items/export", "/other/export", "/items/1/detail", "/items/fixed/detail", "/items/fixed/missing", "/items//detail", "/items/{}", "/items/1/", "//items/export//", "/unregistered"}
	methods := []string{"GET", "POST", "get", "k", "K", "s", "ſ"}
	for trial := 0; trial < 150; trial++ {
		endpoints := []any{}
		for i := 0; i < 30; i++ {
			endpoints = append(endpoints, map[string]any{"id": fmt.Sprint(i), "service_resource": fmt.Sprint(rng.Intn(2)), "method": methods[rng.Intn(len(methods))], "path_template": templates[rng.Intn(len(templates))], "enabled": rng.Intn(4) != 0})
		}
		roles := []any{}
		for i := 0; i < 10; i++ {
			grants := []any{}
			for j := 0; j < 8; j++ {
				grants = append(grants, fmt.Sprint(rng.Intn(35)))
			}
			roles = append(roles, map[string]any{"id": fmt.Sprint(i), "code": fmt.Sprintf("role-%d", i), "enabled": rng.Intn(4) != 0, "endpoint_ids": grants})
		}
		bindings := []any{}
		for i := 0; i < 35; i++ {
			bindings = append(bindings, map[string]any{"subject": fmt.Sprint(rng.Intn(3)), "domain": fmt.Sprint(rng.Intn(2)), "role_id": fmt.Sprint(rng.Intn(12))})
		}
		state := CurrentPolicy{Data: map[string]any{"endpoints": endpoints, "roles": roles, "subject_roles": bindings}}
		policy := compileRBACPolicy(state)
		for request := 0; request < 150; request++ {
			input := map[string]any{"subject": fmt.Sprint(rng.Intn(4)), "domain": fmt.Sprint(rng.Intn(3)), "service_resource": fmt.Sprint(rng.Intn(3)), "method": methods[rng.Intn(len(methods))], "path": paths[rng.Intn(len(paths))]}
			want := evaluateCurrentPolicyLinear(state, input)
			got := policy.Authorize(input)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d input %v\ngot %+v\nwant %+v", trial, input, got, want)
			}
		}
	}
}

func TestCompiledRouteFallbackAndAmbiguity(t *testing.T) {
	for _, tt := range []struct {
		name         string
		templates    []string
		path, reason string
	}{
		{"literal branch dead end", []string{"/a/b/no", "/{x}/b/yes"}, "/a/b/yes", "allowed"},
		{"later literals win", []string{"/a/{x}/{y}", "/{x}/b/c"}, "/a/b/c", "allowed"},
		{"equal scores conflict", []string{"/a/{x}/c", "/{x}/b/c"}, "/a/b/c", "ambiguous_endpoint"},
		{"equivalent parameter shapes conflict", []string{"/a/{id}", "/a/{name}"}, "/a/1", "ambiguous_endpoint"},
		{"static beats ambiguity", []string{"/{x}/b", "/a/{y}", "/a/b"}, "/a/b", "allowed"},
		{"parameter cannot consume empty segment", []string{"/a/{id}/b"}, "/a//b", "endpoint_not_found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			endpoints, ids := []any{}, []any{}
			for i, path := range tt.templates {
				id := fmt.Sprint(i)
				endpoints = append(endpoints, map[string]any{"id": id, "service_resource": "svc", "method": "GET", "path_template": path, "enabled": true})
				ids = append(ids, id)
			}
			state := CurrentPolicy{Data: map[string]any{"endpoints": endpoints, "roles": []any{map[string]any{"id": "r", "code": "reader", "enabled": true, "endpoint_ids": ids}}, "subject_roles": []any{map[string]any{"subject": "u", "domain": "d", "role_id": "r"}}}}
			input := map[string]any{"subject": "u", "domain": "d", "service_resource": "svc", "method": "GET", "path": tt.path}
			if decision := compileRBACPolicy(state).Authorize(input); decision.Reason != tt.reason {
				t.Fatalf("decision %+v", decision)
			}
		})
	}
}

func TestRoleBitmapIntersectionAcrossSparseWords(t *testing.T) {
	bitmap := func(ordinals ...uint32) roleBitmap {
		words := map[uint32]uint64{}
		for _, ordinal := range ordinals {
			addRole(words, ordinal)
		}
		return freezeRoleBitmap(words)
	}
	for _, tt := range []struct {
		a, b roleBitmap
		want bool
	}{
		{bitmap(), bitmap(1), false},
		{bitmap(1), bitmap(1), true},
		{bitmap(1), bitmap(65), false},
		{bitmap(65), bitmap(64), false},
		{bitmap(65, 10000), bitmap(1, 65), true},
		{bitmap(65, 10000), bitmap(1, 9999), false},
		{bitmap(10000), bitmap(1, 65, 10000), true},
	} {
		if got := tt.a.intersects(tt.b); got != tt.want {
			t.Fatalf("intersection = %v, want %v", got, tt.want)
		}
		if got := tt.b.intersects(tt.a); got != tt.want {
			t.Fatalf("reverse intersection = %v, want %v", got, tt.want)
		}
	}
}

func TestCompiledPolicyDecisionCannotMutateIndex(t *testing.T) {
	policy := compileRBACPolicy(CurrentPolicy{Data: directPolicyData(true, true)})
	input := map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"}
	first := policy.Authorize(input)
	first.MatchedRoles[0] = "changed"
	if next := policy.Authorize(input); !next.Allowed || next.MatchedRoles[0] != "reader" {
		t.Fatalf("caller mutated index: %+v", next)
	}
}

func TestCompiledPolicyRoleOrdinalsDoNotAliasAcrossWords(t *testing.T) {
	state := CurrentPolicy{Data: directPolicyData(true, false)}
	roles := []any{}
	for i := 0; i < 130; i++ {
		grants := []any{}
		if i == 129 {
			grants = append(grants, "endpoint-1")
		}
		roles = append(roles, map[string]any{"id": fmt.Sprint(i), "code": fmt.Sprint(i), "enabled": true, "endpoint_ids": grants})
	}
	state.Data["roles"] = roles
	state.Data["subject_roles"] = []any{
		map[string]any{"subject": "granted", "domain": "tenant", "role_id": "129"},
		map[string]any{"subject": "denied", "domain": "tenant", "role_id": "1"},
	}
	policy := compileRBACPolicy(state)
	input := map[string]any{"service_resource": "order-service", "method": "GET", "path": "/books", "subject": "granted", "domain": "tenant"}
	if !policy.Authorize(input).Allowed {
		t.Fatal("role 129 grant lost")
	}
	input["subject"] = "denied"
	if policy.Authorize(input).Allowed {
		t.Fatal("role 1 aliased role 129")
	}
}
