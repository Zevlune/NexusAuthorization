package biz

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// subjectAPIRepositoryFixture serves the aggregated SubjectAPIRepository
// contract and records every read, so tests can prove that the service never
// substitutes the local compiled cache for the per-request repository call.
type subjectAPIRepositoryFixture struct {
	rbacV1FakeRepository

	mu       sync.Mutex
	policies map[string]SubjectAPIPolicy
	requests []subjectAPIRequest
	err      error
}

type subjectAPIRequest struct {
	app     string
	subject string
	domain  string
	service string
}

func newSubjectAPIRepositoryFixture() *subjectAPIRepositoryFixture {
	return &subjectAPIRepositoryFixture{policies: map[string]SubjectAPIPolicy{}}
}

func subjectAPIRouteKey(app, subject, domain, service string) string {
	return strings.Join([]string{app, subject, NormalizeRBACDomain(domain), service}, "\x00")
}

func (r *subjectAPIRepositoryFixture) register(app, subject, domain, service string, policy SubjectAPIPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[subjectAPIRouteKey(app, subject, domain, service)] = policy
}

func (r *subjectAPIRepositoryFixture) setError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *subjectAPIRepositoryFixture) GetSubjectAPIs(_ context.Context, app, subject, domain, service string) (SubjectAPIPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, subjectAPIRequest{app: app, subject: subject, domain: domain, service: service})
	if r.err != nil {
		return SubjectAPIPolicy{}, r.err
	}
	policy, ok := r.policies[subjectAPIRouteKey(app, subject, domain, service)]
	if !ok {
		return SubjectAPIPolicy{}, ErrNotFound
	}
	return policy, nil
}

func (r *subjectAPIRepositoryFixture) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *subjectAPIRepositoryFixture) lastRequest() subjectAPIRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return subjectAPIRequest{}
	}
	return r.requests[len(r.requests)-1]
}

func newSubjectAPITestService(repository RBACV1Repository, capacity int) *RBACV1Service {
	return &RBACV1Service{repository: repository, policies: newRBACPolicyCache(64), subjectAPIs: newSubjectAPICache(capacity)}
}

func subjectAPIEndpoint(path string, enabled bool) SubjectAPIEndpoint {
	return SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: path, Enabled: enabled}
}

func subjectAPITestInput(subject, domain, service, path string) map[string]any {
	return map[string]any{"subject": subject, "domain": domain, "service_resource": service, "method": "GET", "path": path}
}

func subjectAPIPolicyFor(app string, appID uuid.UUID, subject, domain, service string, active bool, apis, catalog []SubjectAPIEndpoint, roleCodes []string) SubjectAPIPolicy {
	return SubjectAPIPolicy{
		SubjectAPIIdentity: SubjectAPIIdentity{
			ApplicationKey:  app,
			ApplicationID:   appID,
			Subject:         subject,
			Domain:          NormalizeRBACDomain(domain),
			ServiceResource: service,
			ServiceActive:   active,
		},
		APIs:      apis,
		Catalog:   catalog,
		RoleCodes: roleCodes,
	}
}

func mustSubjectAPIAuthorize(t *testing.T, service *RBACV1Service, app string, input map[string]any) RBACDecision {
	t.Helper()
	decision, err := service.Authorize(context.Background(), app, input)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return decision
}

func mustSubjectAPIPolicyKey(t *testing.T, policy SubjectAPIPolicy) subjectAPICacheKey {
	t.Helper()
	key, err := subjectAPIPolicyKey(policy)
	if err != nil {
		t.Fatalf("policy key: %v", err)
	}
	return key
}

func TestSubjectAPIAuthorizeServicePolicyWithoutApplicationMetadata(t *testing.T) {
	endpoint := subjectAPIEndpoint("/books", true)
	policy := subjectAPIPolicyFor("", uuid.Nil, "user-1", "", "orders", true,
		[]SubjectAPIEndpoint{endpoint}, []SubjectAPIEndpoint{endpoint}, []string{"reader"})
	fixture := newSubjectAPIRepositoryFixture()
	service := newSubjectAPITestService(fixture, 16)
	for _, app := range []string{"console", "billing"} {
		fixture.register(app, "user-1", "", "orders", policy)
		decision := mustSubjectAPIAuthorize(t, service, app, subjectAPITestInput("user-1", "", "orders", "/books"))
		if !decision.Allowed || decision.Reason != "allowed" {
			t.Fatalf("service-level grant via %s = %+v", app, decision)
		}
	}
}

func TestSubjectAPIAuthorizeReadsAggregateEveryRequestAndAppliesChanges(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, catalog, catalog, []string{"reader"}))
	service := newSubjectAPITestService(fixture, 16)
	input := subjectAPITestInput("user-1", "", "orders", "/books")

	for i := 0; i < 2; i++ {
		if decision := mustSubjectAPIAuthorize(t, service, "console", input); !decision.Allowed {
			t.Fatalf("request %d denied: %+v", i+1, decision)
		}
	}
	if reads := fixture.readCount(); reads != 2 {
		t.Fatalf("repository reads = %d, want one per request", reads)
	}

	// The aggregate changes without any metadata or token change. The next
	// request must observe it even though the route catalog is identical.
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, nil, catalog, nil))
	if decision := mustSubjectAPIAuthorize(t, service, "console", input); decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("changed aggregate was not applied: %+v", decision)
	}
	if reads := fixture.readCount(); reads != 3 {
		t.Fatalf("repository reads = %d, want 3", reads)
	}

	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, catalog, catalog, []string{"reader"}))
	if decision := mustSubjectAPIAuthorize(t, service, "console", input); !decision.Allowed {
		t.Fatalf("restored aggregate denied: %+v", decision)
	}
	if reads := fixture.readCount(); reads != 4 {
		t.Fatalf("repository reads = %d, want 4", reads)
	}
}

func TestSubjectAPIAuthorizeScopeIsolation(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "tenant-a", "orders", subjectAPIPolicyFor("console", appID, "user-1", "tenant-a", "orders", true, catalog, catalog, []string{"reader"}))
	fixture.register("console", "user-2", "tenant-a", "orders", subjectAPIPolicyFor("console", appID, "user-2", "tenant-a", "orders", true, nil, catalog, nil))
	fixture.register("console", "user-1", "tenant-b", "orders", subjectAPIPolicyFor("console", appID, "user-1", "tenant-b", "orders", true, nil, catalog, nil))
	fixture.register("console", "user-1", "tenant-a", "inventory", subjectAPIPolicyFor("console", appID, "user-1", "tenant-a", "inventory", true, nil, catalog, nil))
	fixture.register("other-app", "user-1", "tenant-a", "orders", subjectAPIPolicyFor("other-app", appID, "user-1", "tenant-a", "orders", true, nil, catalog, nil))
	service := newSubjectAPITestService(fixture, 16)

	cases := []struct {
		name, app, subject, domain, service string
		allowed                             bool
	}{
		{"granted identity allows", "console", "user-1", "tenant-a", "orders", true},
		{"other subject denies", "console", "user-2", "tenant-a", "orders", false},
		{"other domain denies", "console", "user-1", "tenant-b", "orders", false},
		{"other service denies", "console", "user-1", "tenant-a", "inventory", false},
		{"other application denies", "other-app", "user-1", "tenant-a", "orders", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := mustSubjectAPIAuthorize(t, service, tc.app, subjectAPITestInput(tc.subject, tc.domain, tc.service, "/books"))
			if decision.Allowed != tc.allowed {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestSubjectAPIAuthorizeNormalizesRequestAndReturnedIdentity(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	policy := subjectAPIPolicyFor(" console ", appID, " user-1 ", " global ", " orders ", true, catalog, catalog, []string{" reader "})
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", policy)
	service := newSubjectAPITestService(fixture, 16)

	input := map[string]any{"subject": "  user-1  ", "domain": " global ", "service_resource": " orders ", "method": "GET", "path": "/books"}
	keys := len(input)
	decision := mustSubjectAPIAuthorize(t, service, "  console  ", input)
	if !decision.Allowed {
		t.Fatalf("normalized request denied: %+v", decision)
	}
	if input["subject"] != "  user-1  " || input["service_resource"] != " orders " || input["domain"] != " global " || len(input) != keys {
		t.Fatalf("caller input mutated: %+v", input)
	}
	request := fixture.lastRequest()
	if request != (subjectAPIRequest{app: "console", subject: "user-1", domain: "", service: "orders"}) {
		t.Fatalf("repository request = %+v", request)
	}
	if strings.Join(decision.MatchedRoles, ",") != "reader" {
		t.Fatalf("matched roles = %v", decision.MatchedRoles)
	}
}

func TestSubjectAPIAuthorizeFailsClosedOnEmptyAndRepositoryErrors(t *testing.T) {
	input := subjectAPITestInput("user-1", "", "orders", "/books")

	emptyFixture := newSubjectAPIRepositoryFixture()
	emptyFixture.register("console", "user-1", "", "orders", SubjectAPIPolicy{})
	decision, err := newSubjectAPITestService(emptyFixture, 16).Authorize(context.Background(), "console", input)
	if !errors.Is(err, ErrNotFound) || decision.Allowed {
		t.Fatalf("empty aggregate must fail closed: decision=%+v err=%v", decision, err)
	}

	errorFixture := newSubjectAPIRepositoryFixture()
	errorFixture.setError(errors.New("redis unavailable"))
	decision, err = newSubjectAPITestService(errorFixture, 16).Authorize(context.Background(), "console", input)
	if err == nil || decision.Allowed {
		t.Fatalf("repository error must fail closed: decision=%+v err=%v", decision, err)
	}
}

func TestSubjectAPIAuthorizeRejectsReturnedIdentityMismatch(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	mutations := map[string]func(*SubjectAPIIdentity){
		"application key": func(identity *SubjectAPIIdentity) { identity.ApplicationKey = "other-app" },
		"subject":         func(identity *SubjectAPIIdentity) { identity.Subject = "user-2" },
		"domain":          func(identity *SubjectAPIIdentity) { identity.Domain = "tenant-b" },
		"service":         func(identity *SubjectAPIIdentity) { identity.ServiceResource = "inventory" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			policy := subjectAPIPolicyFor("console", appID, "user-1", "tenant-a", "orders", true, catalog, catalog, []string{"reader"})
			mutate(&policy.SubjectAPIIdentity)
			fixture := newSubjectAPIRepositoryFixture()
			fixture.register("console", "user-1", "tenant-a", "orders", policy)

			decision, err := newSubjectAPITestService(fixture, 16).Authorize(context.Background(), "console", subjectAPITestInput("user-1", "tenant-a", "orders", "/books"))
			if !errors.Is(err, ErrConflict) || decision.Allowed {
				t.Fatalf("identity mismatch must fail closed: decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestSubjectAPIAuthorizeDeniesInactiveService(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", false, catalog, catalog, []string{"reader"}))

	decision := mustSubjectAPIAuthorize(t, newSubjectAPITestService(fixture, 16), "console", subjectAPITestInput("user-1", "", "orders", "/books"))
	if decision.Allowed || decision.Reason != "service_inactive" {
		t.Fatalf("inactive service must deny: %+v", decision)
	}
}

func TestSubjectAPIAuthorizeKeepsCatalogRouteSpecificity(t *testing.T) {
	appID := uuid.New()
	wide := subjectAPIEndpoint("/items/{id}", true)
	export := subjectAPIEndpoint("/items/export", true)
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, []SubjectAPIEndpoint{wide}, []SubjectAPIEndpoint{wide, export}, []string{"reader"}))
	service := newSubjectAPITestService(fixture, 16)

	if decision := mustSubjectAPIAuthorize(t, service, "console", subjectAPITestInput("user-1", "", "orders", "/items/export")); decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("ungranted specific path was widened by a template: %+v", decision)
	}
	if decision := mustSubjectAPIAuthorize(t, service, "console", subjectAPITestInput("user-1", "", "orders", "/items/123")); !decision.Allowed {
		t.Fatalf("granted template denied: %+v", decision)
	}

	disabled := export
	disabled.Enabled = false
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, []SubjectAPIEndpoint{wide}, []SubjectAPIEndpoint{wide, disabled}, []string{"reader"}))
	if decision := mustSubjectAPIAuthorize(t, service, "console", subjectAPITestInput("user-1", "", "orders", "/items/export")); decision.Allowed || decision.Reason != "endpoint_disabled" {
		t.Fatalf("disabled specific path fell back to a wide grant: %+v", decision)
	}
}

func TestSubjectAPIAuthorizeDeniesAmbiguousRoute(t *testing.T) {
	appID := uuid.New()
	first := subjectAPIEndpoint("/items/{id}", true)
	second := subjectAPIEndpoint("/items/{name}", true)
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, []SubjectAPIEndpoint{first}, []SubjectAPIEndpoint{first, second}, []string{"reader"}))

	decision := mustSubjectAPIAuthorize(t, newSubjectAPITestService(fixture, 16), "console", subjectAPITestInput("user-1", "", "orders", "/items/123"))
	if decision.Allowed || decision.Reason != "ambiguous_endpoint" {
		t.Fatalf("ambiguous route must deny: %+v", decision)
	}
}

func TestSubjectAPIAuthorizeReportsRoleUnionAndDoesNotLeakCachedRoles(t *testing.T) {
	appID := uuid.New()
	first := subjectAPIEndpoint("/a", true)
	second := subjectAPIEndpoint("/b", true)
	catalog := []SubjectAPIEndpoint{first, second}
	fixture := newSubjectAPIRepositoryFixture()
	fixture.register("console", "user-1", "", "orders", subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, catalog, catalog, []string{"reader", "writer", "reader"}))
	service := newSubjectAPITestService(fixture, 16)

	decision := mustSubjectAPIAuthorize(t, service, "console", subjectAPITestInput("user-1", "", "orders", "/a"))
	if !decision.Allowed || strings.Join(decision.MatchedRoles, ",") != "reader,writer" {
		t.Fatalf("matched roles = %v allowed = %v", decision.MatchedRoles, decision.Allowed)
	}
	decision.MatchedRoles[0] = "mutated"
	again := mustSubjectAPIAuthorize(t, service, "console", subjectAPITestInput("user-1", "", "orders", "/a"))
	if strings.Join(again.MatchedRoles, ",") != "reader,writer" {
		t.Fatalf("returned roles share cached state: %v", again.MatchedRoles)
	}
}

func TestSubjectAPICompileMatchesOriginalEngine(t *testing.T) {
	endpoints := []SubjectAPIEndpoint{
		{ID: uuid.New(), Method: "GET", PathTemplate: "/a/{}", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/a/{id}", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/b/{", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/b/{c}", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/d/{x}/e", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/d/f/{y}", Enabled: true},
		{ID: uuid.New(), Method: "GET", PathTemplate: "/e/{", Enabled: false},
	}
	identity := SubjectAPIIdentity{ApplicationKey: "console", ApplicationID: uuid.New(), Subject: "user-1", ServiceResource: "orders", ServiceActive: true}
	aggregated := compileSubjectAPIPolicy(SubjectAPIPolicy{SubjectAPIIdentity: identity, APIs: endpoints, Catalog: endpoints, RoleCodes: []string{"reader"}})
	original := compileRBACPolicy(CurrentPolicy{Data: originalSubjectAPIData("orders", endpoints)})
	for _, path := range []string{"/a/{}", "/a/1", "/a/x/y", "/b/{", "/b/2", "/d/1/e", "/d/f/2", "/d/f/e", "/e/{}", "/e/1", "/missing"} {
		input := map[string]any{"subject": "user-1", "domain": "", "service_resource": "orders", "method": "GET", "path": path}
		got, want := aggregated.Authorize(input), original.Authorize(input)
		if got.Allowed != want.Allowed || got.Reason != want.Reason {
			t.Fatalf("path %q: aggregated=%+v original=%+v", path, got, want)
		}
	}
}

func TestSubjectAPICompiledCacheEvictsLeastRecentlyUsed(t *testing.T) {
	appID := uuid.New()
	cache := newSubjectAPICache(2)
	policies := []SubjectAPIPolicy{
		subjectAPIPolicyFor("console", appID, "u1", "", "orders", true, nil, nil, nil),
		subjectAPIPolicyFor("console", appID, "u2", "", "orders", true, nil, nil, nil),
		subjectAPIPolicyFor("console", appID, "u3", "", "orders", true, nil, nil, nil),
	}
	keys := make([]subjectAPICacheKey, len(policies))
	for i, policy := range policies {
		keys[i] = mustSubjectAPIPolicyKey(t, policy)
	}
	for i := 0; i < 2; i++ {
		if _, err := cache.resolve(context.Background(), policies[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Touch the first entry so the second is the least recently used.
	if cache.get(keys[0]) == nil {
		t.Fatal("first entry missing before eviction")
	}
	if _, err := cache.resolve(context.Background(), policies[2]); err != nil {
		t.Fatal(err)
	}
	if cache.get(keys[1]) != nil {
		t.Fatal("least recently used entry survived")
	}
	if cache.get(keys[0]) == nil || cache.get(keys[2]) == nil {
		t.Fatal("live entries were evicted")
	}
}

func TestSubjectAPICompiledCacheReplacesChangedGenerationForScope(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	catalog := []SubjectAPIEndpoint{books}
	first := subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, catalog, catalog, []string{"reader"})
	second := subjectAPIPolicyFor("console", appID, "user-1", "", "orders", true, nil, catalog, nil)
	cache := newSubjectAPICache(4)

	if _, err := cache.resolve(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.resolve(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	firstKey := mustSubjectAPIPolicyKey(t, first)
	secondKey := mustSubjectAPIPolicyKey(t, second)
	if firstKey == secondKey {
		t.Fatal("changed aggregate reused a compiled cache key")
	}
	if cache.get(firstKey) != nil {
		t.Fatal("stale compiled generation retained for the same scope")
	}
	if cache.get(secondKey) == nil {
		t.Fatal("new compiled generation missing")
	}
}

func TestSubjectAPIPolicyKeyCoversScopeAndContent(t *testing.T) {
	appID := uuid.New()
	books := subjectAPIEndpoint("/books", true)
	base := subjectAPIPolicyFor("console", appID, "user-1", "tenant-a", "orders", true, []SubjectAPIEndpoint{books}, []SubjectAPIEndpoint{books}, []string{"reader"})
	baseKey := mustSubjectAPIPolicyKey(t, base)

	mutations := map[string]func(*SubjectAPIPolicy){
		"application key": func(policy *SubjectAPIPolicy) { policy.ApplicationKey = "other-app" },
		"application id":  func(policy *SubjectAPIPolicy) { policy.ApplicationID = uuid.New() },
		"subject":         func(policy *SubjectAPIPolicy) { policy.Subject = "user-2" },
		"domain":          func(policy *SubjectAPIPolicy) { policy.Domain = "tenant-b" },
		"service":         func(policy *SubjectAPIPolicy) { policy.ServiceResource = "inventory" },
		"active":          func(policy *SubjectAPIPolicy) { policy.ServiceActive = false },
		"grants":          func(policy *SubjectAPIPolicy) { policy.APIs = nil },
		"catalog":         func(policy *SubjectAPIPolicy) { policy.Catalog = nil },
		"roles":           func(policy *SubjectAPIPolicy) { policy.RoleCodes = []string{"writer"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			policy := base
			mutate(&policy)
			if mustSubjectAPIPolicyKey(t, policy) == baseKey {
				t.Fatal("cache key ignored changed policy content")
			}
		})
	}
}

// originalSubjectAPIData builds the current-policy document the full engine
// compiles, granting every catalog endpoint to one enabled role.
func originalSubjectAPIData(service string, endpoints []SubjectAPIEndpoint) map[string]any {
	catalog := make([]any, 0, len(endpoints))
	grants := make([]any, 0, len(endpoints))
	for _, endpoint := range endpoints {
		catalog = append(catalog, map[string]any{
			"id":               endpoint.ID.String(),
			"service_resource": service,
			"method":           endpoint.Method,
			"path_template":    endpoint.PathTemplate,
			"enabled":          endpoint.Enabled,
		})
		grants = append(grants, endpoint.ID.String())
	}
	return map[string]any{
		"endpoints": catalog,
		"roles":     []any{map[string]any{"id": "role", "code": "reader", "enabled": true, "endpoint_ids": grants}},
		"subject_roles": []any{
			map[string]any{"subject": "user-1", "domain": "", "role_id": "role"},
		},
	}
}
