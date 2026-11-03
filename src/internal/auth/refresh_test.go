package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func browserSessionRequest(t *testing.T, s *Service, user sessionUser) *http.Request {
	t.Helper()
	w := httptest.NewRecorder()
	if err := s.sessions.save(w, &sessionData{User: &user}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(w.Result().Cookies()[0])
	return r
}

func refreshTestService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s, err := NewForTest(testConfig(true))
	if err != nil {
		t.Fatal(err)
	}
	s.oauthConfig = &oauth2.Config{ClientID: "console", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: server.URL, AuthStyle: oauth2.AuthStyleInHeader}}
	return s
}

func TestSessionRefreshRotatesCookieAndCoalescesConcurrentRequests(t *testing.T) {
	var requests atomic.Int32
	s := refreshTestService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("unexpected refresh grant: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":600}`)
	})
	deadline := time.Now().Add(2 * time.Hour).Unix()
	u := sessionUser{Subject: "alice", Name: "Alice", ExpiresAt: time.Now().Add(-time.Second).Unix(), SessionExpiresAt: deadline, RefreshToken: "old-refresh", IDToken: "logout-hint"}
	r := browserSessionRequest(t, s, u)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			user, ok := s.SessionUser(w, r.Clone(context.Background()))
			if !ok || user.Subject != "alice" {
				t.Errorf("refresh failed: %+v, %v", user, ok)
				return
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly {
				t.Error("expected renewed HttpOnly cookie")
				return
			}
			next := httptest.NewRequest(http.MethodGet, "/", nil)
			next.AddCookie(cookies[0])
			data, err := s.sessions.load(next)
			if err != nil || data.User.RefreshToken != "rotated-refresh" || data.User.SessionExpiresAt != deadline || data.User.IDToken != "logout-hint" {
				t.Error("renewal lost credentials or extended absolute session deadline")
			}
			if strings.Contains(cookies[0].Value, "rotated-refresh") {
				t.Error("refresh token was not encrypted")
			}
			if cookies[0].MaxAge <= 0 || cookies[0].MaxAge > int((2*time.Hour)/time.Second) {
				t.Error("cookie outlives absolute session deadline")
			}
		}()
	}
	wg.Wait()
	if got := requests.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
}

func TestSessionRefreshFailsClosedAndEnforcesAbsoluteDeadline(t *testing.T) {
	for _, test := range []string{"provider_error", "no_refresh", "absolute_expiry", "legacy_cookie", "invalid_bearer"} {
		t.Run(test, func(t *testing.T) {
			var calls atomic.Int32
			s := refreshTestService(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			})
			u := sessionUser{Subject: "alice", ExpiresAt: time.Now().Add(-time.Second).Unix(), SessionExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshToken: "refresh"}
			switch test {
			case "no_refresh":
				u.RefreshToken = ""
			case "absolute_expiry":
				u.SessionExpiresAt = time.Now().Add(time.Minute).Unix()
				u.ExpiresAt = time.Now().Add(time.Hour).Unix()
				s.now = func() time.Time { return time.Unix(u.SessionExpiresAt+1, 0) }
			case "legacy_cookie":
				u.SessionExpiresAt = 0
			case "invalid_bearer":
				u.ExpiresAt = time.Now().Add(time.Hour).Unix()
			}
			r := browserSessionRequest(t, s, u)
			if test == "invalid_bearer" {
				r.Header.Set("Authorization", "Bearer invalid")
			}
			w := httptest.NewRecorder()
			if _, ok := s.SessionUser(w, r); ok {
				t.Fatal("invalid session accepted")
			}
			want := int32(0)
			if test == "provider_error" {
				want = 1
			}
			if got := calls.Load(); got != want {
				t.Fatalf("refresh calls = %d, want %d", got, want)
			}
			if test != "invalid_bearer" && len(w.Result().Cookies()) > 0 && w.Result().Cookies()[0].MaxAge >= 0 {
				t.Error("invalid session cookie was not cleared")
			}
		})
	}
}

func TestRefreshedIDTokenMustPreserveSubjectAndNonce(t *testing.T) {
	for _, test := range []struct {
		name, subject, nonce string
		want                 bool
	}{
		{"same identity", "alice", "login-nonce", true},
		{"nonce optional", "alice", "", true},
		{"subject changed", "mallory", "login-nonce", false},
		{"nonce changed", "alice", "other-nonce", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims, _ := json.Marshal(map[string]any{"iss": "https://issuer.example", "aud": "console", "sub": test.subject, "nonce": test.nonce, "exp": time.Now().Add(time.Hour).Unix(), "name": "Alice Updated"})
			raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
			s := refreshTestService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "expires_in": 600, "id_token": raw})
			})
			s.verifier = oidc.NewVerifier("https://issuer.example", payloadKeySet{payload: claims}, &oidc.Config{ClientID: "console"})
			r := browserSessionRequest(t, s, sessionUser{Subject: "alice", ExpiresAt: time.Now().Add(-time.Second).Unix(), RefreshToken: "refresh", AuthNonce: "login-nonce", SessionExpiresAt: time.Now().Add(time.Hour).Unix()})
			user, ok := s.SessionUser(httptest.NewRecorder(), r)
			if ok != test.want {
				t.Fatalf("accepted = %v, want %v", ok, test.want)
			}
			if ok && user.Name != "Alice Updated" {
				t.Error("refreshed profile was not applied")
			}
		})
	}
}

func TestTokenCacheExpiresDoesNotRetainFailuresAndIsBounded(t *testing.T) {
	c := newTokenCache[int]()
	now := time.Now()
	clock := func() time.Time { return now }
	calls := 0
	fail := false
	fetch := func(context.Context) (int, time.Time, error) {
		calls++
		if fail {
			return 0, time.Time{}, errors.New("upstream unavailable")
		}
		return calls, now.Add(15 * time.Second), nil
	}
	for range 2 {
		if _, err := c.load(context.Background(), "token", clock, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("cache calls = %d", calls)
	}
	now = now.Add(15 * time.Second)
	fail = true
	for range 2 {
		if _, err := c.load(context.Background(), "token", clock, fetch); err == nil {
			t.Fatal("served stale success or cached failure")
		}
	}
	if calls != 3 {
		t.Fatalf("failure calls = %d", calls)
	}
	fail = false
	for i := 0; i < tokenCacheCapacity+10; i++ {
		if _, err := c.load(context.Background(), fmt.Sprint(i), clock, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) > tokenCacheCapacity {
		t.Fatal("unbounded cache")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.load(ctx, "canceled", clock, fetch); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request = %v", err)
	}
}
