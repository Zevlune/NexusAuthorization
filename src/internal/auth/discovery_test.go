package auth

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/luck/nexus-authorization/internal/config"
)

const discoveryTestAuthority = "https://issuer.example.test"

type discoveryTestRoundTripper func(*http.Request) (*http.Response, error)

func (f discoveryTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.String() != discoveryTestAuthority+"/.well-known/openid-configuration" {
		return nil, fmt.Errorf("unexpected discovery request: %s %s", request.Method, request.URL)
	}
	return f(request)
}

func discoveryTestResponse(request *http.Request, issuer string) *http.Response {
	body, _ := json.Marshal(map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                discoveryTestAuthority + "/connect/authorize",
		"token_endpoint":                        discoveryTestAuthority + "/connect/token",
		"jwks_uri":                              discoveryTestAuthority + "/.well-known/jwks.json",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    request,
	}
}

func TestDiscoverOIDCProviderRetriesEOFThenSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var requestTimes []time.Duration
		client := &http.Client{Transport: discoveryTestRoundTripper(func(request *http.Request) (*http.Response, error) {
			requestTimes = append(requestTimes, time.Since(started))
			if len(requestTimes) < 3 {
				return nil, io.EOF
			}
			return discoveryTestResponse(request, discoveryTestAuthority), nil
		})}

		provider, err := discoverOIDCProvider(context.Background(), discoveryTestAuthority, client)
		if err != nil {
			t.Fatalf("discovery after two EOFs: %v", err)
		}
		if provider == nil {
			t.Fatal("discovery returned a nil provider")
		}
		wantTimes := []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond}
		if len(requestTimes) != len(wantTimes) {
			t.Fatalf("request times = %v, want %v", requestTimes, wantTimes)
		}
		for i, want := range wantTimes {
			if requestTimes[i] != want {
				t.Errorf("request %d at %v, want %v", i+1, requestTimes[i], want)
			}
		}
		endpoint := provider.Endpoint()
		if endpoint.AuthURL != discoveryTestAuthority+"/connect/authorize" || endpoint.TokenURL != discoveryTestAuthority+"/connect/token" {
			t.Errorf("provider endpoints changed public authority: %+v", endpoint)
		}
	})
}

func TestDiscoverOIDCProviderLimitsTransientFailuresToThreeRequests(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"EOF", io.EOF},
		{"unexpected EOF", io.ErrUnexpectedEOF},
		{"timeout", &net.DNSError{Err: "test timeout", IsTimeout: true}},
		{"connection reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{"broken pipe", &net.OpError{Op: "write", Net: "tcp", Err: syscall.EPIPE}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				requests := 0
				client := &http.Client{Transport: discoveryTestRoundTripper(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, test.err
				})}
				started := time.Now()
				provider, err := discoverOIDCProvider(context.Background(), discoveryTestAuthority, client)
				if provider != nil || !errors.Is(err, test.err) {
					t.Fatalf("provider = %v, error = %v; want nil provider and original failure", provider, err)
				}
				if requests != 3 {
					t.Errorf("requests = %d, want 3", requests)
				}
				if elapsed := time.Since(started); elapsed != 1500*time.Millisecond {
					t.Errorf("elapsed = %v, want 1.5s", elapsed)
				}
			})
		})
	}
}

func TestDiscoverOIDCProviderRejectsIssuerMismatchWithoutRetry(t *testing.T) {
	requests := 0
	const wrongIssuer = "https://different-issuer.example.test"
	client := &http.Client{Transport: discoveryTestRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		return discoveryTestResponse(request, wrongIssuer), nil
	})}

	provider, err := discoverOIDCProvider(context.Background(), discoveryTestAuthority, client)
	if provider != nil || err == nil {
		t.Fatalf("provider = %v, error = %v; want issuer validation failure", provider, err)
	}
	if !strings.Contains(err.Error(), "issuer") || !strings.Contains(err.Error(), wrongIssuer) {
		t.Errorf("error does not identify issuer mismatch: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
}

func TestDiscoverOIDCProviderDoesNotRetryPermanentFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "certificate", err: x509.UnknownAuthorityError{}},
		{name: "non-timeout network error", err: &net.DNSError{Err: "no such host", IsNotFound: true}},
		{name: "HTTP unauthorized", status: http.StatusUnauthorized},
		{name: "HTTP unavailable", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				requests := 0
				client := &http.Client{Transport: discoveryTestRoundTripper(func(request *http.Request) (*http.Response, error) {
					requests++
					if test.err != nil {
						return nil, test.err
					}
					response := discoveryTestResponse(request, discoveryTestAuthority)
					response.StatusCode = test.status
					return response, nil
				})}
				started := time.Now()
				provider, err := discoverOIDCProvider(context.Background(), discoveryTestAuthority, client)
				if provider != nil || err == nil {
					t.Fatalf("provider = %v, error = %v; want permanent failure", provider, err)
				}
				if requests != 1 {
					t.Errorf("requests = %d, want 1", requests)
				}
				if elapsed := time.Since(started); elapsed != 0 {
					t.Errorf("permanent failure delayed return by %v", elapsed)
				}
			})
		})
	}
}

func TestDiscoverOIDCProviderCancellationStopsRequests(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		requests := 0
		client := &http.Client{Transport: discoveryTestRoundTripper(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, io.EOF
		})}
		provider, err := discoverOIDCProvider(ctx, discoveryTestAuthority, client)
		if provider != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("provider = %v, error = %v; want context cancellation", provider, err)
		}
		if requests != 0 {
			t.Errorf("requests after cancellation = %d, want 0", requests)
		}
	})

	t.Run("canceled during retry delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := 0
			client := &http.Client{Transport: discoveryTestRoundTripper(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, io.EOF
			})}
			done := make(chan error, 1)
			go func() {
				_, err := discoverOIDCProvider(ctx, discoveryTestAuthority, client)
				done <- err
			}()
			synctest.Wait()
			if requests != 1 {
				t.Fatalf("requests before cancellation = %d, want 1", requests)
			}
			started := time.Now()
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("discovery error = %v, want context cancellation", err)
			}
			if elapsed := time.Since(started); elapsed != 0 {
				t.Errorf("cancellation waited %v for retry timer", elapsed)
			}
			time.Sleep(2 * time.Second)
			if requests != 1 {
				t.Errorf("requests after cancellation = %d, want 1", requests)
			}
		})
	})
}

func TestNewOIDCHTTPClientWithoutBackchannelUsesPublicAuthorityAndTimeout(t *testing.T) {
	client, public, backchannel, err := newOIDCHTTPClient(config.OIDCConfig{Authority: discoveryTestAuthority})
	if err != nil {
		t.Fatalf("create public OIDC client: %v", err)
	}
	if client == nil {
		t.Fatal("client is nil without a backchannel")
	}
	if client.Timeout != 15*time.Second {
		t.Errorf("client timeout = %v, want 15s", client.Timeout)
	}
	if public == nil || public.String() != discoveryTestAuthority || backchannel != nil {
		t.Fatalf("public authority = %v, backchannel = %v", public, backchannel)
	}
	if _, ok := client.Transport.(*authorityRoutingTransport); ok {
		t.Fatal("client without backchannel unexpectedly routes authority URLs")
	}
	requests := 0
	// Replace only this client's transport; leave http.DefaultTransport untouched.
	client.Transport = discoveryTestRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		return discoveryTestResponse(request, discoveryTestAuthority), nil
	})
	provider, err := discoverOIDCProvider(context.Background(), discoveryTestAuthority, client)
	if err != nil || provider == nil {
		t.Fatalf("public discovery: provider = %v, error = %v", provider, err)
	}
	if requests != 1 {
		t.Errorf("public discovery requests = %d, want 1", requests)
	}
}
