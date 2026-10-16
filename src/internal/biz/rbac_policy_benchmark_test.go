package biz

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

var benchmarkRBACDecision RBACDecision
var benchmarkRBACPolicy *compiledRBACPolicy

func benchmarkPolicyFixture(size int) (CurrentPolicy, map[string]any) {
	endpoints, roles, bindings := make([]any, 0, size), make([]any, 0, size), make([]any, 0, size)
	for i := 0; i < size; i++ {
		id, role := fmt.Sprintf("endpoint-%d", i), fmt.Sprintf("role-%d", i)
		path := fmt.Sprintf("/items/%d", i)
		if i%2 == 1 {
			path += "/{id}"
		}
		endpoints = append(endpoints, map[string]any{"id": id, "service_resource": "orders", "method": "GET", "path_template": path, "enabled": true})
		roles = append(roles, map[string]any{"id": role, "code": role, "enabled": true, "endpoint_ids": []any{id}})
		bindings = append(bindings, map[string]any{"subject": fmt.Sprintf("user-%d", i), "domain": "tenant", "role_id": role})
	}
	return CurrentPolicy{Data: map[string]any{"endpoints": endpoints, "roles": roles, "subject_roles": bindings}}, map[string]any{"subject": fmt.Sprintf("user-%d", size-1), "domain": "tenant", "service_resource": "orders", "method": "GET", "path": fmt.Sprintf("/items/%d/123", size-1)}
}

func BenchmarkRBACPolicyMatching(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		state, input := benchmarkPolicyFixture(size)
		policy := compileRBACPolicy(state)
		for _, kind := range []string{"static", "parameter", "deny"} {
			switch kind {
			case "static":
				input["path"], input["subject"] = "/items/0", "user-0"
			case "parameter":
				input["path"], input["subject"] = fmt.Sprintf("/items/%d/123", size-1), fmt.Sprintf("user-%d", size-1)
			case "deny":
				input["path"], input["subject"] = fmt.Sprintf("/items/%d/123", size-1), "missing-user"
			}
			for _, indexed := range []bool{false, true} {
				name := "linear"
				if indexed {
					name = "indexed"
				}
				b.Run(fmt.Sprintf("%d/%s/%s", size, kind, name), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if indexed {
							benchmarkRBACDecision = policy.Authorize(input)
						} else {
							benchmarkRBACDecision = evaluateCurrentPolicyLinear(state, input)
						}
					}
				})
			}
		}
	}
}

func BenchmarkRBACPolicyCompile(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		state, _ := benchmarkPolicyFixture(size)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkRBACPolicy = compileRBACPolicy(state)
			}
		})
	}
}

func BenchmarkRBACPolicyServiceHot(b *testing.B) {
	state, input := benchmarkPolicyFixture(10000)
	state.ApplicationID, state.Token = uuid.New(), "benchmark"
	repo := &indexedPolicyRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}, active: state, states: map[string]CurrentPolicy{state.Token: state}}
	service := NewRBACV1Service(repo)
	ctx := context.Background()
	if _, err := service.Authorize(ctx, "app", input); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkRBACDecision, err = service.Authorize(ctx, "app", input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Binding volume, not request count. E distinct APIs; U users; K bindings/user;
// R roles; G grants/role. No U*E expansion is built by either fixture or index.
func fanoutPolicyFixture(apis, users, roleCount, rolesPerUser, grantsPerRole int) CurrentPolicy {
	endpoints := make([]any, 0, apis+1)
	for i := 0; i <= apis; i++ {
		path := fmt.Sprintf("/fanout/%d", i)
		if i%2 == 1 {
			path += "/{id}"
		}
		endpoints = append(endpoints, map[string]any{"id": fmt.Sprintf("api-%d", i), "service_resource": "orders", "method": "GET", "path_template": path, "enabled": true})
	}
	roles := make([]any, roleCount)
	for r := range roles {
		grants := make([]any, grantsPerRole)
		for i := range grants {
			grants[i] = fmt.Sprintf("api-%d", (r*grantsPerRole+i)%apis)
		}
		roles[r] = map[string]any{"id": fmt.Sprintf("role-%d", r), "code": fmt.Sprintf("role-%d", r), "enabled": true, "endpoint_ids": grants}
	}
	bindings := make([]any, 0, users*rolesPerUser)
	for u := 0; u < users; u++ {
		subject := fmt.Sprintf("user-%d", u)
		for r := 0; r < rolesPerUser; r++ {
			bindings = append(bindings, map[string]any{"subject": subject, "domain": "tenant", "role_id": fmt.Sprintf("role-%d", (u+r)%roleCount)})
		}
	}
	return CurrentPolicy{Data: map[string]any{"endpoints": endpoints, "roles": roles, "subject_roles": bindings}}
}

func BenchmarkRBACBindingVolume(b *testing.B) {
	for _, fixture := range []struct {
		name                     string
		apis, users, roles, k, g int
	}{
		{"single_10kAPI_100kUsers", 10000, 100000, 1, 1, 10000},
		{"single_100kAPI_1mUsers", 100000, 1000000, 1, 1, 100000},
		{"shared_100roles_400kBindings_100kGrants", 10000, 100000, 100, 4, 1000},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			state := fanoutPolicyFixture(fixture.apis, fixture.users, fixture.roles, fixture.k, fixture.g)
			b.Run("compile", func(b *testing.B) {
				benchmarkRBACPolicy = nil
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				b.ReportAllocs()
				for b.Loop() {
					benchmarkRBACPolicy = compileRBACPolicy(state)
				}
				runtime.GC()
				runtime.ReadMemStats(&after)
				if after.HeapAlloc >= before.HeapAlloc {
					b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "index_retained_B")
				}
				runtime.KeepAlive(state)
				runtime.KeepAlive(benchmarkRBACPolicy)
				b.ReportMetric(float64(fixture.users*fixture.k), "bindings")
				b.ReportMetric(float64(fixture.roles*fixture.g), "grants")
			})
			policy := compileRBACPolicy(state)
			for _, kind := range []string{"static_allow", "parameter_allow", "bound_deny", "unknown_deny", "distributed_allow"} {
				b.Run(kind, func(b *testing.B) {
					input := map[string]any{"subject": "user-0", "domain": "tenant", "service_resource": "orders", "method": "GET", "path": "/fanout/0"}
					switch kind {
					case "parameter_allow":
						input["path"] = "/fanout/1/42"
					case "bound_deny":
						input["path"] = fmt.Sprintf("/fanout/%d", fixture.apis)
					case "unknown_deny":
						input["subject"] = "not-bound"
					}
					inputs := []map[string]any{input}
					if kind == "distributed_allow" {
						inputs = make([]map[string]any, 4096)
						for i := range inputs {
							user := (i * 104729) % fixture.users
							api := ((user%fixture.roles)*fixture.g + (i*65537)%fixture.g) % fixture.apis
							path := fmt.Sprintf("/fanout/%d", api)
							if api%2 == 1 {
								path += "/42"
							}
							inputs[i] = map[string]any{"subject": fmt.Sprintf("user-%d", user), "domain": "tenant", "service_resource": "orders", "method": "GET", "path": path}
							if !policy.Authorize(inputs[i]).Allowed {
								b.Fatal("distributed fixture is not granted")
							}
						}
					}
					if decision := policy.Authorize(input); decision.Allowed != (kind == "static_allow" || kind == "parameter_allow" || kind == "distributed_allow") {
						b.Fatalf("unexpected decision: %+v", decision)
					}
					b.ReportAllocs()
					index := 0
					for b.Loop() {
						benchmarkRBACDecision = policy.Authorize(inputs[index])
						index++
						if index == len(inputs) {
							index = 0
						}
					}
				})
			}
		})
	}
}
