// Package rbac provides a fail-closed PEP client for trusted business services.
package rbac

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is one policy evaluation input. AppKey selects the application;
// ServiceResource, Subject, Method, and Path are required. Domain is optional
// and empty selects the default domain.
type Request struct {
	AppKey          string `json:"appKey"`
	ServiceResource string `json:"serviceResource"`
	Subject         string `json:"subject"`
	Domain          string `json:"domain"`
	Method          string `json:"method"`
	Path            string `json:"path"`
}

// Decision is the authorization service response. Allowed is authoritative;
// Effect, Reason, and MatchedRoles are diagnostic fields for callers and logs.
type Decision struct {
	Allowed      bool     `json:"allowed"`
	Effect       string   `json:"effect"`
	Reason       string   `json:"reason"`
	MatchedRoles []string `json:"matched_roles"`
}

// Client calls the authorization service. URL is the complete /api/v1/authorize
// endpoint, Credential is the service bearer token, and HTTP may be nil to use
// a three-second default client.
type Client struct {
	URL, Credential string
	HTTP            *http.Client
}

// NewClient creates a PEP client for baseURL. The credential is required for
// Check; a nil or empty URL will fail when a request is attempted.
func NewClient(baseURL, credential string) *Client {
	return &Client{URL: strings.TrimRight(baseURL, "/") + "/api/v1/authorize", Credential: credential, HTTP: &http.Client{Timeout: 3 * time.Second}}
}

// Check asks the authorization service whether a request is allowed. It fails
// closed on client construction, transport, non-200, decode, and malformed
// allow responses; only an allow decision with effect "allow" and reason
// "allowed" is accepted.
func (c *Client) Check(ctx context.Context, request Request) (Decision, error) {
	// Domain is optional: an empty value selects the default domain. Named
	// domains stay explicit, so the field is retained for tenant isolation.
	if c == nil || c.Credential == "" || request.AppKey == "" || request.ServiceResource == "" || request.Subject == "" || request.Method == "" || request.Path == "" {
		return Decision{}, fmt.Errorf("invalid authorization request or missing credential")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Decision{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Credential)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return Decision{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("authorization service returned %d", res.StatusCode)
	}
	var decision Decision
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&decision); err != nil {
		return Decision{}, err
	}
	if decision.Allowed && (decision.Effect != "allow" || decision.Reason != "allowed") {
		return Decision{}, fmt.Errorf("invalid decision response")
	}
	return decision, nil
}

// Identity is the authenticated subject and optional tenant domain supplied by
// the host service.
type Identity struct{ Subject, Domain string }

// IdentityResolver returns the authenticated identity for a request. It must
// use trusted authentication context and return ok=false when no identity is
// available.
type IdentityResolver func(*http.Request) (Identity, bool)

// Require returns middleware that checks the authenticated identity against the
// authorization service before calling next. It must be installed after
// authentication; missing configuration or identity and failed/denied checks
// all respond with 401 or 403 rather than forwarding the request.
func (c *Client) Require(appKey, serviceResource string, resolve IdentityResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appKey == "" || serviceResource == "" || resolve == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		identity, ok := resolve(r)
		if !ok || identity.Subject == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		decision, err := c.Check(ctx, Request{AppKey: appKey, ServiceResource: serviceResource, Subject: identity.Subject, Domain: identity.Domain, Method: r.Method, Path: r.URL.Path})
		if err != nil || !decision.Allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
