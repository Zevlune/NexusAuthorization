package biz

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// endpointRecordingRepository records endpoint and role-subject writes.
type endpointRecordingRepository struct {
	rbacV1FakeRepository
	existing     []RBACEndpoint
	singleSaves  int
	batches      int
	batchSaved   []RBACEndpoint
	roleSubjects []string
}

func (r *endpointRecordingRepository) ListEndpoints(context.Context, string) ([]RBACEndpoint, error) {
	return r.existing, nil
}

func (r *endpointRecordingRepository) SaveEndpoint(_ context.Context, _ string, value RBACEndpoint) (RBACEndpoint, error) {
	r.singleSaves++
	return value, nil
}

func (r *endpointRecordingRepository) SaveEndpoints(_ context.Context, _ string, values []RBACEndpoint) ([]RBACEndpoint, error) {
	r.batches++
	r.batchSaved = append(r.batchSaved, values...)
	return values, nil
}

func (r *endpointRecordingRepository) UpdateRoleSubjects(_ context.Context, app string, roleID uuid.UUID, domain string, add, remove []string) error {
	r.roleSubjects = append(r.roleSubjects, app+"|"+roleID.String()+"|"+domain+"|+"+strings.Join(add, ",")+"|-"+strings.Join(remove, ","))
	return nil
}

func TestRouteShapeIgnoresParameterNamesAndSlashes(t *testing.T) {
	for _, pair := range [][2]string{
		{"/orders/{id}", "/orders/{orderId}/"},
		{" /a/b ", "/a/b/"},
		{"/x/{a}/y/{b}", "/x/{c}/y/{d}"},
	} {
		if RouteShape(pair[0]) != RouteShape(pair[1]) {
			t.Fatalf("%q and %q match the same requests but have different shapes", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"/orders/{id}", "/orders/id"},
		{"/orders/{id}", "/orders/{id}/items"},
		{"/a/{}", "/a/{id}"}, // "{}" is matched literally, not as a parameter
	} {
		if RouteShape(pair[0]) == RouteShape(pair[1]) {
			t.Fatalf("%q and %q must have different shapes", pair[0], pair[1])
		}
	}
}

func TestSaveEndpointRejectsUnsupportedInput(t *testing.T) {
	repository := &endpointRecordingRepository{}
	service := NewRBACV1Service(repository)
	ctx := context.Background()
	for name, endpoint := range map[string]RBACEndpoint{
		"unknown method":    {Method: "FETCH", PathTemplate: "/orders"},
		"relative path":     {Method: "GET", PathTemplate: "orders"},
		"path too long":     {Method: "GET", PathTemplate: "/" + strings.Repeat("a", maxEndpointPathLength)},
		"key too long":      {Method: "GET", PathTemplate: "/orders", EndpointKey: strings.Repeat("k", MaxEndpointKeyLength+1)},
		"missing everthing": {},
	} {
		if _, err := service.SaveEndpoint(ctx, "orders", endpoint); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if repository.singleSaves != 0 {
		t.Fatalf("invalid endpoints reached the repository %d times", repository.singleSaves)
	}
	if _, err := service.SaveEndpoint(ctx, "orders", RBACEndpoint{Method: "get", PathTemplate: "/orders"}); err != nil || repository.singleSaves != 1 {
		t.Fatalf("valid endpoint: saves=%d err=%v", repository.singleSaves, err)
	}
}

func swaggerTestServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.0","info":{"title":"Orders"},"paths":{` +
			`"/orders/{orderId}":{"get":{"summary":"get order"}},` +
			`"/orders":{"get":{"summary":"list orders"}},` +
			`"/items/{id}":{"get":{"summary":"get item"}}}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestImportSwaggerSkipsSameRouteShapesAndPublishesOnce(t *testing.T) {
	var hits int32
	server := swaggerTestServer(t, &hits)
	repository := &endpointRecordingRepository{existing: []RBACEndpoint{{Method: "GET", PathTemplate: "/orders/{id}"}}}
	service := NewRBACV1Service(repository).WithSwaggerFetchPolicy(SwaggerFetchPolicy{AllowLoopback: true})

	result, err := service.ImportSwagger(context.Background(), "orders", server.URL+"/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	// GET /orders/{orderId} matches the existing GET /orders/{id}: skipped,
	// instead of creating an ambiguous duplicate.
	if result.Total != 3 || result.Skipped != 1 || result.Created != 2 {
		t.Fatalf("result = %+v", result)
	}
	if repository.batches != 1 || repository.singleSaves != 0 || len(repository.batchSaved) != 2 {
		t.Fatalf("import must save in one batch and publish once: batches=%d single=%d saved=%d", repository.batches, repository.singleSaves, len(repository.batchSaved))
	}
}

func TestImportSwaggerEnforcesFetchPolicy(t *testing.T) {
	var hits int32
	server := swaggerTestServer(t, &hits)
	repository := &endpointRecordingRepository{}
	ctx := context.Background()
	for name, policy := range map[string]SwaggerFetchPolicy{
		"loopback by default":        {},
		"outside the allowlist":      {AllowLoopback: true, AllowedHosts: []string{"10.0.0.0/8", "docs.example.com"}},
		"allowlist without loopback": {AllowedHosts: []string{"127.0.0.1/32"}},
	} {
		_, err := NewRBACV1Service(repository).WithSwaggerFetchPolicy(policy).ImportSwagger(ctx, "orders", server.URL+"/openapi.json")
		if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "may not access") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if hits != 0 {
		t.Fatalf("blocked imports still reached the server %d times", hits)
	}
	if _, err := NewRBACV1Service(repository).WithSwaggerFetchPolicy(SwaggerFetchPolicy{AllowLoopback: true, AllowedHosts: []string{"127.0.0.1/32"}}).ImportSwagger(ctx, "orders", server.URL+"/openapi.json"); err != nil {
		t.Fatalf("allowlisted import failed: %v", err)
	}
}

func TestFetchSwaggerDocumentPreservesResponseErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		wantErr error
		message string
	}{
		{name: "HTTP error", status: http.StatusServiceUnavailable, message: "received HTTP 503"},
		{name: "invalid Swagger UI", status: http.StatusOK, body: "<html></html>", wantErr: ErrInvalidArgument, message: "does not declare a document URL"},
		{name: "blocked Swagger UI reference", status: http.StatusOK, body: `<html><script>url: "http://169.254.169.254/openapi.json"</script></html>`, wantErr: ErrInvalidArgument, message: "may not access"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var hits int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			_, err := fetchSwaggerDocument(context.Background(), server.URL+"/openapi.json", SwaggerFetchPolicy{AllowLoopback: true})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("response error was replaced: %v, want %q", err, test.message)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("err = %v, want %v", err, test.wantErr)
			}
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("server received %d requests, want 1", got)
			}
		})
	}
}

func TestSwaggerFetchPolicyCheckIP(t *testing.T) {
	strict := SwaggerFetchPolicy{}
	for _, address := range []string{
		"169.254.169.254", "::ffff:169.254.169.254", "fe80::1", // link-local and metadata
		"100.100.100.200", "fd00:ec2::254", // cloud metadata outside link-local
		"0.0.0.0", "0.1.2.3", "::", "224.0.0.1", "ff02::1",
		"127.0.0.1", "::1",
	} {
		if err := strict.checkIP(net.ParseIP(address), false); err == nil {
			t.Fatalf("%s must be blocked", address)
		}
	}
	for _, address := range []string{"10.1.2.3", "192.168.1.10", "172.16.0.5", "8.8.8.8", "2001:db8::1"} {
		if err := strict.checkIP(net.ParseIP(address), false); err != nil {
			t.Fatalf("%s must be allowed: %v", address, err)
		}
	}
	loopback := SwaggerFetchPolicy{AllowLoopback: true}
	if err := loopback.checkIP(net.ParseIP("127.0.0.1"), false); err != nil {
		t.Fatalf("loopback policy rejected 127.0.0.1: %v", err)
	}
	if err := loopback.checkIP(net.ParseIP("169.254.169.254"), false); err == nil {
		t.Fatal("loopback policy must still block metadata addresses")
	}
	allowlist := SwaggerFetchPolicy{AllowedHosts: []string{"docs.internal", "10.20.0.0/16"}}
	if err := allowlist.checkIP(net.ParseIP("10.20.3.4"), false); err != nil {
		t.Fatalf("allowlisted range rejected: %v", err)
	}
	if err := allowlist.checkIP(net.ParseIP("10.30.3.4"), false); err == nil {
		t.Fatal("address outside the allowlist accepted")
	}
	if err := allowlist.checkIP(net.ParseIP("10.30.3.4"), allowlist.hostNameAllowed("DOCS.internal")); err != nil {
		t.Fatalf("allowlisted host name rejected: %v", err)
	}
	if err := allowlist.checkIP(net.ParseIP("169.254.169.254"), true); err == nil {
		t.Fatal("an allowlisted name must not unlock metadata addresses")
	}
}

func TestUpdateRoleSubjectsValidatesAndNormalizes(t *testing.T) {
	repository := &endpointRecordingRepository{}
	service := NewRBACV1Service(repository)
	ctx := context.Background()
	roleID := uuid.New()
	for name, call := range map[string]func() error{
		"missing role":  func() error { return service.UpdateRoleSubjects(ctx, "console", uuid.Nil, "", []string{"a"}, nil) },
		"blank subject": func() error { return service.UpdateRoleSubjects(ctx, "console", roleID, "", []string{" "}, nil) },
		"added and removed": func() error {
			return service.UpdateRoleSubjects(ctx, "console", roleID, "", []string{"a"}, []string{"a "})
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if err := service.UpdateRoleSubjects(ctx, "console", roleID, "", nil, nil); err != nil || len(repository.roleSubjects) != 0 {
		t.Fatalf("empty change reached the repository: %v %v", repository.roleSubjects, err)
	}
	if err := service.UpdateRoleSubjects(ctx, " console ", roleID, "global", []string{" a", "a", "b"}, []string{"c"}); err != nil {
		t.Fatal(err)
	}
	want := "console|" + roleID.String() + "||+a,b|-c"
	if len(repository.roleSubjects) != 1 || repository.roleSubjects[0] != want {
		t.Fatalf("repository call = %v, want %s", repository.roleSubjects, want)
	}
}

func TestSubjectAPIAuthorizeMissingRecordIsOrdinaryDeny(t *testing.T) {
	input := subjectAPITestInput("user-without-roles", "", "orders", "/books")
	fixture := newSubjectAPIRepositoryFixture()
	fixture.setError(ErrSubjectAPIRecordNotFound)
	decision, err := newSubjectAPITestService(fixture, 16).Authorize(context.Background(), "console", input)
	if err != nil || decision.Allowed || decision.Effect != "deny" || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("missing record must be a deny decision: %+v, %v", decision, err)
	}
	// An application that was never published is still an error (404).
	fixture.setError(ErrNotFound)
	if _, err := newSubjectAPITestService(fixture, 16).Authorize(context.Background(), "unknown", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpublished application = %v", err)
	}
}
