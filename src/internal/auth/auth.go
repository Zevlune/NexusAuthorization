// Package auth implements the browser-facing OIDC authorization-code flow.
// It intentionally has no dependency on the permission database: identity is
// established by the configured provider and authorization remains a separate
// business concern.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/luck/permission-center-go/internal/config"
	"github.com/luck/permission-center-go/internal/logging"
	"golang.org/x/oauth2"
)

var (
	ErrDisabled        = errors.New("oidc authentication is disabled")
	ErrUnauthenticated = errors.New("authentication required")
	ErrInvalidToken    = errors.New("invalid access token")
	ErrInvalidRequest  = errors.New("invalid authentication request")
	ErrInvalidState    = errors.New("invalid authentication state")
	ErrInvalidNonce    = errors.New("invalid authentication nonce")
)

// User is the small identity projection stored in the encrypted local
// session. Access and refresh tokens are deliberately never placed in it.
type User struct {
	Subject       string `json:"sub"`
	Email         string `json:"email,omitempty"`
	Name          string `json:"name,omitempty"`
	Picture       string `json:"picture,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
}

type userContextKey struct{}

// WithUser stores a verified NexusAuth user in the request context.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// UserFromContext returns the verified NexusAuth user for the current request.
func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey{}).(User)
	return user, ok && user.Subject != ""
}

// UserIDFromContext returns the NexusAuth user ID stored in the access token's sub claim.
func UserIDFromContext(ctx context.Context) (string, bool) {
	user, ok := UserFromContext(ctx)
	return user.Subject, ok
}

// PublicConfig is safe to return to a browser. It excludes client secrets and
// session encryption material.
type PublicConfig struct {
	Enabled               bool     `json:"enabled"`
	Authority             string   `json:"authority,omitempty"`
	ClientID              string   `json:"clientId,omitempty"`
	RedirectURI           string   `json:"redirectUri,omitempty"`
	PostLogoutRedirectURI string   `json:"postLogoutRedirectUri,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	Audience              string   `json:"audience,omitempty"`
}

type Service struct {
	cfg                  config.OIDCConfig
	enabled              bool
	oauthConfig          *oauth2.Config
	verifier             *oidc.IDTokenVerifier
	verifyAccessToken    func(context.Context, string) (User, error)
	endSessionEndpoint   string
	sessions             *sessionStore
	httpClient           *http.Client
	publicAuthority      *url.URL
	backchannelAuthority *url.URL
	now                  func() time.Time
}

type accessTokenIntrospection struct {
	Active   bool   `json:"active"`
	Subject  string `json:"sub"`
	ClientID string `json:"client_id"`
	TokenUse string `json:"token_use"`
}

// New discovers the provider's authorization, token, issuer and JWKS
// metadata. Discovery is done at startup so a bad authority fails fast rather
// than producing an invalid login URL on the first request.
func New(ctx context.Context, cfg config.OIDCConfig) (*Service, error) {
	service := &Service{cfg: cfg, enabled: cfg.Enabled, now: time.Now}
	if !cfg.Enabled {
		return service, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	httpClient, publicAuthority, backchannelAuthority, err := newOIDCHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	sessions, err := newSessionStore(cfg)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(withOIDCHTTPClient(ctx, httpClient), cfg.Authority)
	if err != nil {
		return nil, fmt.Errorf("discover oidc provider: %w", err)
	}
	service.sessions = sessions
	service.httpClient = httpClient
	service.publicAuthority = publicAuthority
	service.backchannelAuthority = backchannelAuthority
	endpoint := provider.Endpoint()
	service.oauthConfig = &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:       service.publicEndpoint(endpoint.AuthURL),
			DeviceAuthURL: endpoint.DeviceAuthURL,
			TokenURL:      endpoint.TokenURL,
		},
		RedirectURL: cfg.RedirectURI,
		Scopes:      append([]string(nil), cfg.Scopes...),
	}
	var metadata struct {
		EndSessionEndpoint    string `json:"end_session_endpoint"`
		IntrospectionEndpoint string `json:"introspection_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("decode oidc discovery metadata: %w", err)
	}
	if strings.TrimSpace(metadata.IntrospectionEndpoint) == "" {
		return nil, errors.New("oidc discovery metadata does not contain an introspection endpoint")
	}
	service.endSessionEndpoint = service.publicEndpoint(metadata.EndSessionEndpoint)
	service.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	accessTokenVerifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.Audience,
	})
	service.verifyAccessToken = func(ctx context.Context, rawToken string) (User, error) {
		user, err := verifyNexusAccessToken(ctx, rawToken, accessTokenVerifier, httpClient)
		if err != nil {
			return User{}, err
		}
		introspection, err := introspectNexusAccessToken(
			ctx,
			rawToken,
			httpClient,
			metadata.IntrospectionEndpoint,
			cfg.ClientID,
			cfg.ClientSecret,
		)
		if err != nil {
			return User{}, fmt.Errorf("%w: introspection failed: %v", ErrInvalidToken, err)
		}
		if !introspection.Active ||
			introspection.Subject != user.Subject ||
			introspection.ClientID != cfg.ClientID ||
			introspection.TokenUse != "access_token" {
			return User{}, ErrInvalidToken
		}
		return user, nil
	}
	return service, nil
}

func verifyNexusAccessToken(ctx context.Context, rawToken string, verifier *oidc.IDTokenVerifier, httpClient *http.Client) (User, error) {
	token, err := verifier.Verify(withOIDCHTTPClient(ctx, httpClient), rawToken)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	var claims struct {
		TokenUse          string `json:"token_use"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		Picture           string `json:"picture"`
		EmailVerified     bool   `json:"email_verified"`
	}
	if err := token.Claims(&claims); err != nil {
		return User{}, fmt.Errorf("%w: decode claims", ErrInvalidToken)
	}
	if claims.TokenUse != "access_token" || strings.TrimSpace(token.Subject) == "" {
		return User{}, ErrInvalidToken
	}
	name := strings.TrimSpace(claims.Name)
	if name == "" {
		name = strings.TrimSpace(claims.PreferredUsername)
	}
	if name == "" {
		name = token.Subject
	}
	return User{
		Subject:       token.Subject,
		Email:         claims.Email,
		Name:          name,
		Picture:       claims.Picture,
		EmailVerified: claims.EmailVerified,
	}, nil
}

func introspectNexusAccessToken(
	ctx context.Context,
	rawToken string,
	client *http.Client,
	endpoint string,
	clientID string,
	clientSecret string,
) (accessTokenIntrospection, error) {
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	form := url.Values{"token": []string{rawToken}}
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return accessTokenIntrospection{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(clientID, clientSecret)
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return accessTokenIntrospection{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return accessTokenIntrospection{}, fmt.Errorf("introspection returned HTTP %d", response.StatusCode)
	}
	var result accessTokenIntrospection
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return accessTokenIntrospection{}, fmt.Errorf("decode introspection response: %w", err)
	}
	return result, nil
}

// NewForTest creates a service without network discovery. It is useful for
// testing cookie and middleware behavior; production code should use New.
func NewForTest(cfg config.OIDCConfig) (*Service, error) {
	sessions, err := newSessionStore(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		cfg:      cfg,
		enabled:  cfg.Enabled,
		sessions: sessions,
		now:      time.Now,
	}, nil
}

func (s *Service) Enabled() bool { return s != nil && s.enabled }

func (s *Service) PublicConfig() PublicConfig {
	if s == nil {
		return PublicConfig{}
	}
	return PublicConfig{
		Enabled:               s.cfg.Enabled,
		Authority:             s.cfg.Authority,
		ClientID:              s.cfg.ClientID,
		RedirectURI:           s.cfg.RedirectURI,
		PostLogoutRedirectURI: s.cfg.PostLogoutRedirectURI,
		Scopes:                append([]string(nil), s.cfg.Scopes...),
		Audience:              s.cfg.Audience,
	}
}

// Login creates a one-time state/nonce/PKCE transaction and persists it in
// the encrypted session cookie before returning the provider URL.
func (s *Service) Login(w http.ResponseWriter, r *http.Request) (string, error) {
	if s == nil || !s.enabled || s.oauthConfig == nil {
		return "", ErrDisabled
	}
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomToken(32)
	if err != nil {
		return "", err
	}
	data, err := s.sessions.load(r)
	if err != nil {
		data = &sessionData{}
	}
	data.State, data.Nonce, data.PKCEVerifier = state, nonce, verifier
	data.FlowCreatedAt = s.now().Unix()
	data.User = nil
	if err := s.sessions.save(w, data); err != nil {
		return "", err
	}
	return s.oauthConfig.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// Callback exchanges the authorization code, verifies the ID token using the
// discovery-backed verifier, and checks the nonce stored before redirect.
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) error {
	if s == nil || !s.enabled || s.oauthConfig == nil || s.verifier == nil {
		return ErrDisabled
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		return fmt.Errorf("%w: provider returned %s", ErrInvalidRequest, providerError)
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state == "" {
		return fmt.Errorf("%w: code and state are required", ErrInvalidRequest)
	}
	data, err := s.sessions.load(r)
	if err != nil || data.State == "" || data.Nonce == "" || data.PKCEVerifier == "" {
		return ErrInvalidState
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(data.State)) != 1 {
		return ErrInvalidState
	}
	if data.FlowCreatedAt == 0 || s.now().Sub(time.Unix(data.FlowCreatedAt, 0)) > flowMaxAge {
		return ErrInvalidState
	}
	// Consume the flow before making the upstream request, preventing a code
	// callback from being replayed if the browser retries the same URL.
	verifier, nonce := data.PKCEVerifier, data.Nonce
	data.State, data.Nonce, data.PKCEVerifier, data.FlowCreatedAt = "", "", "", 0
	if err := s.sessions.save(w, data); err != nil {
		return err
	}
	upstreamContext := s.withOIDCClient(r.Context())
	token, err := s.oauthConfig.Exchange(upstreamContext, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("exchange oidc authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return fmt.Errorf("%w: provider did not return an id_token", ErrInvalidRequest)
	}
	idToken, err := s.verifier.Verify(upstreamContext, rawIDToken)
	if err != nil {
		return fmt.Errorf("verify oidc id_token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return ErrInvalidNonce
	}
	var claims struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fmt.Errorf("decode oidc claims: %w", err)
	}
	data.User = &sessionUser{
		Subject:       idToken.Subject,
		Email:         claims.Email,
		Name:          claims.Name,
		Picture:       claims.Picture,
		EmailVerified: claims.EmailVerified,
		ExpiresAt:     idToken.Expiry.Unix(),
		IDToken:       rawIDToken,
	}
	if data.User.Subject == "" {
		return fmt.Errorf("%w: id_token subject is empty", ErrInvalidRequest)
	}
	return s.sessions.save(w, data)
}

func (s *Service) Me(r *http.Request) (User, bool) {
	if r != nil {
		if user, ok := UserFromContext(r.Context()); ok {
			return user, true
		}
	}
	if s == nil || s.sessions == nil {
		return User{}, false
	}
	data, err := s.sessions.load(r)
	if err != nil || data.User == nil {
		return User{}, false
	}
	if data.User.ExpiresAt == 0 || !s.now().Before(time.Unix(data.User.ExpiresAt, 0)) {
		return User{}, false
	}
	return User{
		Subject:       data.User.Subject,
		Email:         data.User.Email,
		Name:          data.User.Name,
		Picture:       data.User.Picture,
		EmailVerified: data.User.EmailVerified,
	}, true
}

// AuthenticateBearer validates a NexusAuth access token and returns its user claims.
// Signature, issuer, audience, expiry and not-before are checked by the OIDC verifier.
func (s *Service) AuthenticateBearer(ctx context.Context, rawToken string) (User, error) {
	if s == nil || !s.enabled || s.verifyAccessToken == nil {
		return User{}, ErrDisabled
	}
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return User{}, ErrInvalidToken
	}
	return s.verifyAccessToken(ctx, rawToken)
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) (string, error) {
	if s == nil {
		return "", ErrDisabled
	}
	var idTokenHint string
	if s.sessions != nil {
		if data, err := s.sessions.load(r); err == nil && data.User != nil {
			idTokenHint = data.User.IDToken
		}
		s.sessions.clear(w)
	}
	if !s.enabled || s.endSessionEndpoint == "" {
		return "", nil
	}
	logoutURL, err := url.Parse(s.endSessionEndpoint)
	if err != nil || logoutURL.Scheme == "" || logoutURL.Host == "" {
		return "", fmt.Errorf("invalid oidc end_session_endpoint")
	}
	query := logoutURL.Query()
	if idTokenHint != "" {
		query.Set("id_token_hint", idTokenHint)
	}
	if s.cfg.PostLogoutRedirectURI != "" {
		query.Set("post_logout_redirect_uri", s.cfg.PostLogoutRedirectURI)
	}
	query.Set("client_id", s.cfg.ClientID)
	logoutURL.RawQuery = query.Encode()
	return logoutURL.String(), nil
}

// Middleware protects API handlers while leaving the identity endpoints free
// to establish and clear a session.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil || !s.enabled {
			next.ServeHTTP(w, r)
			return
		}
		if rawToken, bearer, err := parseBearerToken(r.Header.Get("Authorization")); bearer {
			if err != nil {
				writeBearerUnauthorized(w)
				return
			}
			user, err := s.AuthenticateBearer(r.Context(), rawToken)
			if err != nil {
				logging.FromContext(r.Context()).Warn("Bearer authentication failed.",
					slog.String(logging.Subcategory, "Middleware"),
					slog.Any("Error", err),
				)
				writeBearerUnauthorized(w)
				return
			}
			setAuthenticatedUser(r, user)
			next.ServeHTTP(w, r)
			return
		}
		if user, ok := s.Me(r); ok {
			setAuthenticatedUser(r, user)
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"authentication required"}` + "\n"))
	})
}

func setAuthenticatedUser(r *http.Request, user User) {
	ctx := WithUser(r.Context(), user)
	requestLogger := logging.FromContext(ctx).With(slog.String(logging.Filter2, user.Subject))
	*r = *r.WithContext(logging.WithLogger(ctx, requestLogger))
}

func parseBearerToken(authorization string) (token string, bearer bool, err error) {
	fields := strings.Fields(authorization)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "Bearer") {
		return "", false, nil
	}
	if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" {
		return "", true, ErrInvalidToken
	}
	return fields[1], true, nil
}

func writeBearerUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}` + "\n"))
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate authentication value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// authorityRoutingTransport keeps the provider's public URLs in discovery and
// ID-token validation while routing server-to-server calls to the configured
// backchannel authority. The transport only rewrites requests whose scheme,
// host, and authority path match the public authority.
type authorityRoutingTransport struct {
	base        http.RoundTripper
	public      *url.URL
	backchannel *url.URL
}

func newOIDCHTTPClient(cfg config.OIDCConfig) (*http.Client, *url.URL, *url.URL, error) {
	public, err := parseAuthority(cfg.Authority, "oidc.authority")
	if err != nil {
		return nil, nil, nil, err
	}
	if strings.TrimSpace(cfg.BackchannelAuthority) == "" {
		return nil, public, nil, nil
	}
	backchannel, err := parseAuthority(cfg.BackchannelAuthority, "oidc.backchannel_authority")
	if err != nil {
		return nil, nil, nil, err
	}
	return &http.Client{Transport: &authorityRoutingTransport{
		base:        http.DefaultTransport,
		public:      public,
		backchannel: backchannel,
	}}, public, backchannel, nil
}

func parseAuthority(raw, field string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%s must be an absolute http or https URL", field)
	}
	return parsed, nil
}

func withOIDCHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return oidc.ClientContext(ctx, client)
}

func (s *Service) withOIDCClient(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return withOIDCHTTPClient(ctx, s.httpClient)
}

func (s *Service) publicEndpoint(raw string) string {
	if s == nil || s.publicAuthority == nil || s.backchannelAuthority == nil {
		return raw
	}
	rewritten, ok := rewriteAuthorityURL(raw, s.backchannelAuthority, s.publicAuthority)
	if !ok {
		return raw
	}
	return rewritten
}

func (t *authorityRoutingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	target, ok := rewriteAuthority(req.URL, t.public, t.backchannel)
	if !ok {
		return base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.URL = target
	if req.Host != "" && strings.EqualFold(req.Host, t.public.Host) {
		clone.Host = target.Host
	}
	return base.RoundTrip(clone)
}

func rewriteAuthorityURL(raw string, from, to *url.URL) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	rewritten, ok := rewriteAuthority(parsed, from, to)
	if !ok {
		return raw, false
	}
	return rewritten.String(), true
}

func rewriteAuthority(endpoint, from, to *url.URL) (*url.URL, bool) {
	if endpoint == nil || from == nil || to == nil || !sameAuthority(endpoint, from) {
		return nil, false
	}
	suffix, ok := authorityPathSuffix(endpoint.EscapedPath(), from.EscapedPath())
	if !ok {
		return nil, false
	}
	rewrittenPath := joinAuthorityPath(to.EscapedPath(), suffix)
	decodedPath, err := url.PathUnescape(rewrittenPath)
	if err != nil {
		return nil, false
	}
	rewritten := *endpoint
	rewritten.Scheme = to.Scheme
	rewritten.Host = to.Host
	rewritten.User = to.User
	rewritten.Path = decodedPath
	rewritten.RawPath = rewrittenPath
	return &rewritten, true
}

func sameAuthority(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func authorityPathSuffix(path, prefix string) (string, bool) {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return path, true
	}
	if path == prefix {
		return "", true
	}
	if strings.HasPrefix(path, prefix+"/") {
		return strings.TrimPrefix(path, prefix), true
	}
	return "", false
}

func joinAuthorityPath(prefix, suffix string) string {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		if suffix == "" {
			return ""
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}
	if suffix == "" {
		return prefix
	}
	if strings.HasPrefix(suffix, "/") {
		return prefix + suffix
	}
	return prefix + "/" + suffix
}
