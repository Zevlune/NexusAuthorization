package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

// publicationFixture is an isolated database/cache scope. Each application
// contributes to the same globally aggregated service policies.
type publicationFixture struct {
	repo        *RBACV1Repository
	rbac        *biz.RBACV1Service
	cache       *rediscache.PolicyCache
	server      *miniredis.Miniredis
	app         string
	service     string
	role        biz.RBACRole
	endpoint    biz.RBACEndpoint
	application biz.Application
}

func newPublicationFixture(t *testing.T) publicationFixture {
	t.Helper()
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := newRepositoryTestPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	server := miniredis.RunT(t)
	cache, err := rediscache.New(config.RedisConfig{
		URL:       "redis://" + server.Addr(),
		KeyPrefix: "nexusauthorization:test:" + uuid.NewString(),
		CacheTTL:  "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	f := publicationFixture{cache: cache, server: server, app: "pub-app-" + uuid.NewString(), service: "pub-api-" + uuid.NewString()}
	if _, err := pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, f.service); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, f.app)
		pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, f.service)
		pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, f.service)
	})
	f.repo = NewRBACV1Repository(pool).WithPolicyCache(cache)
	f.rbac = biz.NewRBACV1Service(f.repo)
	if f.application, err = f.repo.SaveApplication(ctx, biz.Application{Key: f.app, DisplayName: f.app, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReplaceApplicationServices(ctx, f.app, []string{f.service}); err != nil {
		t.Fatal(err)
	}
	if f.endpoint, err = f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if f.role, err = f.repo.SaveRole(ctx, f.app, biz.RBACRole{Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{f.endpoint.ID}}); err != nil {
		t.Fatal(err)
	}
	return f
}

type publicationApplication struct {
	application biz.Application
	service     string
	role        biz.RBACRole
	endpoint    biz.RBACEndpoint
}

func (f publicationFixture) addApplication(t *testing.T, service, path string) publicationApplication {
	t.Helper()
	ctx := context.Background()
	if service == "" {
		service = f.service
	}
	if service != f.service {
		if _, err := f.repo.pool.Exec(ctx, `INSERT INTO service_resources(resource_key,display_name) VALUES($1,$1)`, service); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			f.repo.pool.Exec(ctx, `DELETE FROM api_endpoints WHERE service_resource_key=$1`, service)
			f.repo.pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1`, service)
		})
	}

	appKey := "pub-app-" + uuid.NewString()
	application, err := f.repo.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: appKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.repo.pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey)
	})
	if err := f.repo.ReplaceApplicationServices(ctx, appKey, []string{service}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := f.repo.SaveEndpoint(ctx, service, biz.RBACEndpoint{Method: "GET", PathTemplate: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := f.repo.SaveRole(ctx, appKey, biz.RBACRole{
		Code: "reader", Name: "Reader", Enabled: true, EndpointIDs: []uuid.UUID{endpoint.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return publicationApplication{application: application, service: service, role: role, endpoint: endpoint}
}

func (f publicationFixture) allowed(t *testing.T, subject string) bool {
	t.Helper()
	decision, err := f.rbac.Authorize(context.Background(), f.app, map[string]any{"service_resource": f.service, "subject": subject, "method": "GET", "path": "/orders"})
	if err != nil {
		t.Fatalf("authorize %s: %v", subject, err)
	}
	return decision.Allowed
}

func (f publicationFixture) allowedAt(t *testing.T, app, service, subject, domain, path string) bool {
	t.Helper()
	decision, err := f.rbac.Authorize(context.Background(), app, map[string]any{
		"service_resource": service,
		"subject":          subject,
		"domain":           domain,
		"method":           "GET",
		"path":             path,
	})
	if err != nil {
		t.Fatalf("authorize %s %s %s %s: %v", app, subject, domain, path, err)
	}
	return decision.Allowed
}

func (f publicationFixture) loadPolicy(t *testing.T, service, subject, domain string) (biz.SubjectAPIPolicy, error) {
	t.Helper()
	return f.cache.LoadSubjectAPIs(context.Background(), biz.SubjectAPIIdentity{
		ServiceResource: service,
		Subject:         subject,
		Domain:          domain,
	})
}

func policyPaths(policy biz.SubjectAPIPolicy) []string {
	paths := make([]string, 0, len(policy.APIs))
	for _, endpoint := range policy.APIs {
		paths = append(paths, endpoint.PathTemplate)
	}
	sort.Strings(paths)
	return paths
}

func pathSet(policy biz.SubjectAPIPolicy) map[string]bool {
	paths := make(map[string]bool, len(policy.APIs))
	for _, endpoint := range policy.APIs {
		paths[endpoint.PathTemplate] = true
	}
	return paths
}

func assertNoApplicationMetadata(t *testing.T, policy biz.SubjectAPIPolicy) {
	t.Helper()
	if policy.ApplicationKey != "" || policy.ApplicationID != uuid.Nil {
		t.Fatalf("published policy retained application metadata: %+v", policy.SubjectAPIIdentity)
	}
}

func policyForMerge(service, subject, domain string, active bool, apis, catalog []biz.SubjectAPIEndpoint, roles ...string) biz.SubjectAPIPolicy {
	return biz.SubjectAPIPolicy{
		SubjectAPIIdentity: biz.SubjectAPIIdentity{
			ServiceResource: service,
			Subject:         subject,
			Domain:          domain,
			ServiceActive:   active,
		},
		APIs:      apis,
		Catalog:   catalog,
		RoleCodes: roles,
	}
}

func TestMergeServiceSubjectAPIPoliciesUnionsApplicationsAndDeduplicates(t *testing.T) {
	first := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: "/orders", Enabled: true}
	second := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: "/reports", Enabled: true}
	merged, err := mergeServiceSubjectAPIPolicies([]biz.SubjectAPIPolicy{
		policyForMerge("orders", "alice", "tenant", true, []biz.SubjectAPIEndpoint{first}, []biz.SubjectAPIEndpoint{first, second}, "reader"),
		policyForMerge("orders", "alice", "tenant", true, []biz.SubjectAPIEndpoint{second}, []biz.SubjectAPIEndpoint{first, second}, "reader"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 {
		t.Fatalf("merged policies = %+v", merged)
	}
	policy := merged[0]
	assertNoApplicationMetadata(t, policy)
	if got, want := policyPaths(policy), []string{"/orders", "/reports"}; !equalStrings(got, want) {
		t.Fatalf("union paths = %v, want %v", got, want)
	}
	if len(policy.RoleCodes) != 1 || policy.RoleCodes[0] != "reader" {
		t.Fatalf("role union = %v", policy.RoleCodes)
	}
}

func TestMergeServiceSubjectAPIPoliciesIgnoresDisabledApplicationGrants(t *testing.T) {
	allowed := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: "/orders", Enabled: true}
	disabled := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "POST", PathTemplate: "/orders", Enabled: true}
	merged, err := mergeServiceSubjectAPIPolicies([]biz.SubjectAPIPolicy{
		policyForMerge("orders", "alice", "", true, []biz.SubjectAPIEndpoint{allowed}, []biz.SubjectAPIEndpoint{allowed, disabled}, "reader"),
		policyForMerge("orders", "alice", "", false, []biz.SubjectAPIEndpoint{disabled}, []biz.SubjectAPIEndpoint{allowed, disabled}, "writer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 {
		t.Fatalf("merged policies = %+v", merged)
	}
	policy := merged[0]
	if !policy.ServiceActive {
		t.Fatalf("active contribution was lost: %+v", policy)
	}
	if got := pathSet(policy); len(got) != 1 || !got["/orders"] ||
		len(policy.APIs) != 1 || policy.APIs[0].Method != "GET" {
		t.Fatalf("disabled application contributed grants: %+v", policy.APIs)
	}
	if len(policy.Catalog) != 2 {
		t.Fatalf("catalog should remain complete: %+v", policy.Catalog)
	}
	if len(policy.RoleCodes) != 1 || policy.RoleCodes[0] != "reader" {
		t.Fatalf("disabled application contributed roles: %v", policy.RoleCodes)
	}
}

func TestMergeServiceSubjectAPIPoliciesDeduplicatesRolesAndIsolatesDomains(t *testing.T) {
	endpoint := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: "/orders", Enabled: true}
	merged, err := mergeServiceSubjectAPIPolicies([]biz.SubjectAPIPolicy{
		policyForMerge("orders", "alice", "", true, []biz.SubjectAPIEndpoint{endpoint}, []biz.SubjectAPIEndpoint{endpoint}, "reader"),
		policyForMerge("orders", "alice", " global ", true, nil, nil, "reader", "writer"),
		policyForMerge("orders", "alice", "tenant-a", true, []biz.SubjectAPIEndpoint{endpoint}, []biz.SubjectAPIEndpoint{endpoint}, "tenant-reader"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 {
		t.Fatalf("domain grouping = %+v", merged)
	}
	byDomain := make(map[string]biz.SubjectAPIPolicy, len(merged))
	for _, policy := range merged {
		byDomain[policy.Domain] = policy
	}
	if got := byDomain[""].RoleCodes; !equalStrings(got, []string{"reader", "writer"}) {
		t.Fatalf("default/global roles = %v", got)
	}
	if got := byDomain["tenant-a"].RoleCodes; !equalStrings(got, []string{"tenant-reader"}) {
		t.Fatalf("tenant roles = %v", got)
	}
}

func TestMergeServiceSubjectAPIPoliciesRejectsConflictingCatalogIdentity(t *testing.T) {
	id := uuid.New()
	_, err := mergeServiceSubjectAPIPolicies([]biz.SubjectAPIPolicy{
		policyForMerge("orders", "alice", "", true, nil, []biz.SubjectAPIEndpoint{{ID: id, Method: "GET", PathTemplate: "/orders", Enabled: true}}),
		policyForMerge("orders", "alice", "", true, nil, []biz.SubjectAPIEndpoint{{ID: id, Method: "GET", PathTemplate: "/other", Enabled: true}}),
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting service API catalog") {
		t.Fatalf("conflicting catalog identity = %v", err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Concurrent saves of one application must converge on the final database
// state. Every save republishes one global snapshot, so no application-scoped
// helper records are needed to fence a stale rebuild.
func TestSubjectAPIConcurrentSavesConverge(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	const subjects = 12
	var wg sync.WaitGroup
	errs := make(chan error, subjects)
	for i := 0; i < subjects; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			subject := fmt.Sprintf("user-%d", i)
			for round := 0; round < 3; round++ {
				if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, "", []uuid.UUID{f.role.ID}); err != nil {
					errs <- err
					return
				}
				if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, "", nil); err != nil {
					errs <- err
					return
				}
			}
			// Even subjects end bound, odd subjects end revoked.
			if i%2 == 0 {
				if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, "", []uuid.UUID{f.role.ID}); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i := 0; i < subjects; i++ {
		if got, want := f.allowed(t, fmt.Sprintf("user-%d", i)), i%2 == 0; got != want {
			t.Fatalf("user-%d allowed = %v, want %v (Redis diverged from the database)", i, got, want)
		}
	}
	keys := f.server.Keys()
	matchingServiceKeys := 0
	for _, key := range keys {
		if f.server.Type(key) != "string" {
			t.Fatalf("subject API key %q has Redis type %q", key, f.server.Type(key))
		}
		raw, err := f.server.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		var policy biz.SubjectAPIPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			t.Fatalf("decode business string %q: %v", key, err)
		}
		assertNoApplicationMetadata(t, policy)
		if policy.ServiceResource == f.service {
			matchingServiceKeys++
		}
	}
	if matchingServiceKeys != subjects/2 {
		t.Fatalf("business string count for service %q = %d, want %d; all keys: %v", f.service, matchingServiceKeys, subjects/2, keys)
	}
}

// A save whose publication fails reports PUBLISH_PENDING, and the reconciler
// repairs it, as well as a wiped Redis. Reconciliation has no readiness state.
func TestSubjectAPIReconcileRepairsFailedPublication(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, "alice", "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	if !f.allowed(t, "alice") {
		t.Fatal("alice not allowed after binding")
	}

	// Redis is down while a revoke commits.
	f.server.SetError("ERR outage")
	err := f.repo.ReplaceSubjectRoles(ctx, f.app, "alice", "", nil)
	if !errors.Is(err, biz.ErrPublishPending) {
		t.Fatalf("revoke during outage = %v, want ErrPublishPending", err)
	}
	f.server.SetError("")
	bindings, err := f.repo.ListSubjectRoleBindings(ctx, f.app)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("revoke not committed: %+v %v", bindings, err)
	}
	if !f.allowed(t, "alice") {
		t.Fatal("expected the stale grant before reconciling (test precondition)")
	}
	var applicationCount int
	if err := f.repo.pool.QueryRow(ctx, `SELECT count(*) FROM applications`).Scan(&applicationCount); err != nil {
		t.Fatal(err)
	}
	if got, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil || got != applicationCount {
		t.Fatalf("reconcile = %d, %v; want %d applications", got, err, applicationCount)
	}
	if f.allowed(t, "alice") {
		t.Fatal("reconcile did not publish the revoke")
	}

	// Even an apparently current database is recomputed in full.
	if got, err := f.repo.ReconcileSubjectAPIs(ctx, true); err != nil || got != applicationCount {
		t.Fatalf("full reconcile = %d, %v; want %d applications", got, err, applicationCount)
	}

	// Redis is wiped (restart without persistence, FLUSHALL).
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, "bob", "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	f.server.FlushAll()
	decision, err := f.rbac.Authorize(ctx, f.app, map[string]any{"service_resource": f.service, "subject": "bob", "method": "GET", "path": "/orders"})
	if err != nil || decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("authorize against wiped Redis = %+v, %v; want ordinary deny", decision, err)
	}
	if _, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !f.allowed(t, "bob") {
		t.Fatal("reconcile did not rebuild a wiped application")
	}
}

// A published application answers a subject without bindings with an ordinary
// deny, not an error.
func TestSubjectAPIUnboundSubjectIsDenied(t *testing.T) {
	f := newPublicationFixture(t)
	decision, err := f.rbac.Authorize(context.Background(), f.app, map[string]any{"service_resource": f.service, "subject": "nobody", "method": "GET", "path": "/orders"})
	if err != nil || decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("unbound subject = %+v, %v", decision, err)
	}
}

func TestSubjectAPIMultiApplicationUnionAndRevokeIntegration(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	subject := "shared-user"
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	second := f.addApplication(t, "", "/reports")
	if err := f.repo.ReplaceSubjectRoles(ctx, second.application.Key, subject, "", []uuid.UUID{second.role.ID}); err != nil {
		t.Fatal(err)
	}

	policy, err := f.loadPolicy(t, f.service, subject, "")
	if err != nil {
		t.Fatal(err)
	}
	assertNoApplicationMetadata(t, policy)
	if got := policyPaths(policy); !equalStrings(got, []string{"/orders", "/reports"}) {
		t.Fatalf("cross-application grant union = %v", got)
	}
	if !equalStrings(policy.RoleCodes, []string{"reader"}) {
		t.Fatalf("cross-application role union = %v", policy.RoleCodes)
	}
	if !f.allowedAt(t, f.app, f.service, subject, "", "/reports") {
		t.Fatal("second application did not contribute its API grant")
	}

	second.application.Enabled = false
	if _, err := f.repo.SaveApplication(ctx, second.application); err != nil {
		t.Fatal(err)
	}
	if !f.allowedAt(t, f.app, f.service, subject, "", "/orders") {
		t.Fatal("disabling one application removed another application's contribution")
	}
	if f.allowedAt(t, f.app, f.service, subject, "", "/reports") {
		t.Fatal("disabled application still contributed an API grant")
	}

	second.application.Enabled = true
	if _, err := f.repo.SaveApplication(ctx, second.application); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReplaceSubjectRoles(ctx, second.application.Key, subject, "", nil); err != nil {
		t.Fatal(err)
	}
	if !f.allowedAt(t, f.app, f.service, subject, "", "/orders") {
		t.Fatal("revoking one application removed another application's contribution")
	}
	if f.allowedAt(t, f.app, f.service, subject, "", "/reports") {
		t.Fatal("revoked application still contributed an API grant")
	}
}

func TestSubjectAPIRemovingServiceAssociationRemovesBusinessRecordIntegration(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	subject := "service-removal-user"
	service := "removed-service-" + uuid.NewString()
	second := f.addApplication(t, service, "/reports")
	if err := f.repo.ReplaceSubjectRoles(ctx, second.application.Key, subject, "", []uuid.UUID{second.role.ID}); err != nil {
		t.Fatal(err)
	}
	if policy, err := f.loadPolicy(t, service, subject, ""); err != nil || len(policy.APIs) != 1 {
		t.Fatalf("initial service policy = %+v, %v", policy, err)
	}

	// Remove the grant first because the database keeps service links that are
	// still referenced by role grants. The binding remains, so only the link
	// removal can eliminate this application's contribution.
	if _, err := f.repo.pool.Exec(ctx, `DELETE FROM role_api_endpoint_bindings WHERE role_id=$1`, second.role.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReplaceApplicationServices(ctx, second.application.Key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.loadPolicy(t, service, subject, ""); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("removed service record = %v, want ErrSubjectAPIRecordNotFound", err)
	}
	if f.allowedAt(t, second.application.Key, service, subject, "", "/reports") {
		t.Fatal("removed service association still authorized its old grant")
	}
}

func TestSubjectAPIDomainIsolationAndRevokeIntegration(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	subject := "domain-user"
	firstDomain := "tenant-a"
	secondDomain := "tenant-b"
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, firstDomain, []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	second := f.addApplication(t, "", "/reports")
	if err := f.repo.ReplaceSubjectRoles(ctx, second.application.Key, subject, secondDomain, []uuid.UUID{second.role.ID}); err != nil {
		t.Fatal(err)
	}

	first, err := f.loadPolicy(t, f.service, subject, firstDomain)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.loadPolicy(t, f.service, subject, secondDomain)
	if err != nil {
		t.Fatal(err)
	}
	if got := policyPaths(first); !equalStrings(got, []string{"/orders"}) {
		t.Fatalf("%s grants = %v", firstDomain, got)
	}
	if got := policyPaths(other); !equalStrings(got, []string{"/reports"}) {
		t.Fatalf("%s grants = %v", secondDomain, got)
	}
	if f.allowedAt(t, f.app, f.service, subject, firstDomain, "/reports") ||
		f.allowedAt(t, f.app, f.service, subject, secondDomain, "/orders") {
		t.Fatal("named-domain grants leaked into another domain")
	}

	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, subject, firstDomain, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.loadPolicy(t, f.service, subject, firstDomain); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("revoked domain record = %v, want ErrSubjectAPIRecordNotFound", err)
	}
	if f.allowedAt(t, f.app, f.service, subject, firstDomain, "/orders") {
		t.Fatal("revoked domain grant still authorized")
	}
	if !f.allowedAt(t, f.app, f.service, subject, secondDomain, "/reports") {
		t.Fatal("revoking one domain removed another domain's contribution")
	}
}

func TestEndpointRouteShapeAndGeneratedKeysIntegration(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	// The fixture's GET /orders exists; a template that only differs in its
	// trailing slash matches the same requests.
	if _, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders/", Enabled: true}); !errors.Is(err, biz.ErrAlreadyExists) {
		t.Fatalf("same-shape endpoint = %v", err)
	}
	byID, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders/{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/orders/{orderId}", Enabled: true}); !errors.Is(err, biz.ErrAlreadyExists) || !strings.Contains(err.Error(), "/orders/{id}") {
		t.Fatalf("renamed parameter = %v", err)
	}
	// Renaming the parameter of the existing endpoint itself is allowed.
	byID.PathTemplate = "/orders/{orderId}"
	byID.EndpointKey = "orders.get"
	renamed, err := f.repo.SaveEndpoint(ctx, f.service, byID)
	if err != nil || renamed.PathTemplate != "/orders/{orderId}" || renamed.EndpointKey != "orders.get" {
		t.Fatalf("update of the same endpoint: %+v %v", renamed, err)
	}
	// Another method is a different route.
	if _, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "DELETE", PathTemplate: "/orders/{id}", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// Generated keys stay unique when paths flatten to the same text, and fit
	// the column for long paths.
	dashed, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/a-b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/a/b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if dashed.EndpointKey == nested.EndpointKey {
		t.Fatalf("generated keys collided: %s", dashed.EndpointKey)
	}
	long, err := f.repo.SaveEndpoint(ctx, f.service, biz.RBACEndpoint{Method: "GET", PathTemplate: "/" + strings.Repeat("segment/", 55) + "{id}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(long.EndpointKey)); n > biz.MaxEndpointKeyLength {
		t.Fatalf("generated key has %d characters", n)
	}
}

func TestUpdateRoleSubjectsIntegration(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	writer, err := f.repo.SaveRole(ctx, f.app, biz.RBACRole{Code: "writer", Name: "Writer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Two administrators assign different roles to the same user at once.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, roleID := range []uuid.UUID{f.role.ID, writer.ID} {
		wg.Add(1)
		go func(roleID uuid.UUID) {
			defer wg.Done()
			errs <- f.repo.UpdateRoleSubjects(ctx, f.app, roleID, "", []string{"carol", "dave"}, nil)
		}(roleID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	countRoles := func(subject string) int {
		bindings, err := f.repo.ListSubjectRoleBindings(ctx, f.app)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, binding := range bindings {
			if binding.Subject == subject {
				count++
			}
		}
		return count
	}
	if countRoles("carol") != 2 || countRoles("dave") != 2 {
		t.Fatal("concurrent role assignments overwrote each other")
	}
	if !f.allowed(t, "carol") {
		t.Fatal("carol not allowed after assignment")
	}

	// Revoking one role keeps the subject's other role.
	if err := f.repo.UpdateRoleSubjects(ctx, f.app, f.role.ID, "", nil, []string{"carol"}); err != nil {
		t.Fatal(err)
	}
	if countRoles("carol") != 1 || f.allowed(t, "carol") {
		t.Fatal("revoke removed the wrong binding or did not take effect")
	}

	// A disabled role cannot be assigned, but can still be revoked.
	writer.Enabled = false
	if _, err := f.repo.SaveRole(ctx, f.app, writer); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.UpdateRoleSubjects(ctx, f.app, writer.ID, "", []string{"erin"}, nil); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("assigning a disabled role = %v", err)
	}
	if err := f.repo.UpdateRoleSubjects(ctx, f.app, writer.ID, "", nil, []string{"carol"}); err != nil {
		t.Fatal(err)
	}
	if countRoles("carol") != 0 {
		t.Fatal("revoking a disabled role failed")
	}
	if err := f.repo.UpdateRoleSubjects(ctx, f.app, uuid.New(), "", []string{"erin"}, nil); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("unknown role = %v", err)
	}
}
