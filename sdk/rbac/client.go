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

type Request struct {
	AppKey          string `json:"appKey"`
	ServiceResource string `json:"serviceResource"`
	Subject         string `json:"subject"`
	Domain          string `json:"domain"`
	Method          string `json:"method"`
	Path            string `json:"path"`
}
type Decision struct {
	Allowed        bool     `json:"allowed"`
	Effect         string   `json:"effect"`
	Reason         string   `json:"reason"`
	ReleaseVersion int64    `json:"release_version"`
	MatchedRoles   []string `json:"matched_roles"`
}
type Client struct {
	URL, Credential string
	HTTP            *http.Client
}

func NewClient(baseURL, credential string) *Client {
	return &Client{URL: strings.TrimRight(baseURL, "/") + "/v1/authorize", Credential: credential, HTTP: &http.Client{Timeout: 3 * time.Second}}
}
func (c *Client) Check(ctx context.Context, request Request) (Decision, error) {
	if c == nil || c.Credential == "" || request.AppKey == "" || request.ServiceResource == "" || request.Subject == "" || request.Domain == "" || request.Method == "" || request.Path == "" {
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
	if decision.Allowed && (decision.Effect != "allow" || decision.Reason != "allowed" || decision.ReleaseVersion <= 0) {
		return Decision{}, fmt.Errorf("invalid decision response")
	}
	return decision, nil
}

type Identity struct{ Subject, Domain string }
type IdentityResolver func(*http.Request) (Identity, bool)

// Require must be installed after authentication; the resolver uses trusted
// token/session context, never user-supplied role IDs or request query values.
func (c *Client) Require(appKey, serviceResource string, resolve IdentityResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appKey == "" || serviceResource == "" || resolve == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		identity, ok := resolve(r)
		if !ok || identity.Subject == "" || identity.Domain == "" {
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
