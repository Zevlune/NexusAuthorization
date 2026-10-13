package apitoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testToken = "nxa_token-secret-that-must-not-appear-in-errors"
	testKey   = "internal-service-key-that-must-not-appear-in-errors"
)

func testTarget() Target {
	return Target{
		AppKey:          "orders",
		ServiceResource: "orders-api",
		Method:          http.MethodPost,
		Path:            "/v1/orders/42",
		TenantID:        "tenant-a",
		ProjectID:       "project-1",
	}
}

func TestAuthorizeSendsContractAndReturnsDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != authorizePath {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != testKey {
			t.Fatalf("service key = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		var payload struct {
			Token string `json:"token"`
			Target
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Token != testToken || payload.Target != testTarget() {
			t.Fatalf("payload = %+v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"active":true,"allowed":true,"reason":"allowed","token_id":"token-1","app_key":"orders","owner_type":"service","owner_id":"orders-api"}`)
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, ServiceKey: testKey})
	decision, err := client.Authorize(t.Context(), testToken, testTarget())
	if err != nil {
		t.Fatalf("Authorize returned error: %v", err)
	}
	if !decision.Active || !decision.Allowed || decision.Reason != "allowed" || decision.TokenID != "token-1" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizeInvalidTokenAndForbiddenAreDecisions(t *testing.T) {
	for _, response := range []string{
		`{"active":false,"allowed":false,"reason":"invalid_token"}`,
		`{"active":true,"allowed":false,"reason":"permission_denied","token_id":"token-1"}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, response)
			}))
			defer server.Close()
			decision, err := NewClient(Options{BaseURL: server.URL, ServiceKey: testKey}).Authorize(t.Context(), testToken, testTarget())
			if err != nil {
				t.Fatalf("Authorize returned error: %v", err)
			}
			if decision.Allowed {
				t.Fatalf("invalid/forbidden response was allowed: %+v", decision)
			}
		})
	}
}

func TestAuthorizeClassifiesHTTPFailuresWithoutResponseSecrets(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, "body contains "+testToken+" and "+testKey)
			}))
			defer server.Close()

			_, err := NewClient(Options{BaseURL: server.URL, ServiceKey: testKey}).Authorize(t.Context(), testToken, testTarget())
			if err == nil {
				t.Fatal("expected HTTP failure")
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
				t.Fatalf("error leaked secret: %v", err)
			}
			switch status {
			case http.StatusBadRequest:
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want ErrInvalidInput", err)
				}
			case http.StatusUnauthorized, http.StatusForbidden:
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("error = %v, want ErrUnauthorized", err)
				}
			default:
				if !errors.Is(err, ErrServiceFailure) {
					t.Fatalf("error = %v, want ErrServiceFailure", err)
				}
			}
		})
	}
}

func TestAuthorizeRejectsMalformedMissingAndOversizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"active":`},
		{name: "missing reason", body: `{"active":true,"allowed":true}`},
		{name: "allowed inactive", body: `{"active":false,"allowed":true,"reason":"allowed"}`},
		{name: "oversized", body: `{"active":true,"allowed":true,"reason":"` + strings.Repeat("x", maxResponseSize) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := NewClient(Options{BaseURL: server.URL, ServiceKey: testKey}).Authorize(t.Context(), testToken, testTarget())
			if !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("error = %v, want ErrMalformedResponse", err)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
				t.Fatalf("error leaked secret: %v", err)
			}
		})
	}
}

func TestAuthorizeNetworkErrorAndTimeoutDoNotExposeSecrets(t *testing.T) {
	client := NewClient(Options{
		BaseURL:    "https://permission-center.example",
		ServiceKey: testKey,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("transport saw %s and %s", testToken, testKey)
		})},
	})
	_, err := client.Authorize(t.Context(), testToken, testTarget())
	if !errors.Is(err, ErrServiceFailure) || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("network error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"active":true,"allowed":true,"reason":"allowed"}`)
		case <-t.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err = NewClient(Options{BaseURL: server.URL, ServiceKey: testKey}).Authorize(ctx, testToken, testTarget())
	if !errors.Is(err, ErrServiceFailure) || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestAuthorizeRejectsCrossHostRedirectAndDoesNotMutateHTTPClient(t *testing.T) {
	received := make(chan struct{}, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"active":true,"allowed":true,"reason":"allowed"}`)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+authorizePath, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	callerClient := &http.Client{}
	client := NewClient(Options{BaseURL: source.URL, ServiceKey: testKey, HTTPClient: callerClient})
	_, err := client.Authorize(t.Context(), testToken, testTarget())
	if err == nil || !errors.Is(err, ErrServiceFailure) {
		t.Fatalf("redirect error = %v", err)
	}
	select {
	case <-received:
		t.Fatal("cross-host redirect reached destination")
	default:
	}
	if callerClient.CheckRedirect != nil {
		t.Fatal("NewClient mutated caller CheckRedirect")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestAuthorizeDefaultHTTPClientRejectsCrossHostRedirect(t *testing.T) {
	received := make(chan struct{}, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"active":true,"allowed":true,"reason":"allowed"}`)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+authorizePath, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	_, err := NewClient(Options{BaseURL: source.URL, ServiceKey: testKey}).Authorize(t.Context(), testToken, testTarget())
	if err == nil || !errors.Is(err, ErrServiceFailure) {
		t.Fatalf("redirect error = %v", err)
	}
	select {
	case <-received:
		t.Fatal("cross-host redirect reached destination")
	default:
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestNewClientValidatesBaseURLAndTarget(t *testing.T) {
	for _, baseURL := range []string{
		"http://service.example",
		"https://user:password@service.example",
		"https://service.example?secret=1",
		"https://service.example#fragment",
	} {
		if _, err := NewClient(Options{BaseURL: baseURL, ServiceKey: testKey}).Authorize(t.Context(), testToken, testTarget()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("base URL %q error = %v", baseURL, err)
		}
	}

	client := NewClient(Options{BaseURL: "https://permission-center.example", ServiceKey: testKey})
	for _, target := range []Target{
		{AppKey: "orders", ServiceResource: "orders-api", Method: "get", Path: "/orders"},
		{AppKey: "orders", ServiceResource: "orders-api", Method: "GET", Path: "/orders?token=1"},
		{AppKey: "orders", ServiceResource: "orders-api", Method: "GET", Path: "/orders//1"},
	} {
		if _, err := client.Authorize(t.Context(), testToken, target); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("target %+v error = %v", target, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
