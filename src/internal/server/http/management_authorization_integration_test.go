package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/db/repo"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

func TestManagementRoutesUseRedisPermissions(t *testing.T) {
	redis := miniredis.RunT(t)
	cache, err := rediscache.New(config.RedisConfig{URL: "redis://" + redis.Addr(), KeyPrefix: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	endpoint := biz.SubjectAPIEndpoint{ID: uuid.New(), Method: "GET", PathTemplate: "/api/v1/applications", Enabled: true}
	policy := biz.SubjectAPIPolicy{
		SubjectAPIIdentity: biz.SubjectAPIIdentity{Subject: "alice", ServiceResource: "management", ServiceActive: true},
		Catalog:            []biz.SubjectAPIEndpoint{endpoint}, APIs: []biz.SubjectAPIEndpoint{endpoint},
	}
	if err := cache.StoreSubjectAPIs(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	service := biz.NewRBACV1Service(repo.NewRBACV1Repository(nil).WithPolicyCache(cache))
	identity := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subject := r.Header.Get("X-Test-Subject"); subject != "" {
				r = r.WithContext(auth.WithUser(r.Context(), auth.User{Subject: subject}))
			}
			next.ServeHTTP(w, r)
		})
	}
	server := NewServer(NewHandler(nil), identity, ManagementAuthorizationMiddleware(service, "context", "management"))
	for _, tc := range []struct {
		subject, path string
		status        int
	}{
		{"", "/api/v1/applications", http.StatusUnauthorized},
		{"bob", "/api/v1/applications", http.StatusForbidden},
		{"alice", "/api/v1/roles", http.StatusForbidden},
		// The nil handler service returns 404 after authorization has allowed the request.
		{"alice", "/api/v1/applications", http.StatusNotFound},
		{"", "/api/healthz", http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("X-Test-Subject", tc.subject)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("subject=%q path=%s: got %d want %d", tc.subject, tc.path, w.Code, tc.status)
		}
	}
	otherCache, err := rediscache.New(config.RedisConfig{URL: "redis://" + redis.Addr(), KeyPrefix: "other-environment"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherCache.Close() })
	otherService := biz.NewRBACV1Service(repo.NewRBACV1Repository(nil).WithPolicyCache(otherCache))
	otherServer := NewServer(NewHandler(nil), identity, ManagementAuthorizationMiddleware(otherService, "context", "management"))
	otherRequest := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	otherRequest.Header.Set("X-Test-Subject", "alice")
	otherResponse := httptest.NewRecorder()
	otherServer.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusForbidden {
		t.Fatalf("different environment: got %d", otherResponse.Code)
	}
	redis.Close()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	r.Header.Set("X-Test-Subject", "alice")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("redis unavailable: got %d", w.Code)
	}
}
