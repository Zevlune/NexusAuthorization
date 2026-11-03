package biz

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNexusAuthServiceResourceCatalogCachesSuccessfulAndEmptySnapshots(t *testing.T) {
	var calls int
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: serviceResourceRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		body := `[{"id":"orders-id","name":"orders","displayName":"Orders","isActive":true}]`
		if calls == 2 {
			body = `[]`
		}
		return serviceResourceResponse(http.StatusOK, body), nil
	})}
	catalog, err := newNexusAuthServiceResourceCatalogWithClient(
		"https://nexus.example",
		"directory-token",
		time.Second,
		client,
		10*time.Millisecond,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}

	first, err := catalog.List(context.Background())
	if err != nil || len(first) != 1 || calls != 1 {
		t.Fatalf("first list = %#v, calls=%d, err=%v", first, calls, err)
	}
	now = now.Add(9 * time.Millisecond)
	second, err := catalog.List(context.Background())
	if err != nil || len(second) != 1 || calls != 1 {
		t.Fatalf("cached list = %#v, calls=%d, err=%v", second, calls, err)
	}

	now = now.Add(time.Millisecond)
	empty, err := catalog.List(context.Background())
	if err != nil || empty == nil || len(empty) != 0 || calls != 2 {
		t.Fatalf("expired list = %#v, calls=%d, err=%v", empty, calls, err)
	}
	emptyAgain, err := catalog.List(context.Background())
	if err != nil || emptyAgain == nil || len(emptyAgain) != 0 || calls != 2 {
		t.Fatalf("cached empty list = %#v, calls=%d, err=%v", emptyAgain, calls, err)
	}
}

func TestNexusAuthServiceResourceCatalogReturnsIsolatedSnapshots(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: serviceResourceRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return serviceResourceResponse(http.StatusOK, `[{"id":"orders-id","name":"orders","displayName":"Orders","audience":"orders","isActive":true}]`), nil
	})}
	catalog, err := NewNexusAuthServiceResourceCatalogWithClient("https://nexus.example", "directory-token", time.Second, client)
	if err != nil {
		t.Fatal(err)
	}

	first, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first[0].DisplayName = "caller changed"
	first[0].IsActive = false
	first[0] = nil

	second, err := catalog.List(context.Background())
	if err != nil || len(second) != 1 || second[0].DisplayName != "Orders" || !second[0].IsActive {
		t.Fatalf("cached list was mutated = %#v, err=%v", second, err)
	}
	second[0].Audience = "caller changed"

	found, err := catalog.Get(context.Background(), "orders")
	if err != nil || found.Audience != "orders" {
		t.Fatalf("cached get was mutated = %#v, err=%v", found, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestNexusAuthServiceResourceCatalogDoesNotCacheErrorsOrServeStaleData(t *testing.T) {
	var calls int
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: serviceResourceRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return serviceResourceResponse(http.StatusOK, `[{"name":"old","displayName":"Old","isActive":true}]`), nil
		case 2:
			return serviceResourceResponse(http.StatusServiceUnavailable, `unavailable`), nil
		default:
			return serviceResourceResponse(http.StatusOK, `[{"name":"new","displayName":"New","isActive":true}]`), nil
		}
	})}
	catalog, err := newNexusAuthServiceResourceCatalogWithClient(
		"https://nexus.example",
		"directory-token",
		time.Second,
		client,
		10*time.Millisecond,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}

	if values, err := catalog.List(context.Background()); err != nil || values[0].Key != "old" {
		t.Fatalf("initial list = %#v, err=%v", values, err)
	}
	now = now.Add(10 * time.Millisecond)
	if values, err := catalog.List(context.Background()); err == nil || values != nil {
		t.Fatalf("expired error returned stale data = %#v, err=%v", values, err)
	}
	values, err := catalog.List(context.Background())
	if err != nil || len(values) != 1 || values[0].Key != "new" {
		t.Fatalf("error was cached = %#v, calls=%d, err=%v", values, calls, err)
	}
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want 3", calls)
	}
}

func TestNexusAuthServiceResourceCatalogCoalescesRequestsAndPreservesCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var calls atomic.Int32
	requestContextErr := make(chan error, 1)
	client := &http.Client{Transport: serviceResourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-time.After(2 * time.Second):
			return nil, errors.New("test request timed out")
		}
		requestContextErr <- request.Context().Err()
		return serviceResourceResponse(http.StatusOK, `[{"name":"orders","displayName":"Orders","isActive":true}]`), nil
	})}
	catalog, err := NewNexusAuthServiceResourceCatalogWithClient("https://nexus.example", "directory-token", 5*time.Second, client)
	if err != nil {
		t.Fatal(err)
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := catalog.List(leaderCtx)
		leaderDone <- err
	}()
	<-started

	const waiters = 8
	start := make(chan struct{})
	ready := make(chan struct{}, waiters)
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		i := i
		go func() {
			ready <- struct{}{}
			<-start
			if i%2 == 0 {
				values, err := catalog.List(context.Background())
				if err == nil && (len(values) != 1 || values[0].Key != "orders") {
					err = errors.New("unexpected list result")
				}
				results <- err
				return
			}
			value, err := catalog.Get(context.Background(), "orders")
			if err == nil && value.Key != "orders" {
				err = errors.New("unexpected get result")
			}
			results <- err
		}()
	}
	for i := 0; i < waiters; i++ {
		<-ready
	}
	close(start)
	cancelLeader()

	select {
	case err := <-leaderDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled leader did not return")
	}
	close(release)
	for i := 0; i < waiters; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-requestContextErr; err != nil {
		t.Fatalf("leader cancellation reached shared request: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if values, err := catalog.List(canceledCtx); !errors.Is(err, context.Canceled) || values != nil {
		t.Fatalf("canceled cache hit = %#v, err=%v", values, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("canceled cache hit called upstream: %d", calls.Load())
	}
}

func serviceResourceResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
