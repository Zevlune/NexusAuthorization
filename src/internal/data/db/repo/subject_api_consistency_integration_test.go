package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

// publicationFixture is one application with a linked service, one granted
// endpoint and one role, published to an isolated miniredis instance.
type publicationFixture struct {
	repo     *RBACV1Repository
	rbac     *biz.RBACV1Service
	cache    *rediscache.PolicyCache
	server   *miniredis.Miniredis
	app      string
	service  string
	role     biz.RBACRole
	endpoint biz.RBACEndpoint
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
	cache, err := rediscache.New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization:test:" + uuid.NewString()})
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
	if _, err := f.repo.SaveApplication(ctx, biz.Application{Key: f.app, DisplayName: f.app, Enabled: true}); err != nil {
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

func (f publicationFixture) allowed(t *testing.T, subject string) bool {
	t.Helper()
	decision, err := f.rbac.Authorize(context.Background(), f.app, map[string]any{"service_resource": f.service, "subject": subject, "method": "GET", "path": "/orders"})
	if err != nil {
		t.Fatalf("authorize %s: %v", subject, err)
	}
	return decision.Allowed
}

// Concurrent saves of one application must converge on the final database
// state; before the rebuild lease, a slower rebuild could publish an older
// snapshot over a newer one and resurrect revoked grants.
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
	statuses, err := f.cache.SubjectAPIStatuses(ctx, []string{f.app})
	if err != nil || !statuses[f.app].Ready() {
		t.Fatalf("publication not ready after concurrent saves: %+v, %v", statuses[f.app], err)
	}
}

// A save whose publication fails reports PUBLISH_PENDING, and the reconciler
// repairs it, as well as a wiped Redis and a gate left in "updating".
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
	// The shared test database may hold other applications, so assert on this
	// application's state rather than on the number republished.
	if _, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if f.allowed(t, "alice") {
		t.Fatal("reconcile did not publish the revoke")
	}

	// Nothing is pending for this application: an incremental pass leaves it alone.
	before, _ := f.cache.SubjectAPIStatuses(ctx, []string{f.app})
	if _, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	after, _ := f.cache.SubjectAPIStatuses(ctx, []string{f.app})
	if after[f.app] != before[f.app] || !after[f.app].Ready() {
		t.Fatalf("idle reconcile republished a current application: %+v -> %+v", before[f.app], after[f.app])
	}

	// Redis is wiped (restart without persistence, FLUSHALL).
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, "bob", "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	f.server.FlushAll()
	if _, err := f.rbac.Authorize(ctx, f.app, map[string]any{"service_resource": f.service, "subject": "bob", "method": "GET", "path": "/orders"}); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("authorize against wiped Redis = %v", err)
	}
	if _, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !f.allowed(t, "bob") {
		t.Fatal("reconcile did not rebuild a wiped application")
	}

	// A rebuild died after closing the gate.
	statuses, _ := f.cache.SubjectAPIStatuses(ctx, []string{f.app})
	if !statuses[f.app].Ready() {
		t.Fatalf("not ready before the stuck-gate check: %+v", statuses[f.app])
	}
	for _, key := range f.server.Keys() {
		if strings.HasSuffix(key, ":state") {
			if err := f.server.Set(key, "updating"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.rbac.Authorize(ctx, f.app, map[string]any{"service_resource": f.service, "subject": "bob", "method": "GET", "path": "/orders"}); err == nil {
		t.Fatal("authorize served a gated application")
	}
	if _, err := f.repo.ReconcileSubjectAPIs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !f.allowed(t, "bob") {
		t.Fatal("reconcile did not reopen a stuck gate")
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
