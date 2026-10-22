package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxSwaggerDocumentSize = 8 << 20

const (
	swaggerControllerMaxLength = 200
	swaggerPathMaxLength       = 500
)

// SwaggerEndpoint is an import projection, not an authorization grant.
type SwaggerEndpoint struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Module  string `json:"module"`
	Summary string `json:"summary"`
}

// SwaggerFetchPolicy limits the addresses a Swagger import may contact. Imports
// fetch URLs supplied by administrators, so without limits the server could be
// used to probe internal networks or cloud metadata endpoints.
type SwaggerFetchPolicy struct {
	// AllowLoopback permits loopback addresses (local development only).
	AllowLoopback bool
	// AllowedHosts, when non-empty, lists the only host names and CIDR ranges
	// an import may contact.
	AllowedHosts []string
}

// errSwaggerAddressBlocked is returned when the policy rejects a target address.
var errSwaggerAddressBlocked = errors.New("swagger_url points to an address that imports may not access")

// Cloud metadata services outside the link-local range.
var swaggerMetadataNetworks = []*net.IPNet{
	mustParseCIDR("100.100.100.200/32"), // Alibaba Cloud
	mustParseCIDR("fd00:ec2::254/128"),  // AWS IPv6
}

func mustParseCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		panic(err)
	}
	return network
}

func (p SwaggerFetchPolicy) hostNameAllowed(host string) bool {
	for _, entry := range p.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(entry), host) {
			return true
		}
	}
	return false
}

// checkIP rejects addresses that imports must never reach and, with an
// allowlist, addresses outside it.
func (p SwaggerFetchPolicy) checkIP(ip net.IP, hostAllowed bool) error {
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 0 {
		return errSwaggerAddressBlocked // 0.0.0.0/8 reaches the local host
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return errSwaggerAddressBlocked
	}
	for _, network := range swaggerMetadataNetworks {
		if network.Contains(ip) {
			return errSwaggerAddressBlocked
		}
	}
	if ip.IsLoopback() && !p.AllowLoopback {
		return errSwaggerAddressBlocked
	}
	if len(p.AllowedHosts) == 0 || hostAllowed {
		return nil
	}
	for _, entry := range p.AllowedHosts {
		if _, network, err := net.ParseCIDR(strings.TrimSpace(entry)); err == nil && network.Contains(ip) {
			return nil
		}
	}
	return errSwaggerAddressBlocked
}

// dialContext resolves the host itself, checks every candidate address and
// connects to the checked IP, so DNS rebinding cannot swap in another address
// between the check and the connection.
func (p SwaggerFetchPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	hostAllowed := p.hostNameAllowed(host)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	lastErr := errSwaggerAddressBlocked
	for _, candidate := range addresses {
		if err := p.checkIP(candidate.IP, hostAllowed); err != nil {
			lastErr = err
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// httpClient fetches documents directly (never through an HTTP proxy, which
// would hide the real target from the address checks) and does not follow
// redirects.
func (p SwaggerFetchPolicy) httpClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         p.dialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func ReadSwaggerEndpoints(ctx context.Context, rawURL string, policy SwaggerFetchPolicy) ([]SwaggerEndpoint, error) {
	body, err := fetchSwaggerDocument(ctx, rawURL, policy)
	if err != nil {
		return nil, err
	}
	ops, err := parseSwaggerOperations(body)
	if err != nil {
		return nil, err
	}
	result := make([]SwaggerEndpoint, 0, len(ops))
	for _, op := range ops {
		result = append(result, SwaggerEndpoint{Method: op.Method, Path: op.PathTemplate, Module: op.Controller, Summary: op.Summary})
	}
	return result, nil
}

var (
	swaggerUIURLPattern       = regexp.MustCompile(`(?i)(?:["']?url["']?)\s*[:=]\s*["']([^"']+)['"]`)
	swaggerUIConfigURLPattern = regexp.MustCompile(`(?i)(?:["']?configUrl["']?)\s*[:=]\s*["']([^"']+)['"]`)
	swaggerInitializerPattern = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']*swagger-initializer[^"']*)["']`)
	swaggerUIScriptPattern    = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+\.js(?:\?[^"']*)?)["']`)
	swaggerControllerPattern  = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// SwaggerImportRequest 描述从远程 Swagger/OpenAPI 文档导入端点的请求。
type SwaggerImportRequest struct {
	ServiceResource string `json:"service_resource"`
	SwaggerURL      string `json:"swagger_url"`
}

// SwaggerImportResult 返回 Swagger/OpenAPI 导入的统计结果。
type SwaggerImportResult struct {
	Total   int `json:"total"`
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

type swaggerDocument struct {
	Swagger  string `json:"swagger" yaml:"swagger"`
	OpenAPI  string `json:"openapi" yaml:"openapi"`
	BasePath string `json:"basePath" yaml:"basePath"`
	Servers  []struct {
		URL string `json:"url" yaml:"url"`
	} `json:"servers" yaml:"servers"`
	Info struct {
		Title string `json:"title" yaml:"title"`
	} `json:"info" yaml:"info"`
	Paths map[string]swaggerPathItem `json:"paths" yaml:"paths"`
}

type swaggerOperation struct {
	Tags        []string `json:"tags" yaml:"tags"`
	OperationID string   `json:"operationId" yaml:"operationId"`
	Summary     string   `json:"summary" yaml:"summary"`
}

// Swagger/OpenAPI path items may contain parameters and servers in addition
// to operations. Explicit method fields leave those metadata keys ignored.
type swaggerPathItem struct {
	Get     *swaggerOperation `json:"get" yaml:"get"`
	Post    *swaggerOperation `json:"post" yaml:"post"`
	Put     *swaggerOperation `json:"put" yaml:"put"`
	Patch   *swaggerOperation `json:"patch" yaml:"patch"`
	Delete  *swaggerOperation `json:"delete" yaml:"delete"`
	Head    *swaggerOperation `json:"head" yaml:"head"`
	Options *swaggerOperation `json:"options" yaml:"options"`
	Trace   *swaggerOperation `json:"trace" yaml:"trace"`
}

func (p swaggerPathItem) operations() map[string]*swaggerOperation {
	return map[string]*swaggerOperation{
		"GET": p.Get, "POST": p.Post, "PUT": p.Put, "PATCH": p.Patch,
		"DELETE": p.Delete, "HEAD": p.Head, "OPTIONS": p.Options, "TRACE": p.Trace,
	}
}

type swaggerEndpointOperation struct {
	Controller   string
	Method       string
	PathTemplate string
	Summary      string
}

func parseSwaggerOperations(body []byte) ([]swaggerEndpointOperation, error) {
	var document swaggerDocument
	if err := json.Unmarshal(body, &document); err != nil {
		if yamlErr := yaml.Unmarshal(body, &document); yamlErr != nil {
			return nil, fmt.Errorf("invalid Swagger or OpenAPI document: %w", err)
		}
	}
	if document.Swagger != "2.0" && !strings.HasPrefix(document.OpenAPI, "3.") {
		return nil, fmt.Errorf("%w: only Swagger 2.0 and OpenAPI 3.x documents are supported", ErrInvalidArgument)
	}
	basePath := strings.TrimSpace(document.BasePath)
	if basePath == "" && len(document.Servers) > 0 {
		if serverURL, err := url.Parse(strings.TrimSpace(document.Servers[0].URL)); err == nil {
			basePath = serverURL.Path
		}
	}
	if basePath == "/" {
		basePath = ""
	}
	operations := make([]swaggerEndpointOperation, 0)
	for route, item := range document.Paths {
		for method, operation := range item.operations() {
			if operation == nil || !isSwaggerHTTPMethod(method) {
				continue
			}
			controller := swaggerController(*operation, document.Info.Title)
			pathTemplate := joinSwaggerPath(basePath, route)
			if err := validateSwaggerOperation(controller, method, pathTemplate); err != nil {
				return nil, err
			}
			summary := strings.TrimSpace(operation.Summary)
			if summary == "" {
				summary = strings.TrimSpace(operation.OperationID)
			}
			operations = append(operations, swaggerEndpointOperation{
				Controller: controller, Method: method, PathTemplate: pathTemplate, Summary: summary,
			})
		}
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("%w: Swagger document contains no HTTP operations", ErrInvalidArgument)
	}
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].PathTemplate != operations[j].PathTemplate {
			return operations[i].PathTemplate < operations[j].PathTemplate
		}
		return operations[i].Method < operations[j].Method
	})
	return operations, nil
}

func validateSwaggerOperation(controller, method, pathTemplate string) error {
	controller = strings.TrimSpace(controller)
	if controller == "" || len([]rune(controller)) > swaggerControllerMaxLength {
		return fmt.Errorf("%w: controller must be 1-%d characters", ErrInvalidArgument, swaggerControllerMaxLength)
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if !isSwaggerHTTPMethod(method) {
		return fmt.Errorf("%w: unsupported HTTP method %q", ErrInvalidArgument, method)
	}
	pathTemplate = strings.TrimSpace(pathTemplate)
	if pathTemplate == "" || len([]rune(pathTemplate)) > swaggerPathMaxLength || !strings.HasPrefix(pathTemplate, "/") {
		return fmt.Errorf("%w: path_template must be an absolute route path", ErrInvalidArgument)
	}
	return nil
}

func isSwaggerHTTPMethod(value string) bool {
	switch value {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}

func fetchSwaggerDocument(ctx context.Context, rawURL string, policy SwaggerFetchPolicy) ([]byte, error) {
	documentURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || documentURL.Scheme == "" || documentURL.Host == "" || (documentURL.Scheme != "http" && documentURL.Scheme != "https") {
		return nil, fmt.Errorf("%w: swagger_url must be an absolute http or https URL", ErrInvalidArgument)
	}
	client := policy.httpClient()
	document, err := fetchSwaggerDocumentAt(ctx, client, documentURL, 0)
	if err == nil || !policy.AllowLoopback || (documentURL.Hostname() != "localhost" && documentURL.Hostname() != "127.0.0.1") {
		return document, err
	}
	// Development only: in Docker, localhost may resolve to the API container.
	// Compose exposes host.docker.internal for the developer machine.
	hostURL := *documentURL
	hostURL.Host = "host.docker.internal"
	if documentURL.Port() != "" {
		hostURL.Host += ":" + documentURL.Port()
	}
	return fetchSwaggerDocumentAt(ctx, client, &hostURL, 0)
}

func fetchSwaggerDocumentAt(ctx context.Context, client *http.Client, documentURL *url.URL, depth int) ([]byte, error) {
	if depth > 3 {
		return nil, fmt.Errorf("%w: Swagger UI document reference is nested too deeply", ErrInvalidArgument)
	}
	fetch := func(value *url.URL) ([]byte, string, error) {
		if value.Scheme != "http" && value.Scheme != "https" {
			return nil, "", fmt.Errorf("%w: Swagger references must use http or https", ErrInvalidArgument)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, value.String(), nil)
		if err != nil {
			return nil, "", err
		}
		resp, err := client.Do(req)
		if errors.Is(err, errSwaggerAddressBlocked) {
			return nil, "", fmt.Errorf("%w: %w", ErrInvalidArgument, errSwaggerAddressBlocked)
		}
		if err != nil {
			return nil, "", fmt.Errorf("fetch Swagger document: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, "", fmt.Errorf("fetch Swagger document: received HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSwaggerDocumentSize+1))
		if err != nil {
			return nil, "", err
		}
		if len(body) > maxSwaggerDocumentSize {
			return nil, "", fmt.Errorf("%w: Swagger document exceeds 8 MiB", ErrInvalidArgument)
		}
		return body, resp.Header.Get("Content-Type"), nil
	}
	body, contentType, err := fetch(documentURL)
	if err != nil {
		return nil, err
	}
	lowerBody := bytes.ToLower(body)
	if bytes.Contains(lowerBody, []byte("<html")) || strings.Contains(strings.ToLower(contentType), "text/html") {
		match := swaggerUIURLPattern.FindSubmatch(body)
		if len(match) < 2 {
			match = swaggerUIConfigURLPattern.FindSubmatch(body)
		}
		if len(match) < 2 {
			match, err = swaggerUIReference(body, documentURL, fetch)
			if err != nil {
				return nil, err
			}
		}
		if len(match) < 2 {
			return nil, fmt.Errorf("%w: Swagger UI page does not declare a document URL", ErrInvalidArgument)
		}
		specURL, err := swaggerReferenceURL(documentURL, match[1])
		if err != nil {
			return nil, fmt.Errorf("%w: invalid Swagger UI document URL", ErrInvalidArgument)
		}
		return fetchSwaggerDocumentAt(ctx, client, specURL, depth+1)
	}
	if isSwaggerDocument(body) {
		return body, nil
	}
	if match := swaggerUIURLPattern.FindSubmatch(body); len(match) >= 2 {
		specURL, parseErr := swaggerReferenceURL(documentURL, match[1])
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid Swagger configuration document URL", ErrInvalidArgument)
		}
		return fetchSwaggerDocumentAt(ctx, client, specURL, depth+1)
	}
	if match := swaggerUIConfigURLPattern.FindSubmatch(body); len(match) >= 2 {
		specURL, parseErr := swaggerReferenceURL(documentURL, match[1])
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid Swagger configuration URL", ErrInvalidArgument)
		}
		return fetchSwaggerDocumentAt(ctx, client, specURL, depth+1)
	}
	return body, nil
}

func swaggerUIReference(body []byte, documentURL *url.URL, fetch func(*url.URL) ([]byte, string, error)) ([][]byte, error) {
	scripts := swaggerInitializerPattern.FindAllSubmatch(body, -1)
	if len(scripts) == 0 {
		scripts = swaggerUIScriptPattern.FindAllSubmatch(body, -1)
	}
	for index := len(scripts) - 1; index >= 0; index-- {
		scriptURL, err := swaggerReferenceURL(documentURL, scripts[index][1])
		if err != nil {
			return nil, fmt.Errorf("%w: invalid Swagger UI script URL", ErrInvalidArgument)
		}
		script, _, err := fetch(scriptURL)
		if err != nil {
			return nil, err
		}
		if match := swaggerUIURLPattern.FindSubmatch(script); len(match) >= 2 {
			return match, nil
		}
		if match := swaggerUIConfigURLPattern.FindSubmatch(script); len(match) >= 2 {
			return match, nil
		}
	}
	return nil, nil
}

// Swagger UI escapes slashes in inline JSON as \/; decode them before URL parsing.
func swaggerReferenceURL(documentURL *url.URL, reference []byte) (*url.URL, error) {
	value := strings.ReplaceAll(strings.TrimSpace(string(reference)), `\/`, "/")
	return documentURL.Parse(value)
}

func isSwaggerDocument(body []byte) bool {
	var document swaggerDocument
	if json.Unmarshal(body, &document) != nil {
		if yaml.Unmarshal(body, &document) != nil {
			return false
		}
	}
	return document.Swagger == "2.0" || strings.HasPrefix(document.OpenAPI, "3.")
}

func swaggerController(operation swaggerOperation, fallback string) string {
	if len(operation.Tags) > 0 && strings.TrimSpace(operation.Tags[0]) != "" {
		return sanitizeSwaggerController(operation.Tags[0])
	}
	if strings.TrimSpace(operation.OperationID) != "" {
		return operationIDController(operation.OperationID)
	}
	return sanitizeSwaggerController(fallback)
}

func operationIDController(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '.' || r == '/' || r == ':' || r == '-'
	})
	if len(parts) == 0 {
		return "default"
	}
	return sanitizeSwaggerController(parts[0])
}

func sanitizeSwaggerController(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = swaggerControllerPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-._")
	if value == "" {
		return "default"
	}
	if len([]rune(value)) > swaggerControllerMaxLength {
		return string([]rune(value)[:swaggerControllerMaxLength])
	}
	return value
}

func joinSwaggerPath(basePath, route string) string {
	return path.Clean("/" + strings.Trim(basePath, "/") + "/" + strings.TrimSpace(route))
}
