package rbac

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizationPathKeepsEncodedSlashInsideSegment(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"/api/books", "/api/books"},
		{"/api/orders/1%2Fitems", "/api/orders/1%2Fitems"},
		{"/api/orders/1%2fitems", "/api/orders/1%2Fitems"},
		{"/api/a%20b", "/api/a b"},
		{"/api/caf%C3%A9", "/api/café"},
		{"/api/100%25", "/api/100%25"},
		{"/api/books/", "/api/books/"},
	} {
		parsed, err := url.ParseRequestURI(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := authorizationPath(parsed); got != tc.want {
			t.Fatalf("authorizationPath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if got := authorizationPath(&url.URL{Path: "/a", RawPath: "/a%zz"}); got != "/a" {
		// An invalid RawPath is ignored by EscapedPath, which falls back to Path.
		t.Fatalf("invalid raw path = %q", got)
	}
}

// The authorization service grants only /api/orders/1/items. The encoded
// request is dispatched by ServeMux to /api/orders/{id}, so it must be denied.
func TestMiddlewareChecksTheRouteServeMuxDispatches(t *testing.T) {
	var checked []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload Request
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		checked = append(checked, payload.Path)
		body := `{"allowed":false,"effect":"deny","reason":"endpoint_not_granted"}`
		if payload.Path == "/api/orders/1/items" {
			body = `{"allowed":true,"effect":"allow","reason":"allowed"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	client := NewClient("http://nexus-authorization.test", "secret")
	client.HTTP = &http.Client{Transport: transport}
	var routed string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orders/{id}", func(w http.ResponseWriter, r *http.Request) { routed = "order:" + r.PathValue("id") })
	mux.HandleFunc("GET /api/orders/{id}/items", func(w http.ResponseWriter, r *http.Request) { routed = "items:" + r.PathValue("id") })
	handler := client.Require("shop", "orders", func(*http.Request) (Identity, bool) { return Identity{Subject: "user:1"}, true }, mux)

	for _, tc := range []struct {
		path, routed string
		status       int
	}{
		{"/api/orders/1/items", "items:1", http.StatusOK},
		{"/api/orders/1%2Fitems", "", http.StatusForbidden},
	} {
		routed = ""
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || routed != tc.routed {
			t.Fatalf("%s: status = %d routed = %q, want %d %q (checked %v)", tc.path, w.Code, routed, tc.status, tc.routed, checked)
		}
	}
}
