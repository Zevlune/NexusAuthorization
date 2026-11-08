// Package rbac provides a fail-closed PEP client for trusted business services.
package rbac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// SubjectMenu is one granted menu or button. Children contains the nested
// descendants built by SubjectMenus in the service's sibling order.
type SubjectMenu struct {
	ID            string        `json:"id"`
	ApplicationID string        `json:"application_id"`
	ParentID      *string       `json:"parent_id"`
	Code          string        `json:"code"`
	Name          string        `json:"name"`
	MenuType      string        `json:"menu_type"`
	Route         string        `json:"route"`
	Component     string        `json:"component"`
	Icon          string        `json:"icon"`
	SortOrder     int           `json:"sort_order"`
	Enabled       bool          `json:"enabled"`
	Children      []SubjectMenu `json:"children,omitempty"`
}

// Menu is an alias for SubjectMenu.
type Menu = SubjectMenu

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

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 3 * time.Second}
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
	res, err := c.httpClient().Do(req)
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

// SubjectMenus returns the granted menu tree for one verified subject. The
// service credential is required; domain is optional and uses the default
// domain when empty.
func (c *Client) SubjectMenus(ctx context.Context, appKey, subject, domain string) ([]SubjectMenu, error) {
	if c == nil || strings.TrimSpace(c.Credential) == "" || strings.TrimSpace(appKey) == "" || strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("invalid subject menus request or missing credential")
	}
	endpoint, err := c.subjectMenusEndpoint(appKey, subject, domain)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Credential)
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subject menus service returned %d", res.StatusCode)
	}
	var response struct {
		Success *bool `json:"success"`
		Result  *struct {
			Items *[]SubjectMenu `json:"items"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	if err = decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode subject menus response: %w", err)
	}
	if err = ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode subject menus response: %w", err)
	}
	if response.Success == nil || !*response.Success || response.Result == nil || response.Result.Items == nil {
		return nil, fmt.Errorf("invalid subject menus response")
	}
	return buildSubjectMenuTree(*response.Result.Items)
}

func (c *Client) subjectMenusEndpoint(appKey, subject, domain string) (string, error) {
	endpoint, err := url.Parse(c.URL)
	if err != nil {
		return "", fmt.Errorf("parse authorization URL: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return "", fmt.Errorf("authorization URL must be absolute")
	}
	const authorizationPath = "/api/v1/authorize"
	cleanPath := strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(cleanPath, authorizationPath) {
		return "", fmt.Errorf("authorization URL must end with %s", authorizationPath)
	}
	endpoint.Path = strings.TrimSuffix(cleanPath, authorizationPath) + "/api/v1/subject-menus"
	endpoint.RawPath = ""
	endpoint.Fragment = ""
	query := url.Values{}
	query.Set("app_key", appKey)
	query.Set("subject", subject)
	query.Set("domain", domain)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func buildSubjectMenuTree(items []SubjectMenu) ([]SubjectMenu, error) {
	if len(items) == 0 {
		return []SubjectMenu{}, nil
	}
	indexByID := make(map[string]int, len(items))
	for i := range items {
		items[i].Children = nil
		if items[i].ID == "" {
			return nil, fmt.Errorf("invalid subject menus response: menu id is empty")
		}
		if _, exists := indexByID[items[i].ID]; exists {
			return nil, fmt.Errorf("invalid subject menus response: duplicate menu id %q", items[i].ID)
		}
		indexByID[items[i].ID] = i
	}
	children := make([][]int, len(items))
	roots := make([]int, 0, len(items))
	for i := range items {
		if items[i].ParentID == nil || *items[i].ParentID == "" {
			roots = append(roots, i)
			continue
		}
		parent, ok := indexByID[*items[i].ParentID]
		if !ok {
			return nil, fmt.Errorf("invalid subject menus response: parent %q is missing", *items[i].ParentID)
		}
		children[parent] = append(children[parent], i)
	}
	visited := make([]bool, len(items))
	visiting := make([]bool, len(items))
	var build func(int) (SubjectMenu, error)
	build = func(index int) (SubjectMenu, error) {
		if visiting[index] {
			return SubjectMenu{}, fmt.Errorf("invalid subject menus response: menu hierarchy contains a cycle")
		}
		visiting[index] = true
		menu := items[index]
		for _, child := range children[index] {
			value, err := build(child)
			if err != nil {
				return SubjectMenu{}, err
			}
			menu.Children = append(menu.Children, value)
		}
		visiting[index] = false
		visited[index] = true
		return menu, nil
	}
	tree := make([]SubjectMenu, 0, len(roots))
	for _, root := range roots {
		value, err := build(root)
		if err != nil {
			return nil, err
		}
		tree = append(tree, value)
	}
	for i := range visited {
		if !visited[i] {
			return nil, fmt.Errorf("invalid subject menus response: menu hierarchy contains a cycle")
		}
	}
	return tree, nil
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
		decision, err := c.Check(ctx, Request{AppKey: appKey, ServiceResource: serviceResource, Subject: identity.Subject, Domain: identity.Domain, Method: r.Method, Path: authorizationPath(r.URL)})
		if err != nil || !decision.Allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// segmentEscaper keeps characters that would change the segment structure
// escaped after a segment is decoded.
var segmentEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

// authorizationPath returns the request path with the same segments the Go
// ServeMux routes on. r.URL.Path decodes %2F into a separator, so
// /orders/1%2Fitems would be checked as /orders/1/items while the mux
// dispatches /orders/{id}. Each escaped segment is decoded on its own and an
// encoded slash or percent stays escaped. An undecodable path returns "" so
// Check fails closed.
func authorizationPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	parts := strings.Split(u.EscapedPath(), "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return ""
		}
		parts[i] = segmentEscaper.Replace(decoded)
	}
	return strings.Join(parts, "/")
}
