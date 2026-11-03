package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type introspectionCacheTestState struct {
	calls  atomic.Int32
	active atomic.Bool
	fail   atomic.Bool
}

var (
	introspectionSigningKeyOnce sync.Once
	introspectionSigningKey     *rsa.PrivateKey
	introspectionSigningKeyErr  error
)

func TestAuthenticateBearerCachesValidJWTs(t *testing.T) {
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	service, token, state := newIntrospectionCacheTestService(t, func() time.Time { return now })

	for range 3 {
		user, err := service.AuthenticateBearer(context.Background(), token)
		if err != nil || user.Subject != "user-42" {
			t.Fatalf("authenticated user = %#v, err=%v", user, err)
		}
	}
	if calls := state.calls.Load(); calls != 1 {
		t.Fatalf("introspection calls = %d, want 1", calls)
	}
}

func TestAuthenticateBearerReintrospectsAtTTLAndRejectsInactiveToken(t *testing.T) {
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	service, token, state := newIntrospectionCacheTestService(t, func() time.Time { return now })

	if _, err := service.AuthenticateBearer(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(15 * time.Second)
	state.active.Store(false)

	if user, err := service.AuthenticateBearer(context.Background(), token); !errors.Is(err, ErrInvalidToken) || user != (User{}) {
		t.Fatalf("inactive token result = %#v, err=%v", user, err)
	}
	if calls := state.calls.Load(); calls != 2 {
		t.Fatalf("introspection calls after TTL = %d, want 2", calls)
	}
}

func TestAuthenticateBearerInvalidJWTDoesNotUseCachedSuccess(t *testing.T) {
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	service, token, state := newIntrospectionCacheTestService(t, func() time.Time { return now })

	if _, err := service.AuthenticateBearer(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := service.AuthenticateBearer(context.Background(), compactTestJWT()); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("invalid JWT error = %v", err)
		}
	}
	if calls := state.calls.Load(); calls != 1 {
		t.Fatalf("invalid JWTs triggered introspection: calls=%d, want 1", calls)
	}
	if _, err := service.AuthenticateBearer(context.Background(), token); err != nil {
		t.Fatalf("valid cached token stopped working: %v", err)
	}
}

func TestAuthenticateBearerDoesNotCacheIntrospectionFailure(t *testing.T) {
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	service, token, state := newIntrospectionCacheTestService(t, func() time.Time { return now })
	state.fail.Store(true)

	if _, err := service.AuthenticateBearer(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("first authentication error = %v", err)
	}
	state.fail.Store(false)
	if _, err := service.AuthenticateBearer(context.Background(), token); err != nil {
		t.Fatalf("retry after introspection failure: %v", err)
	}
	if _, err := service.AuthenticateBearer(context.Background(), token); err != nil {
		t.Fatalf("cached retry success: %v", err)
	}
	if calls := state.calls.Load(); calls != 2 {
		t.Fatalf("introspection calls = %d, want 2", calls)
	}
}

func newIntrospectionCacheTestService(t *testing.T, now func() time.Time) (*Service, string, *introspectionCacheTestState) {
	t.Helper()
	key := introspectionTestSigningKey(t)
	state := &introspectionCacheTestState{}
	state.active.Store(true)
	var token string
	var issuer string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeIntrospectionTestJSON(t, w, map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                issuer + "/connect/authorize",
				"token_endpoint":                        issuer + "/connect/token",
				"jwks_uri":                              issuer + "/jwks",
				"introspection_endpoint":                issuer + "/introspect",
				"end_session_endpoint":                  issuer + "/connect/endsession",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			writeIntrospectionTestJSON(t, w, map[string]any{
				"keys": []any{rsaPublicJWK(&key.PublicKey)},
			})
		case "/introspect":
			state.calls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse introspection form: %v", err)
			}
			if got := r.PostForm.Get("token"); got != token {
				t.Errorf("introspection token = %q, want test JWT", got)
			}
			if state.fail.Load() {
				http.Error(w, `{"error":"temporarily_unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			writeIntrospectionTestJSON(t, w, map[string]any{
				"active":    state.active.Load(),
				"sub":       "user-42",
				"client_id": "permission.center",
				"token_use": "access_token",
				"exp":       now().Add(time.Hour).Unix(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	issuer = server.URL
	token = signIntrospectionTestJWT(t, key, server.URL)
	cfg := testConfig(true)
	cfg.Authority = server.URL
	cfg.ClientID = "permission.center"
	cfg.ClientSecret = "client-secret"
	cfg.Audience = "permission.center.api"
	cfg.RedirectURI = "http://localhost:8080/signin-oidc"
	cfg.PostLogoutRedirectURI = "http://localhost:5274/"
	cfg.Scopes = []string{"openid", "profile"}
	service, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	service.now = now
	return service, token, state
}

func writeIntrospectionTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

func introspectionTestSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	introspectionSigningKeyOnce.Do(func() {
		introspectionSigningKey, introspectionSigningKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if introspectionSigningKeyErr != nil {
		t.Fatal(introspectionSigningKeyErr)
	}
	return introspectionSigningKey
}

func signIntrospectionTestJWT(t *testing.T, key *rsa.PrivateKey, issuer string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": "introspection-cache-test", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"iss":       issuer,
		"sub":       "user-42",
		"aud":       "permission.center.api",
		"exp":       now.Add(time.Hour).Unix(),
		"iat":       now.Add(-time.Minute).Unix(),
		"token_use": "access_token",
		"name":      "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func rsaPublicJWK(publicKey *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": "introspection-cache-test",
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
	}
}
