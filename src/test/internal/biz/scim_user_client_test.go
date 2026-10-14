package biz

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testSCIMCredential is a placeholder used only inside tests. It is not a real key.
const testSCIMCredential = "test-scim-credential"

func TestNewNexusAuthSCIMClientValidatesBaseURLAndCredential(t *testing.T) {
	invalidBaseURLs := []string{
		"",
		"   ",
		"/scim/v2",
		"scim.example.test",
		"ftp://scim.example.test",
		"javascript:alert(1)",
		"https://",
		"https://user:secret@scim.example.test",
		"https://user@scim.example.test",
		"https://scim.example.test?tenant=1",
		"https://scim.example.test/#fragment",
	}
	for _, baseURL := range invalidBaseURLs {
		t.Run("baseurl_"+baseURL, func(t *testing.T) {
			if _, err := NewNexusAuthSCIMClient(baseURL, testSCIMCredential, time.Second); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("baseURL %q: expected ErrInvalidArgument, got %v", baseURL, err)
			}
		})
	}

	for _, credential := range []string{"", "   "} {
		t.Run("credential", func(t *testing.T) {
			if _, err := NewNexusAuthSCIMClient("https://scim.example.test", credential, time.Second); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("credential %q: expected ErrInvalidArgument, got %v", credential, err)
			}
		})
	}

	for _, baseURL := range []string{"http://scim.example.test", "https://scim.example.test/", "https://scim.example.test/base"} {
		t.Run("valid_"+baseURL, func(t *testing.T) {
			client, err := NewNexusAuthSCIMClient(baseURL, testSCIMCredential, 2*time.Second)
			if err != nil {
				t.Fatalf("baseURL %q: unexpected error %v", baseURL, err)
			}
			if client == nil {
				t.Fatalf("baseURL %q: expected non-nil client", baseURL)
			}
		})
	}

	t.Run("with_client", func(t *testing.T) {
		client, err := NewNexusAuthSCIMClientWithClient("https://scim.example.test", testSCIMCredential, &http.Client{})
		if err != nil || client == nil {
			t.Fatalf("injected client: err=%v client=%v", err, client)
		}
	})

	t.Run("nil_client_defaults", func(t *testing.T) {
		if _, err := NewNexusAuthSCIMClientWithClient("https://scim.example.test", testSCIMCredential, nil); err != nil {
			t.Fatalf("nil client: unexpected error %v", err)
		}
	})
}

func TestNewNexusAuthSCIMClientFallsBackToDefaultTimeout(t *testing.T) {
	// A non-positive timeout must not fail construction: the client applies a default.
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewNexusAuthSCIMClient("https://scim.example.test", testSCIMCredential, timeout); err != nil {
			t.Fatalf("timeout %s: unexpected error %v", timeout, err)
		}
	}
}

func TestListUsersSendsRequestAndDecodesResponse(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotQuery  url.Values
		gotAuth   string
		gotAccept string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/scim+json")
		_, _ = io.WriteString(w, `{
			"schemas": ["urn:ietf:params:scim:api:messages:2.0:ListResponse"],
			"totalResults": 2,
			"startIndex": 1,
			"itemsPerPage": 2,
			"resources": [
				{"id": "u1", "userName": "ada", "active": true, "name": {"formatted": "Ada Lovelace"}, "emails": [{"value": "ada@example.test", "primary": true}]},
				{"id": "u2", "userName": "grace", "active": false}
			]
		}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	list, err := client.ListUsers(context.Background(), `userName eq "ada"`, 1, 25)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/scim/v2/Users" {
		t.Errorf("path = %q, want /scim/v2/Users", gotPath)
	}
	if gotQuery.Get("filter") != `userName eq "ada"` {
		t.Errorf("filter = %q", gotQuery.Get("filter"))
	}
	if gotQuery.Get("startIndex") != "1" {
		t.Errorf("startIndex = %q, want 1", gotQuery.Get("startIndex"))
	}
	if gotQuery.Get("count") != "25" {
		t.Errorf("count = %q, want 25", gotQuery.Get("count"))
	}
	if gotAuth != "Bearer "+testSCIMCredential {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/scim+json" {
		t.Errorf("Accept = %q, want application/scim+json", gotAccept)
	}

	if list.TotalResults != 2 || list.StartIndex != 1 || list.ItemsPerPage != 2 {
		t.Errorf("list metadata = %#v", list)
	}
	if len(list.Resources) != 2 {
		t.Fatalf("resources len = %d, want 2", len(list.Resources))
	}
	if list.Resources[0].ID != "u1" || list.Resources[0].UserName != "ada" {
		t.Errorf("resource[0] = %#v", list.Resources[0])
	}
	if list.Resources[0].Name == nil || list.Resources[0].Name.Formatted != "Ada Lovelace" {
		t.Errorf("resource[0].Name = %#v", list.Resources[0].Name)
	}
	if len(list.Resources[0].Emails) != 1 || list.Resources[0].Emails[0].Value != "ada@example.test" {
		t.Errorf("resource[0].Emails = %#v", list.Resources[0].Emails)
	}
	if list.Resources[0].Active == nil || !*list.Resources[0].Active {
		t.Errorf("resource[0].Active = %#v", list.Resources[0].Active)
	}
	if list.Resources[1].Active == nil || *list.Resources[1].Active {
		t.Errorf("resource[1].Active = %#v", list.Resources[1].Active)
	}
}

func TestListUsersOmitsUnsetFilterAndStartIndex(t *testing.T) {
	var rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"totalResults": 0, "startIndex": 1, "itemsPerPage": 0, "resources": []}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.ListUsers(context.Background(), "", 0, 100); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if rawQuery != "count=100" {
		t.Fatalf("raw query = %q, want count=100", rawQuery)
	}
}

func TestListUsersSendsZeroCountForTotalsOnly(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, `{"totalResults": 42, "startIndex": 1, "itemsPerPage": 0, "resources": []}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	list, err := client.ListUsers(context.Background(), "", 0, 0)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if !gotQuery.Has("count") || gotQuery.Get("count") != "0" {
		t.Fatalf("count query = %q, want explicit 0 for totals-only request", gotQuery.Get("count"))
	}
	if gotQuery.Has("startIndex") {
		t.Fatalf("startIndex should be omitted, got %q", gotQuery.Get("startIndex"))
	}
	if list.TotalResults != 42 || len(list.Resources) != 0 {
		t.Fatalf("list = %#v", list)
	}
}

func TestListUsersRejectsNegativePagination(t *testing.T) {
	client, err := NewNexusAuthSCIMClient("https://scim.example.test", testSCIMCredential, time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.ListUsers(context.Background(), "", -1, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative startIndex: expected ErrInvalidArgument, got %v", err)
	}
	if _, err := client.ListUsers(context.Background(), "", 1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative count: expected ErrInvalidArgument, got %v", err)
	}
}

func TestListUsersEncodesFilterValue(t *testing.T) {
	var gotRawQuery string
	var gotFilter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		gotFilter = r.URL.Query().Get("filter")
		_, _ = io.WriteString(w, `{"totalResults": 0, "resources": []}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	filter := `emails.value co "a b+c&d/e"`
	if _, err := client.ListUsers(context.Background(), filter, 0, 1); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if gotFilter != filter {
		t.Fatalf("server decoded filter = %q, want %q", gotFilter, filter)
	}
	// Spaces use "+" and reserved characters are percent-encoded on the wire.
	const wantRawQuery = `count=1&filter=emails.value+co+%22a+b%2Bc%26d%2Fe%22`
	if gotRawQuery != wantRawQuery {
		t.Fatalf("raw query = %q, want %q", gotRawQuery, wantRawQuery)
	}
}

func TestListUsersDecodesCapitalizedResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"totalResults": 1, "resources": null, "Resources": [{"id": "u9", "userName": "linus"}]}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	list, err := client.ListUsers(context.Background(), "", 0, 1)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if list.TotalResults != 1 {
		t.Fatalf("totalResults = %d, want 1", list.TotalResults)
	}
	if len(list.Resources) != 1 || list.Resources[0].ID != "u9" {
		t.Fatalf("resources = %#v", list.Resources)
	}
}

func TestListUsersReturnsErrorOnNon2xxWithoutLeaking(t *testing.T) {
	const bodyDetail = "upstream-unavailable-detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"detail": "`+bodyDetail+`"}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	list, err := client.ListUsers(context.Background(), "", 0, 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if list != nil {
		t.Fatalf("expected nil list, got %#v", list)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want status code", err.Error())
	}
	if strings.Contains(err.Error(), testSCIMCredential) {
		t.Errorf("error must not leak credential: %q", err.Error())
	}
	if strings.Contains(err.Error(), bodyDetail) {
		t.Errorf("error must not echo response body: %q", err.Error())
	}
}

func TestListUsersUsesInjectedHTTPClient(t *testing.T) {
	transport := &recordingRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/scim+json"}},
			Body:       io.NopCloser(strings.NewReader(`{"totalResults": 0, "resources": []}`)),
		},
	}
	client, err := NewNexusAuthSCIMClientWithClient("https://scim.example.test", testSCIMCredential, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.ListUsers(context.Background(), "", 0, 1); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if transport.request == nil {
		t.Fatal("injected transport was not used")
	}
	if transport.request.URL.String() != "https://scim.example.test/scim/v2/Users?count=1" {
		t.Fatalf("url = %q", transport.request.URL.String())
	}
}

func TestListUsersRespectsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"totalResults": 0, "resources": []}`)
	}))
	defer server.Close()

	client, err := NewNexusAuthSCIMClient(server.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ListUsers(ctx, "", 0, 1); err == nil {
		t.Fatal("expected context cancellation error, got nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDefaultClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedHits int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&redirectedHits, 1)
		_, _ = io.WriteString(w, `{"totalResults": 0, "resources": []}`)
	}))
	defer redirected.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+"/scim/v2/Users", http.StatusFound)
	}))
	defer redirector.Close()

	client, err := NewNexusAuthSCIMClient(redirector.URL, testSCIMCredential, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.ListUsers(context.Background(), "", 0, 1); err == nil {
		t.Fatal("expected error for unfollowed redirect, got nil")
	}
	if got := atomic.LoadInt32(&redirectedHits); got != 0 {
		t.Fatalf("redirect target was hit %d times, want 0", got)
	}
}

type recordingRoundTripper struct {
	request  *http.Request
	response *http.Response
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.request = req
	return rt.response, nil
}
