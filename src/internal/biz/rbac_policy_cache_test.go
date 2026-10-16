package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type indexedPolicyRepository struct {
	*rbacV1FakeRepository
	mu                       sync.Mutex
	active                   CurrentPolicy
	states                   map[string]CurrentPolicy
	metadataErr              error
	dataErr                  error
	metadataReads, dataReads int
	gate                     <-chan struct{}
	started                  chan struct{}
	once                     sync.Once
}

func (r *indexedPolicyRepository) GetPolicyMetadata(context.Context, string) (CurrentPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadataReads++
	metadata := r.active
	metadata.Data = nil
	return metadata, r.metadataErr
}
func (r *indexedPolicyRepository) GetCurrentPolicy(ctx context.Context, metadata CurrentPolicy) (CurrentPolicy, error) {
	r.mu.Lock()
	r.dataReads++
	state, err, gate := r.states[metadata.Token], r.dataErr, r.gate
	r.mu.Unlock()
	if r.started != nil {
		r.once.Do(func() { close(r.started) })
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return CurrentPolicy{}, ctx.Err()
		}
	}
	return state, err
}

func indexedTestPolicy(_ int64, granted bool) CurrentPolicy {
	return CurrentPolicy{ApplicationID: uuid.New(), Token: uuid.NewString(), Data: directPolicyData(true, granted)}
}

func indexedTestInput() map[string]any {
	return map[string]any{"subject": "user-1", "domain": "tenant-a", "service_resource": "order-service", "method": "GET", "path": "/books"}
}

func TestServiceCachesIndexButChecksTokenOnEveryRequest(t *testing.T) {
	first := indexedTestPolicy(1, true)
	repo := &indexedPolicyRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}, active: first, states: map[string]CurrentPolicy{first.Token: first}}
	service := NewRBACV1Service(repo)
	for i := 0; i < 5; i++ {
		decision, err := service.Authorize(context.Background(), "app", indexedTestInput())
		if err != nil || !decision.Allowed {
			t.Fatalf("decision %+v err %v", decision, err)
		}
	}
	if repo.metadataReads != 5 || repo.dataReads != 1 {
		t.Fatalf("metadata=%d recordss=%d", repo.metadataReads, repo.dataReads)
	}
	second := indexedTestPolicy(2, false)
	second.ApplicationID = first.ApplicationID
	repo.active, repo.states[second.Token] = second, second
	decision, err := service.Authorize(context.Background(), "app", indexedTestInput())
	if err != nil || decision.Allowed {
		t.Fatalf("revocation %+v %v", decision, err)
	}
	if repo.dataReads != 2 {
		t.Fatalf("new state must compile once: %d", repo.dataReads)
	}
	repo.metadataErr = ErrNotFound
	if _, err := service.Authorize(context.Background(), "app", indexedTestInput()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cached policy bypassed disabled app: %v", err)
	}
	repo.metadataErr = errors.New("database unavailable")
	if _, err := service.Authorize(context.Background(), "app", indexedTestInput()); err == nil {
		t.Fatal("cached policy bypassed unavailable database")
	}
}

func TestConcurrentRequestsCompileStateOnce(t *testing.T) {
	state := indexedTestPolicy(1, true)
	gate := make(chan struct{})
	repo := &indexedPolicyRepository{rbacV1FakeRepository: &rbacV1FakeRepository{}, active: state, states: map[string]CurrentPolicy{state.Token: state}, gate: gate, started: make(chan struct{})}
	service := NewRBACV1Service(repo)
	var wg sync.WaitGroup
	errors := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := service.Authorize(context.Background(), "app", indexedTestInput())
			if err != nil {
				errors <- err
			} else if !decision.Allowed {
				errors <- ErrConflict
			}
		}()
	}
	select {
	case <-repo.started:
	case <-time.After(5 * time.Second):
		t.Fatal("records load did not start")
	}
	close(gate)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if repo.dataReads != 1 || repo.metadataReads != 32 {
		t.Fatalf("metadata=%d recordss=%d", repo.metadataReads, repo.dataReads)
	}
}

func TestPolicyCacheStateIsolationEvictionAndRetry(t *testing.T) {
	cache := newRBACPolicyCache(2)
	ctx := context.Background()
	first, second, third := indexedTestPolicy(1, true), indexedTestPolicy(2, false), indexedTestPolicy(3, true)
	load := func(state CurrentPolicy) func(context.Context) (CurrentPolicy, error) {
		return func(context.Context) (CurrentPolicy, error) { return state, nil }
	}
	for _, state := range []CurrentPolicy{first, second} {
		if _, err := cache.resolve(ctx, state, load(state)); err != nil {
			t.Fatal(err)
		}
	}
	cache.get(currentPolicyKey(first)) // first is now most recently used
	if _, err := cache.resolve(ctx, third, load(third)); err != nil {
		t.Fatal(err)
	}
	if cache.get(currentPolicyKey(second)) != nil || len(cache.entries) != 2 {
		t.Fatal("LRU did not evict second state")
	}
	var attempts atomic.Int32
	fail := func(context.Context) (CurrentPolicy, error) { attempts.Add(1); return CurrentPolicy{}, ErrConflict }
	for i := 0; i < 2; i++ {
		if _, err := cache.resolve(ctx, second, fail); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if attempts.Load() != 2 {
		t.Fatal("load failure was cached")
	}
	if _, err := cache.resolve(ctx, second, load(first)); err == nil {
		t.Fatal("accepted mismatching state identity")
	}
}

func TestCanceledWaiterDoesNotCancelSharedPolicyLoad(t *testing.T) {
	cache := newRBACPolicyCache(2)
	state := indexedTestPolicy(1, true)
	started, gate := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (CurrentPolicy, error) {
		close(started)
		select {
		case <-gate:
			return state, nil
		case <-ctx.Done():
			return CurrentPolicy{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := cache.resolve(ctx, state, load); result <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("load did not start")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(gate)
	policy, err := cache.resolve(context.Background(), state, func(context.Context) (CurrentPolicy, error) { t.Error("shared build was lost"); return state, nil })
	if err != nil || !policy.Authorize(indexedTestInput()).Allowed {
		t.Fatalf("shared build failed: %v", err)
	}
}

func TestDelayedOldPolicyCannotReplaceNewState(t *testing.T) {
	cache := newRBACPolicyCache(2)
	old, latest := indexedTestPolicy(1, true), indexedTestPolicy(2, false)
	latest.ApplicationID = old.ApplicationID
	started, gate, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := cache.resolve(context.Background(), old, func(context.Context) (CurrentPolicy, error) { close(started); <-gate; return old, nil })
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old load did not start")
	}
	if _, err := cache.resolve(context.Background(), latest, func(context.Context) (CurrentPolicy, error) { return latest, nil }); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	policy, err := cache.resolve(context.Background(), latest, func(context.Context) (CurrentPolicy, error) { return latest, nil })
	if err != nil || policy.Authorize(indexedTestInput()).Allowed {
		t.Fatal("late old state replaced revocation")
	}
}
