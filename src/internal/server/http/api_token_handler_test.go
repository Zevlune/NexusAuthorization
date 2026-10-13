package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/config"
)

type httpTokenRepository struct {
	token     biz.APIToken
	endpoints []biz.RBACEndpoint
	err       error
}

func (r *httpTokenRepository) List(_ context.Context, f biz.APITokenFilter) (biz.RBACPage[biz.APIToken], error) {
	return biz.RBACPage[biz.APIToken]{Items: []biz.APIToken{r.token}, Total: 1, Page: f.Page, PageSize: f.PageSize}, r.err
}
func (r *httpTokenRepository) Get(_ context.Context, app string, id uuid.UUID) (biz.APIToken, error) {
	if app != r.token.AppKey || id != r.token.ID {
		return biz.APIToken{}, biz.ErrNotFound
	}
	return r.token, r.err
}
func (r *httpTokenRepository) Find(_ context.Context, id uuid.UUID) (biz.APIToken, error) {
	if id != r.token.ID {
		return biz.APIToken{}, biz.ErrNotFound
	}
	return r.token, r.err
}
func (r *httpTokenRepository) Create(_ context.Context, v biz.APIToken) (biz.APIToken, error) {
	if r.err != nil {
		return biz.APIToken{}, r.err
	}
	r.token = v
	return v, nil
}
func (r *httpTokenRepository) Update(ctx context.Context, v biz.APIToken) (biz.APIToken, error) {
	return r.Create(ctx, v)
}
func (r *httpTokenRepository) SetStatus(ctx context.Context, app string, id uuid.UUID, status string) (biz.APIToken, error) {
	v, err := r.Get(ctx, app, id)
	if err != nil {
		return v, err
	}
	if v.Status == "revoked" && status != "revoked" {
		return v, biz.ErrConflict
	}
	v.Status = status
	return r.Create(ctx, v)
}
func (r *httpTokenRepository) Rotate(ctx context.Context, app string, id uuid.UUID, v biz.APIToken, revoke bool) (biz.APIToken, error) {
	return r.Create(ctx, v)
}
func (r *httpTokenRepository) Endpoints(_ context.Context, app, service string) ([]biz.RBACEndpoint, error) {
	if r.err != nil {
		return nil, r.err
	}
	if app != "console" {
		return nil, biz.ErrNotFound
	}
	out := []biz.RBACEndpoint{}
	for _, v := range r.endpoints {
		if v.ServiceResource == service {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *httpTokenRepository) RecordUse(context.Context, uuid.UUID) error { return nil }

const callbackTestKey = "independent-internal-key-32-bytes-minimum"

// httpServiceResourceCatalog is a read-only directory stub used to drive owner
// validation without a live NexusAuth catalog. Only enabled resources may own a
// management-created token.
type httpServiceResourceCatalog struct {
	resources map[string]*biz.ServiceResource
	err       error
}

func (c *httpServiceResourceCatalog) List(context.Context) ([]*biz.ServiceResource, error) {
	if c.err != nil {
		return nil, c.err
	}
	items := make([]*biz.ServiceResource, 0, len(c.resources))
	for _, value := range c.resources {
		items = append(items, value)
	}
	return items, nil
}

func (c *httpServiceResourceCatalog) Get(_ context.Context, key string) (*biz.ServiceResource, error) {
	if c.err != nil {
		return nil, c.err
	}
	value, ok := c.resources[key]
	if !ok {
		return nil, biz.ErrNotFound
	}
	return value, nil
}

func newHTTPServiceResourceCatalog() *httpServiceResourceCatalog {
	return &httpServiceResourceCatalog{resources: map[string]*biz.ServiceResource{
		"sync":    {Key: "sync", DisplayName: "Sync", IsActive: true},
		"billing": {Key: "billing", DisplayName: "Billing", IsActive: true},
		"legacy":  {Key: "legacy", DisplayName: "Legacy", IsActive: false},
	}}
}

func tokenHTTPFixture(t *testing.T) (*Handler, *httpTokenRepository, string) {
	t.Helper()
	r := &httpTokenRepository{endpoints: []biz.RBACEndpoint{{ID: uuid.New(), ServiceResource: "nexusauth", Method: "GET", PathTemplate: "/scim/v2/Users", Enabled: true}}}
	s := biz.NewAPITokenService(r)
	c, err := s.Create(context.Background(), "console", biz.APITokenInput{Name: "SCIM", OwnerType: "service", OwnerID: "sync", Permissions: []biz.APITokenPermission{{EndpointID: r.endpoints[0].ID, ServiceResource: "nexusauth", Action: "read", TenantID: "tenant-a"}}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil).
		WithAPITokens(s, config.APITokenConfig{ServiceResource: "nexusauthorization", AdminSubjects: []string{"admin"}, InternalCallers: []config.APITokenInternalCaller{{ServiceResource: "nexusauth", Key: callbackTestKey}}}, true).
		WithServiceResourceCatalog(newHTTPServiceResourceCatalog())
	return h, r, c.RawToken
}
func tokenInternalBody(raw, service, tenant string) string {
	b, _ := json.Marshal(map[string]any{"token": raw, "app_key": "console", "service_resource": service, "method": "GET", "path": "/scim/v2/Users", "tenant_id": tenant})
	return string(b)
}
func runTokenRequest(server http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func TestInternalAPITokenAuthorization(t *testing.T) {
	h, r, raw := tokenHTTPFixture(t)
	server := NewServer(h)
	for _, tc := range []struct {
		name, key, body string
		status          int
		active, allowed bool
	}{
		{"allowed", callbackTestKey, tokenInternalBody(raw, "nexusauth", "tenant-a"), 200, true, true},
		{"no internal key", "", tokenInternalBody(raw, "nexusauth", "tenant-a"), 401, false, false},
		{"external token cannot authenticate callback", raw, tokenInternalBody(raw, "nexusauth", "tenant-a"), 401, false, false},
		{"cross service", callbackTestKey, tokenInternalBody(raw, "other", "tenant-a"), 403, false, false},
		{"wrong scope", callbackTestKey, tokenInternalBody(raw, "nexusauth", "tenant-b"), 200, true, false},
		{"invalid external token", callbackTestKey, tokenInternalBody("nxa_invalid", "nexusauth", "tenant-a"), 200, false, false},
		{"malformed", callbackTestKey, "{", 400, false, false},
		{"trailing JSON", callbackTestKey, tokenInternalBody(raw, "nexusauth", "tenant-a") + "{}", 400, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := runTokenRequest(server, "POST", "/internal/v1/api-tokens/authorize", tc.body, tc.key)
			if w.Code != tc.status {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("validation response cacheable")
			}
			if strings.Contains(w.Body.String(), raw) || strings.Contains(w.Body.String(), callbackTestKey) {
				t.Fatal("credential leaked")
			}
			if tc.status == 200 {
				var d biz.APITokenDecision
				if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.Active != tc.active || d.Allowed != tc.allowed {
					t.Fatalf("wrong decision %s", w.Body.String())
				}
			}
		})
	}
	r.err = errors.New("DB parameters contain " + raw)
	w := runTokenRequest(server, "POST", "/internal/v1/api-tokens/authorize", tokenInternalBody(raw, "nexusauth", "tenant-a"), callbackTestKey)
	if w.Code != 503 || strings.Contains(w.Body.String(), raw) {
		t.Fatalf("failure leaked or did not fail closed: %d %s", w.Code, w.Body.String())
	}
}

func TestAPITokenManagementRequiresAdminAndNeverReturnsHash(t *testing.T) {
	h, r, raw := tokenHTTPFixture(t)
	identity := func(subject string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), auth.User{Subject: subject})))
			})
		}
	}
	for _, tc := range []struct {
		subject string
		status  int
	}{{"viewer", 403}, {"admin", 200}} {
		w := runTokenRequest(NewServer(h, identity(tc.subject)), "GET", "/v1/api-tokens?app_key=console", "", "")
		if w.Code != tc.status {
			t.Fatalf("admin boundary: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), raw) || strings.Contains(w.Body.String(), r.token.TokenHash) {
			t.Fatal("summary leaked secret")
		}
	}
	server := NewServer(h, identity("admin"))
	req := httptest.NewRequest("GET", "/v1/api-tokens?app_key=console", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("API token could manage tokens")
	}
	w = runTokenRequest(server, "GET", "/v1/api-tokens", "", "")
	if w.Code != 400 {
		t.Fatal("missing application accepted")
	}
	w = runTokenRequest(server, "GET", "/v1/api-tokens/"+r.token.ID.String()+"?app_key=other", "", "")
	if w.Code != 404 {
		t.Fatal("cross app detail accepted")
	}
}

func TestLocalAPITokenMiddlewareScopesAppAndDoesNotFallback(t *testing.T) {
	h, r, _ := tokenHTTPFixture(t)
	r.endpoints = []biz.RBACEndpoint{{ID: uuid.New(), ServiceResource: "nexusauthorization", Method: "GET", PathTemplate: "/v1/rbac-v1/roles", Enabled: true}}
	in := biz.APITokenInput{Name: "Local reader", OwnerType: "user", OwnerID: "u", Permissions: []biz.APITokenPermission{{EndpointID: r.endpoints[0].ID, ServiceResource: "nexusauthorization", Action: "read"}}}
	c, err := h.apiTokens.Create(context.Background(), "console", in)
	if err != nil {
		t.Fatal(err)
	}
	identityCalls := 0
	identity := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { identityCalls++; next.ServeHTTP(w, req) })
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := apiTokenFromRequest(req); !ok {
			t.Fatal("missing token identity")
		}
		w.WriteHeader(204)
	})
	server := h.tokenOrIdentity(identity, next)
	for _, tc := range []struct {
		path, token string
		status      int
	}{{"/v1/rbac-v1/roles?app_key=console", c.RawToken, 204}, {"/v1/rbac-v1/roles?app_key=other", c.RawToken, 403}, {"/v1/service-resources?app_key=console", c.RawToken, 403}, {"/v1/api-tokens?app_key=console", c.RawToken, 403}, {"/v1/rbac-v1/roles?app_key=console", "nxa_wrong", 401}} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if identityCalls != 0 {
		t.Fatal("failed API token fell back to browser/dev authentication")
	}
	// Even an explicit grant cannot make an application token mutate the
	// shared endpoint catalog used by other applications.
	r.endpoints = []biz.RBACEndpoint{{ID: uuid.New(), ServiceResource: "nexusauthorization", Method: "POST", PathTemplate: "/v1/rbac-v1/endpoints", Enabled: true}}
	in.Permissions = []biz.APITokenPermission{{EndpointID: r.endpoints[0].ID, ServiceResource: "nexusauthorization", Action: "write"}}
	writer, err := h.apiTokens.Create(context.Background(), "console", in)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/rbac-v1/endpoints?app_key=console", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+writer.RawToken)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("shared catalog write accepted: %d %s", w.Code, w.Body.String())
	}
}

func apiTokenAdminIdentity(subject string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), auth.User{Subject: subject})))
		})
	}
}

func apiTokenCreateBody(endpointID uuid.UUID, ownerType, ownerID string) string {
	in := biz.APITokenInput{
		Name:      "directory owner token",
		OwnerType: ownerType,
		OwnerID:   ownerID,
		Permissions: []biz.APITokenPermission{{
			EndpointID: endpointID, ServiceResource: "nexusauth", Action: "read",
		}},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// Management creation requires no separate caller service selection.
// Compatibility owner fields cannot override the application identity.
func TestCreateAPITokenUsesApplicationIdentity(t *testing.T) {
	h, r, _ := tokenHTTPFixture(t)
	server := NewServer(h, apiTokenAdminIdentity("admin"))
	endpoint := r.endpoints[0].ID
	for _, tc := range []struct {
		name, ownerType, ownerID string
		status                   int
	}{
		{"explicit user owner rejected", "user", "sync", 400},
		{"legacy owner field ignored", "service", "ghost", 201},
		{"inactive legacy owner field ignored", "service", "legacy", 201},
		{"owner selection not required", "service", "", 201},
		{"service owner accepted", "service", "sync", 201},
		{"owner independent of target service", "service", "billing", 201},
		{"no ownership fields required", "", "", 201},
		{"invalid type rejected", "unknown", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := apiTokenCreateBody(endpoint, tc.ownerType, tc.ownerID)
			w := runTokenRequest(server, "POST", "/v1/api-tokens?app_key=console", body, "")
			if w.Code != tc.status {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if tc.status != 201 {
				return
			}
			var envelope struct {
				Result biz.APITokenCreated `json:"result"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v (%s)", err, w.Body.String())
			}
			if envelope.Result.Token.OwnerType != "service" || envelope.Result.Token.OwnerID != "console" {
				t.Fatalf("owner not derived from application: %+v", envelope.Result.Token)
			}
			if envelope.Result.RawToken == "" {
				t.Fatal("created token missing credential")
			}
		})
	}
}

// TestLegacyUserAPITokenStillReadableAndUpdatable pins the compatibility
// guarantee: the service-owner gate applies to creation only, so a token stored
// before the change with a user owner can still be read and updated.
func TestLegacyUserAPITokenStillReadableAndUpdatable(t *testing.T) {
	h, r, _ := tokenHTTPFixture(t)
	legacy := biz.APIToken{
		ID:        uuid.New(),
		AppKey:    "console",
		Name:      "legacy user token",
		OwnerType: "user",
		OwnerID:   "u-1",
		Status:    "active",
		Permissions: []biz.APITokenPermission{{
			EndpointID: r.endpoints[0].ID, ServiceResource: "nexusauth", Action: "read",
			Method: "GET", PathTemplate: "/scim/v2/Users",
		}},
	}
	r.token = legacy
	server := NewServer(h, apiTokenAdminIdentity("admin"))

	read := runTokenRequest(server, "GET", "/v1/api-tokens/"+legacy.ID.String()+"?app_key=console", "", "")
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"owner_type":"user"`) {
		t.Fatalf("legacy user token no longer readable: %d %s", read.Code, read.Body.String())
	}

	body := apiTokenCreateBody(r.endpoints[0].ID, "user", "u-1")
	updated := runTokenRequest(server, "PUT", "/v1/api-tokens/"+legacy.ID.String()+"?app_key=console", body, "")
	if updated.Code != 200 {
		t.Fatalf("legacy user token no longer updatable: %d %s", updated.Code, updated.Body.String())
	}
}
