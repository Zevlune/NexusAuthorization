package rbac

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestMiddlewareFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		authenticated bool
		body          string
		status        int
	}{
		{"unauthenticated", false, `{"allowed":true}`, 401},
		{"denied", true, `{"allowed":false,"reason":"no_binding"}`, 403},
		{"malformed", true, `broken`, 403},
		{"invalid_allow", true, `{"allowed":true}`, 403},
		{"unpublished_allow", true, `{"allowed":true,"effect":"allow","reason":"allowed","release_version":0}`, 403},
		{"invalid_effect", true, `{"allowed":true,"effect":"deny","reason":"allowed","release_version":1}`, 403},
		{"allowed", true, `{"allowed":true,"effect":"allow","reason":"allowed","release_version":1}`, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing credential")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			client := NewClient("http://nexus-authorization.test", "secret")
			client.HTTP = &http.Client{Transport: transport}
			middleware := client.Require("books", "book-service", func(*http.Request) (Identity, bool) { return Identity{"user:1", "global"}, tc.authenticated }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			w := httptest.NewRecorder()
			middleware.ServeHTTP(w, httptest.NewRequest("GET", "/api/books", nil))
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
		})
	}
}

func TestCheckRequiresApplicationAndService(t *testing.T) {
	client := NewClient("http://nexus-authorization.test", "secret")
	for _, tc := range []struct {
		name    string
		request Request
	}{
		{name: "missing app key", request: Request{ServiceResource: "book-service", Subject: "user:1", Domain: "global", Method: "GET", Path: "/api/books"}},
		{name: "missing service resource", request: Request{AppKey: "books", Subject: "user:1", Domain: "global", Method: "GET", Path: "/api/books"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Check(t.Context(), tc.request); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCheckSendsApplicationAndServiceContext(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload Request
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		want := Request{AppKey: "books", ServiceResource: "book-service", Subject: "user:1", Domain: "global", Method: "GET", Path: "/api/books"}
		if payload != want {
			t.Fatalf("payload = %+v, want %+v", payload, want)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"allowed":false,"reason":"no_binding"}`)), Header: make(http.Header)}, nil
	})
	client := NewClient("http://nexus-authorization.test", "secret")
	client.HTTP = &http.Client{Transport: transport}
	if _, err := client.Check(t.Context(), Request{AppKey: "books", ServiceResource: "book-service", Subject: "user:1", Domain: "global", Method: "GET", Path: "/api/books"}); err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
}

func TestMiddlewareRequiresTrustedApplicationAndServiceConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		appKey   string
		service  string
		expected int
	}{
		{name: "missing app key", service: "book-service", expected: http.StatusUnauthorized},
		{name: "missing service resource", appKey: "books", expected: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			client := NewClient("http://nexus-authorization.test", "secret")
			middleware := client.Require(tc.appKey, tc.service, func(*http.Request) (Identity, bool) {
				return Identity{"user:1", "global"}, true
			}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			w := httptest.NewRecorder()
			middleware.ServeHTTP(w, httptest.NewRequest("GET", "/api/books", nil))
			if w.Code != tc.expected {
				t.Fatalf("got %d want %d", w.Code, tc.expected)
			}
			if called {
				t.Fatal("next handler was called")
			}
		})
	}
}
