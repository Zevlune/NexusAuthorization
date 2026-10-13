// Package apitoken provides a small fail-closed client for the internal API
// token authorization endpoint.
package apitoken

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

const (
	authorizePath   = "/internal/v1/api-tokens/authorize"
	defaultTimeout  = 3 * time.Second
	maxResponseSize = 64 << 10
	maxTokenSize    = 4096
)

var (
	// ErrInvalidInput indicates invalid client configuration or an invalid
	// authorization request. It never includes token or service-key values.
	ErrInvalidInput = errors.New("invalid API token authorization input")
	// ErrUnauthorized indicates that the internal caller was rejected.
	ErrUnauthorized = errors.New("internal authorization caller was rejected")
	// ErrServiceFailure indicates that the authorization service could not
	// provide a decision.
	ErrServiceFailure = errors.New("API token authorization service failure")
	// ErrMalformedResponse indicates a successful HTTP response that does not
	// contain the documented decision JSON.
	ErrMalformedResponse = errors.New("malformed API token authorization response")
)

// Options configures a Client. ServiceKey is an internal, startup-loaded
// credential and is independent of the raw API token supplied to Authorize.
type Options struct {
	BaseURL    string
	ServiceKey string

	// HTTPClient is copied before use. The caller's client and its redirect
	// policy are never mutated. HTTP is retained as a compatibility alias;
	// HTTPClient takes precedence when both are set.
	HTTPClient *http.Client
	HTTP       *http.Client
}

// Target describes the trusted request scope that the API token should be
// checked against. Callers must resolve tenant and project independently of
// untrusted request body values before constructing a Target.
type Target struct {
	AppKey          string `json:"app_key"`
	ServiceResource string `json:"service_resource"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	TenantID        string `json:"tenant_id"`
	ProjectID       string `json:"project_id"`
}

// Decision is the authorization service's raw decision. A valid token with
// insufficient permissions is represented by a successful response with
// Allowed=false; it is not a transport error.
type Decision struct {
	Active    bool   `json:"active"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason"`
	TokenID   string `json:"token_id,omitempty"`
	AppKey    string `json:"app_key,omitempty"`
	OwnerType string `json:"owner_type,omitempty"`
	OwnerID   string `json:"owner_id,omitempty"`
}

// Client calls the internal authorization endpoint without caching decisions.
type Client struct {
	endpoint   string
	serviceKey string
	http       *http.Client
	configErr  error
}

// NewClient constructs a client. Configuration is validated on the first
// Authorize call so callers can create clients during startup without needing
// a second constructor form. Invalid configuration always fails closed.
func NewClient(options Options) *Client {
	c := &Client{serviceKey: options.ServiceKey}

	base, err := parseBaseURL(options.BaseURL)
	if err != nil {
		c.configErr = fmt.Errorf("%w: invalid base URL", ErrInvalidInput)
	} else {
		base.Path = strings.TrimRight(base.Path, "/") + authorizePath
		base.RawPath = ""
		base.RawQuery = ""
		base.Fragment = ""
		c.endpoint = base.String()
	}

	if strings.TrimSpace(options.ServiceKey) == "" {
		c.configErr = fmt.Errorf("%w: missing internal service key", ErrInvalidInput)
	}

	provided := options.HTTPClient
	if provided == nil {
		provided = options.HTTP
	}
	if provided == nil {
		c.http = &http.Client{Timeout: defaultTimeout, CheckRedirect: rejectCrossOriginRedirect}
	} else {
		copy := *provided
		if copy.Timeout == 0 {
			copy.Timeout = defaultTimeout
		}
		if copy.CheckRedirect == nil {
			copy.CheckRedirect = rejectCrossOriginRedirect
		} else {
			original := copy.CheckRedirect
			copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				if err := rejectCrossOriginRedirect(req, via); err != nil {
					return err
				}
				if err := original(req, via); err != nil {
					return errors.New("redirect rejected")
				}
				return nil
			}
		}
		c.http = &copy
	}

	return c
}

// New is the error-returning constructor variant for callers that want to
// validate configuration during startup. NewClient remains convenient for
// dependency wiring; both constructors produce the same client.
func New(options Options) (*Client, error) {
	c := NewClient(options)
	if c.configErr != nil {
		return nil, c.configErr
	}
	return c, nil
}

// Authorize asks the internal endpoint for one uncached decision.
func (c *Client) Authorize(ctx context.Context, rawToken string, target Target) (Decision, error) {
	if c == nil || c.configErr != nil {
		if c != nil && c.configErr != nil {
			return Decision{}, c.configErr
		}
		return Decision{}, fmt.Errorf("%w: nil client", ErrInvalidInput)
	}
	if ctx == nil || strings.TrimSpace(rawToken) == "" || len(rawToken) > maxTokenSize {
		return Decision{}, fmt.Errorf("%w: token and context are required", ErrInvalidInput)
	}
	if err := validateTarget(target); err != nil {
		return Decision{}, err
	}

	payload := struct {
		Token string `json:"token"`
		Target
	}{Token: rawToken, Target: target}
	body, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: could not encode request", ErrInvalidInput)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Decision{}, fmt.Errorf("%w: could not create request", ErrInvalidInput)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.serviceKey)

	res, err := c.http.Do(req)
	if err != nil {
		// Do not return url.Error or a custom transport error: either can embed
		// request details supplied by a caller or transport implementation.
		return Decision{}, fmt.Errorf("%w: request failed", ErrServiceFailure)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		switch res.StatusCode {
		case http.StatusBadRequest:
			return Decision{}, fmt.Errorf("%w: authorization service returned HTTP 400", ErrInvalidInput)
		case http.StatusUnauthorized, http.StatusForbidden:
			return Decision{}, fmt.Errorf("%w: authorization service rejected the internal caller", ErrUnauthorized)
		default:
			return Decision{}, fmt.Errorf("%w: authorization service returned HTTP %d", ErrServiceFailure, res.StatusCode)
		}
	}

	response, err := io.ReadAll(io.LimitReader(res.Body, maxResponseSize+1))
	if err != nil || len(response) > maxResponseSize {
		return Decision{}, ErrMalformedResponse
	}
	var decision Decision
	if err := json.Unmarshal(response, &decision); err != nil || strings.TrimSpace(decision.Reason) == "" || (decision.Allowed && !decision.Active) {
		return Decision{}, ErrMalformedResponse
	}
	return decision, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, ErrInvalidInput
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && isLocalHost(parsed.Hostname())) {
		return nil, ErrInvalidInput
	}
	if parsed.Hostname() == "" {
		return nil, ErrInvalidInput
	}
	parsed.Scheme = scheme
	return parsed, nil
}

func isLocalHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func rejectCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	if previous == nil || previous.URL == nil || req == nil || req.URL == nil || !sameOrigin(previous.URL, req.URL) {
		return errors.New("cross-origin redirect rejected")
	}
	return nil
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.AppKey) == "" || strings.TrimSpace(target.ServiceResource) == "" || len(target.AppKey) > 128 || len(target.ServiceResource) > 128 || len(target.TenantID) > 200 || len(target.ProjectID) > 200 {
		return fmt.Errorf("%w: invalid target scope", ErrInvalidInput)
	}
	switch target.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("%w: invalid target method", ErrInvalidInput)
	}
	if len(target.Path) == 0 || len(target.Path) > 2048 || target.Path[0] != '/' || strings.ContainsAny(target.Path, "?#\\\x00\r\n") || strings.Contains(target.Path, "//") {
		return fmt.Errorf("%w: invalid target path", ErrInvalidInput)
	}
	for _, segment := range strings.Split(strings.TrimPrefix(target.Path, "/"), "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\\x00") {
			return fmt.Errorf("%w: invalid target path", ErrInvalidInput)
		}
	}
	return nil
}
