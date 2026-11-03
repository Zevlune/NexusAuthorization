package biz

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// databaseAPIRequestPolicyFixture exposes both the request-scoped database
// interface and the legacy full-policy/Redis interfaces. The read counters
// prove that database mode chooses the request-scoped authority only.
type databaseAPIRequestPolicyFixture struct {
	subjectAPIRepositoryFixture

	mu             sync.Mutex
	active         CurrentPolicy
	err            error
	apiReads       int
	legacyReads    int
	redisReads     int
	requestedApp   string
	requestedUser  string
	requestedSite  string
	requestedRoute string
}

func (r *databaseAPIRequestPolicyFixture) GetAPIRequestPolicy(_ context.Context, app, subject, domain, service string) (CurrentPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apiReads++
	r.requestedApp = app
	r.requestedUser = subject
	r.requestedSite = domain
	r.requestedRoute = service
	return r.active, r.err
}

func (r *databaseAPIRequestPolicyFixture) GetPolicyMetadata(context.Context, string) (CurrentPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.legacyReads++
	return r.active, nil
}

func (r *databaseAPIRequestPolicyFixture) GetCurrentPolicy(context.Context, CurrentPolicy) (CurrentPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.legacyReads++
	return r.active, nil
}

func (r *databaseAPIRequestPolicyFixture) GetSubjectAPIs(context.Context, string, string, string, string) (SubjectAPIPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redisReads++
	return SubjectAPIPolicy{}, context.Canceled
}

func TestDatabaseAuthorizationUsesRequestScopedPolicyEveryTime(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	fixture := &databaseAPIRequestPolicyFixture{active: CurrentPolicy{
		ApplicationID: appID,
		Token:         "allowed",
		Data:          directPolicyData(true, true),
	}}
	service := NewRBACV1Service(fixture).WithDatabaseAuthorization()
	input := map[string]any{
		"subject":          " user-1 ",
		"domain":           " tenant-a ",
		"service_resource": " order-service ",
		"method":           "GET",
		"path":             "/books",
	}

	decision, err := service.Authorize(context.Background(), " console ", input)
	if err != nil || !decision.Allowed || len(decision.MatchedRoles) != 1 || decision.MatchedRoles[0] != "reader" {
		t.Fatalf("request-scoped database allow = %+v, %v", decision, err)
	}

	fixture.mu.Lock()
	fixture.active = CurrentPolicy{
		ApplicationID: appID,
		Token:         "revoked",
		Data:          directPolicyData(true, false),
	}
	fixture.mu.Unlock()
	decision, err = service.Authorize(context.Background(), " console ", input)
	if err != nil || decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("request-scoped database revoke = %+v, %v", decision, err)
	}

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.apiReads != 2 || fixture.legacyReads != 0 || fixture.redisReads != 0 {
		t.Fatalf("source reads: API=%d legacy=%d Redis=%d", fixture.apiReads, fixture.legacyReads, fixture.redisReads)
	}
	if fixture.requestedApp != "console" || fixture.requestedUser != "user-1" || fixture.requestedSite != "tenant-a" || fixture.requestedRoute != "order-service" {
		t.Fatalf("request identity = %q/%q/%q/%q", fixture.requestedApp, fixture.requestedUser, fixture.requestedSite, fixture.requestedRoute)
	}
}

func TestDatabaseAuthorizationNormalizesDefaultDomainAlias(t *testing.T) {
	fixture := &databaseAPIRequestPolicyFixture{active: CurrentPolicy{
		ApplicationID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		Token:         "default-domain",
		Data:          defaultDomainPolicyData(""),
	}}
	service := NewRBACV1Service(fixture).WithDatabaseAuthorization()

	decision, err := service.Authorize(context.Background(), "books", map[string]any{
		"subject":          " user-1 ",
		"domain":           " global ",
		"service_resource": " order-service ",
		"method":           "GET",
		"path":             "/books",
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("default domain alias = %+v, %v", decision, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.requestedSite != "" || fixture.requestedUser != "user-1" || fixture.requestedRoute != "order-service" {
		t.Fatalf("normalized request identity = %q/%q/%q", fixture.requestedUser, fixture.requestedSite, fixture.requestedRoute)
	}
}
