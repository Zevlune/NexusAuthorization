package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/luck/permission-center-go/internal/config"
)

func testConfig(enabled bool) config.OIDCConfig {
	return config.OIDCConfig{
		Enabled:       enabled,
		SessionSecret: "test-session-secret-at-least-32-characters",
		CookieName:    "permission_test_session",
	}
}

type payloadKeySet struct{ payload []byte }

func (s payloadKeySet) VerifySignature(context.Context, string) ([]byte, error) {
	return s.payload, nil
}

func nexusAccessTokenVerifier(t *testing.T, expiresAt time.Time, tokenUse string, expectedAudience ...string) *oidc.IDTokenVerifier {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"iss":       "http://nexus-auth:5100",
		"sub":       "user-42",
		"aud":       "permission.center.api",
		"exp":       expiresAt.Unix(),
		"iat":       time.Now().Add(-time.Minute).Unix(),
		"token_use": tokenUse,
		"name":      "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	audience := "permission.center.api"
	if len(expectedAudience) > 0 {
		audience = expectedAudience[0]
	}
	return oidc.NewVerifier("http://nexus-auth:5100", payloadKeySet{payload: payload}, &oidc.Config{
		ClientID: audience,
	})
}

func compactTestJWT() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return header + "." + payload + "." + signature
}

func TestVerifyNexusAccessTokenChecksExpiryAndTokenUse(t *testing.T) {
	validVerifier := nexusAccessTokenVerifier(t, time.Now().Add(time.Hour), "access_token")
	user, err := verifyNexusAccessToken(context.Background(), compactTestJWT(), validVerifier, nil)
	if err != nil || user.Subject != "user-42" || user.Name != "Alice" {
		t.Fatalf("verified user = %#v, err = %v", user, err)
	}

	expiredVerifier := nexusAccessTokenVerifier(t, time.Now().Add(-time.Minute), "access_token")
	if _, err := verifyNexusAccessToken(context.Background(), compactTestJWT(), expiredVerifier, nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token error = %v", err)
	}

	idTokenVerifier := nexusAccessTokenVerifier(t, time.Now().Add(time.Hour), "id_token")
	if _, err := verifyNexusAccessToken(context.Background(), compactTestJWT(), idTokenVerifier, nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("id token error = %v", err)
	}

	wrongAudienceVerifier := nexusAccessTokenVerifier(t, time.Now().Add(time.Hour), "access_token", "other.api")
	if _, err := verifyNexusAccessToken(context.Background(), compactTestJWT(), wrongAudienceVerifier, nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong audience error = %v", err)
	}
}

func TestSessionCookieRoundTripAndTamperRejection(t *testing.T) {
	store, err := newSessionStore(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := store.save(recorder, &sessionData{User: &sessionUser{Subject: "user-1", Name: "Alice", ExpiresAt: time.Now().Add(time.Hour).Unix()}}); err != nil {
		t.Fatal(err)
	}
	cookie := recorder.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	data, err := store.load(request)
	if err != nil || data.User == nil || data.User.Subject != "user-1" {
		t.Fatalf("loaded session = %#v, err = %v", data, err)
	}
	cookie.Value += "tampered"
	tampered := httptest.NewRequest(http.MethodGet, "/", nil)
	tampered.AddCookie(cookie)
	if _, err := store.load(tampered); err == nil {
		t.Fatal("expected a tampered cookie to be rejected")
	}
}

func TestMiddlewareRequiresSessionOnlyWhenEnabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	disabled, err := NewForTest(testConfig(false))
	if err != nil {
		t.Fatal(err)
	}
	disabledResponse := httptest.NewRecorder()
	disabled.Middleware(next).ServeHTTP(disabledResponse, httptest.NewRequest(http.MethodGet, "/v1/roles", nil))
	if disabledResponse.Code != http.StatusNoContent {
		t.Fatalf("disabled auth status = %d", disabledResponse.Code)
	}

	enabled, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	enabledResponse := httptest.NewRecorder()
	enabled.Middleware(next).ServeHTTP(enabledResponse, httptest.NewRequest(http.MethodGet, "/v1/roles", nil))
	if enabledResponse.Code != http.StatusUnauthorized {
		t.Fatalf("enabled auth status = %d", enabledResponse.Code)
	}
}

func TestMiddlewareAuthenticatesBearerAndAddsUserToContext(t *testing.T) {
	service, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	service.verifyAccessToken = func(_ context.Context, rawToken string) (User, error) {
		if rawToken != "valid-access-token" {
			t.Fatalf("raw token = %q", rawToken)
		}
		return User{Subject: "user-42", Name: "Alice"}, nil
	}

	var contextUser User
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		contextUser, ok = UserFromContext(r.Context())
		if !ok {
			t.Fatal("verified user missing from request context")
		}
		if me, ok := service.Me(r); !ok || me.Subject != contextUser.Subject {
			t.Fatalf("service user = %#v, %v", me, ok)
		}
		if userID, ok := UserIDFromContext(r.Context()); !ok || userID != "user-42" {
			t.Fatalf("context user id = %q, %v", userID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/roles", nil)
	request.Header.Set("Authorization", "bearer valid-access-token")
	response := httptest.NewRecorder()
	service.Middleware(next).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || contextUser.Subject != "user-42" {
		t.Fatalf("status = %d, user = %#v", response.Code, contextUser)
	}
}

func TestMiddlewareRejectsInvalidBearerWithoutCookieFallback(t *testing.T) {
	service, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	service.verifyAccessToken = func(context.Context, string) (User, error) {
		return User{}, errors.New("token expired")
	}

	cookieRecorder := httptest.NewRecorder()
	if err := service.sessions.save(cookieRecorder, &sessionData{User: &sessionUser{
		Subject: "cookie-user", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/roles", nil)
	request.AddCookie(cookieRecorder.Result().Cookies()[0])
	request.Header.Set("Authorization", "Bearer expired-token")
	response := httptest.NewRecorder()
	service.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid bearer must not fall back to a valid cookie")
	})).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Fatalf("status = %d, challenge = %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	if !strings.Contains(response.Body.String(), `"error":"invalid_token"`) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestMiddlewareRejectsMalformedBearer(t *testing.T) {
	service, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/roles", nil)
	request.Header.Set("Authorization", "Bearer")
	response := httptest.NewRecorder()
	service.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("malformed bearer must not reach the handler")
	})).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLogoutUsesNexusAuthIDTokenHint(t *testing.T) {
	service, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	service.endSessionEndpoint = "http://localhost:5100/connect/endsession"
	service.cfg.ClientID = "permission-center-web"
	service.cfg.PostLogoutRedirectURI = "http://localhost:5274/"

	loginRecorder := httptest.NewRecorder()
	if err := service.sessions.save(loginRecorder, &sessionData{User: &sessionUser{
		Subject: "user-1", IDToken: "signed-id-token", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request.AddCookie(loginRecorder.Result().Cookies()[0])
	logoutRecorder := httptest.NewRecorder()
	logoutURL, err := service.Logout(logoutRecorder, request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(logoutURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("id_token_hint") != "signed-id-token" {
		t.Fatalf("id_token_hint = %q", parsed.Query().Get("id_token_hint"))
	}
	if parsed.Query().Get("post_logout_redirect_uri") != "http://localhost:5274/" {
		t.Fatalf("post_logout_redirect_uri = %q", parsed.Query().Get("post_logout_redirect_uri"))
	}
	if len(logoutRecorder.Result().Cookies()) != 1 || logoutRecorder.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %#v", logoutRecorder.Result().Cookies())
	}
}
