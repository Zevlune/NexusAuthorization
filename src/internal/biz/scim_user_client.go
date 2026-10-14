package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultSCIMHTTPTimeout applies when the caller does not provide a positive timeout.
	defaultSCIMHTTPTimeout = 15 * time.Second
	// scimJSONMediaType is the SCIM 2.0 media type expected by the read API.
	scimJSONMediaType = "application/scim+json"
	// scimUsersPath is appended to baseURL to reach the SCIM 2.0 Users endpoint.
	scimUsersPath = "/scim/v2/Users"
)

// SCIMUserName models the SCIM 2.0 user "name" sub-attribute.
type SCIMUserName struct {
	Formatted  string `json:"formatted,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
}

// SCIMUserEmail models a single SCIM 2.0 user "emails" entry.
type SCIMUserEmail struct {
	Value   string `json:"value,omitempty"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// SCIMUser models the read-only subset of a SCIM 2.0 User resource.
type SCIMUser struct {
	Schemas    []string        `json:"schemas,omitempty"`
	ID         string          `json:"id,omitempty"`
	ExternalID string          `json:"externalId,omitempty"`
	UserName   string          `json:"userName,omitempty"`
	Name       *SCIMUserName   `json:"name,omitempty"`
	Emails     []SCIMUserEmail `json:"emails,omitempty"`
	Active     *bool           `json:"active,omitempty"`
}

// SCIMUserList models a SCIM 2.0 ListResponse of User resources.
type SCIMUserList struct {
	Schemas      []string   `json:"schemas,omitempty"`
	TotalResults int        `json:"totalResults"`
	StartIndex   int        `json:"startIndex"`
	ItemsPerPage int        `json:"itemsPerPage"`
	Resources    []SCIMUser `json:"resources"`
}

// NexusAuthSCIMClient is a read-only SCIM 2.0 client for NexusAuth.
type NexusAuthSCIMClient struct {
	baseURL    string
	credential string
	httpClient *http.Client
}

// NewNexusAuthSCIMClient builds a client with a dedicated HTTP client using the
// supplied timeout. Non-positive timeouts fall back to defaultSCIMHTTPTimeout.
// The dedicated client refuses to follow redirects so the Bearer credential is
// never forwarded to another origin.
func NewNexusAuthSCIMClient(baseURL, credential string, timeout time.Duration) (*NexusAuthSCIMClient, error) {
	if timeout <= 0 {
		timeout = defaultSCIMHTTPTimeout
	}
	return NewNexusAuthSCIMClientWithClient(baseURL, credential, newDefaultSCIMHTTPClient(timeout))
}

// NewNexusAuthSCIMClientWithClient builds a client with an injected HTTP client,
// which is convenient for tests and for sharing connection pools. A nil client
// is replaced with the default redirect-refusing client.
func NewNexusAuthSCIMClientWithClient(baseURL, credential string, client *http.Client) (*NexusAuthSCIMClient, error) {
	normalizedBaseURL, err := normalizeSCIMBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(credential) == "" {
		return nil, fmt.Errorf("%w: credential is required", ErrInvalidArgument)
	}
	if client == nil {
		client = newDefaultSCIMHTTPClient(defaultSCIMHTTPTimeout)
	}
	return &NexusAuthSCIMClient{
		baseURL:    normalizedBaseURL,
		credential: credential,
		httpClient: client,
	}, nil
}

// ListUsers calls GET {baseURL}/scim/v2/Users and decodes the SCIM ListResponse.
// The filter and startIndex query parameters are only sent when set. count is
// always sent: count=0 asks the server for totalResults only, matching SCIM
// pagination semantics. Negative startIndex or count is rejected.
func (c *NexusAuthSCIMClient) ListUsers(ctx context.Context, filter string, startIndex, count int) (*SCIMUserList, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: scim client is nil", ErrInvalidArgument)
	}
	if startIndex < 0 {
		return nil, fmt.Errorf("%w: startIndex must not be negative", ErrInvalidArgument)
	}
	if count < 0 {
		return nil, fmt.Errorf("%w: count must not be negative", ErrInvalidArgument)
	}

	endpoint, err := url.Parse(c.baseURL + scimUsersPath)
	if err != nil {
		return nil, fmt.Errorf("build scim users url: %w", err)
	}
	query := endpoint.Query()
	if strings.TrimSpace(filter) != "" {
		query.Set("filter", filter)
	}
	if startIndex > 0 {
		query.Set("startIndex", strconv.Itoa(startIndex))
	}
	query.Set("count", strconv.Itoa(count))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build scim users request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set("Accept", scimJSONMediaType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request scim users: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// The response body is intentionally not read or echoed: it may contain
		// user profile data, and errors must never surface the credential.
		return nil, fmt.Errorf("scim users request failed: unexpected status %d", resp.StatusCode)
	}

	var list SCIMUserList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode scim users response: %w", err)
	}
	return &list, nil
}

// newDefaultSCIMHTTPClient builds the client used when none is injected. It
// never follows redirects, so the Bearer credential stays bound to the
// configured host.
func newDefaultSCIMHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// normalizeSCIMBaseURL validates that raw is an absolute http/https base URL
// without userinfo, query or fragment, and returns it without trailing slashes.
func normalizeSCIMBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: baseURL is required", ErrInvalidArgument)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: baseURL is not a valid URL", ErrInvalidArgument)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: baseURL scheme must be http or https", ErrInvalidArgument)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: baseURL host is required", ErrInvalidArgument)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w: baseURL must not contain userinfo", ErrInvalidArgument)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("%w: baseURL must not contain a query", ErrInvalidArgument)
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", fmt.Errorf("%w: baseURL must not contain a fragment", ErrInvalidArgument)
	}
	return strings.TrimRight(trimmed, "/"), nil
}
