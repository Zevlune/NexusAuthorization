package biz

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// policyCacheKey identifies the exact current-record generation. Token is an
// internal invalidation identity and is part of the cache lookup, not a
// published version.
type policyCacheKey struct {
	applicationID uuid.UUID
	token         string
}

// currentPolicyKey derives the cache identity from the current policy metadata.
func currentPolicyKey(policy CurrentPolicy) policyCacheKey {
	return policyCacheKey{policy.ApplicationID, policy.Token}
}

// cachedRBACPolicy stores the key with the immutable compiled index so eviction
// and duplicate detection can compare the full identity.
type cachedRBACPolicy struct {
	key    policyCacheKey
	policy *compiledRBACPolicy
}

// Keep a bounded number of immutable indexes of current records. In-flight requests keep their
// own pointer even if an entry is evicted; cache identity is checked on every request.
type rbacPolicyCache struct {
	mu       sync.Mutex
	entries  map[policyCacheKey]*list.Element
	order    *list.List
	capacity int
	loads    singleflight.Group
}

// newRBACPolicyCache creates a bounded cache. A non-positive capacity is
// treated as one entry.
func newRBACPolicyCache(capacity int) *rbacPolicyCache {
	if capacity < 1 {
		capacity = 1
	}
	return &rbacPolicyCache{entries: make(map[policyCacheKey]*list.Element), order: list.New(), capacity: capacity}
}

// get returns a cached compiled policy and refreshes its LRU position.
func (c *rbacPolicyCache) get(key policyCacheKey) *compiledRBACPolicy {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(cachedRBACPolicy).policy
	}
	return nil
}

// put installs a compiled policy. Installing a newer generation removes older
// generations for the same application so the cache holds one current index per
// app, then evicts the least recently used entry when at capacity.
func (c *rbacPolicyCache) put(key policyCacheKey, policy *compiledRBACPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[key]; existing != nil {
		c.order.MoveToFront(existing)
		return
	}
	// Retain one current index per app, not a history of permission changes.
	for prior, element := range c.entries {
		if prior.applicationID == key.applicationID {
			delete(c.entries, prior)
			c.order.Remove(element)
		}
	}
	c.entries[key] = c.order.PushFront(cachedRBACPolicy{key, policy})
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		delete(c.entries, oldest.Value.(cachedRBACPolicy).key)
		c.order.Remove(oldest)
	}
}

// resolve returns the compiled policy for metadata, loading and compiling it
// once when absent. Concurrent loads for the same key are shared; the shared
// load uses an independent 30-second deadline so one canceled waiter cannot
// cancel the build for others. A policy whose identity changed during loading
// is rejected with ErrConflict.
func (c *rbacPolicyCache) resolve(ctx context.Context, metadata CurrentPolicy, load func(context.Context) (CurrentPolicy, error)) (*compiledRBACPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := currentPolicyKey(metadata)
	if cached := c.get(key); cached != nil {
		return cached, nil
	}
	flightKey := fmt.Sprintf("%s/%s", key.applicationID, key.token)
	result := c.loads.DoChan(flightKey, func() (any, error) {
		if cached := c.get(key); cached != nil {
			return cached, nil
		}
		// One canceled waiter must not cancel the shared build for others.
		// The independent load still has a strict deadline.
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		policy, err := load(loadCtx)
		if err != nil {
			return nil, err
		}
		if currentPolicyKey(policy) != key {
			return nil, ErrConflict
		}
		compiled := compileRBACPolicy(policy)
		if err := loadCtx.Err(); err != nil {
			return nil, err
		}
		c.put(key, compiled)
		return compiled, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return nil, loaded.Err
		}
		return loaded.Val.(*compiledRBACPolicy), nil
	}
}

// currentPolicy loads the current policy, retrying metadata races so a save is
// reflected without serving an older cached record.
func (s *RBACV1Service) currentPolicy(ctx context.Context, scope string) (CurrentPolicy, error) {
	for attempt := 0; attempt < 3; attempt++ {
		metadata, err := s.repository.GetPolicyMetadata(ctx, scope)
		if err != nil {
			return CurrentPolicy{}, err
		}
		policy, err := s.repository.GetCurrentPolicy(ctx, metadata)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return policy, err
	}
	return CurrentPolicy{}, ErrConflict
}

// activeCompiledPolicy returns the compiled current policy for an application.
// Small test/adaptor repositories that omit cache identity are compiled
// uncached; normal repositories use the bounded shared cache and retry
// ErrConflict when a save races the load.
func (s *RBACV1Service) activeCompiledPolicy(ctx context.Context, scope string) (*compiledRBACPolicy, error) {
	for attempt := 0; attempt < 3; attempt++ {
		metadata, err := s.repository.GetPolicyMetadata(ctx, scope)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Incomplete metadata is supported by small test/adaptor repositories, uncached.
		if metadata.ApplicationID == uuid.Nil || metadata.Token == "" {
			policy, err := s.repository.GetCurrentPolicy(ctx, metadata)
			if err != nil {
				return nil, err
			}
			return compileRBACPolicy(policy), nil
		}
		if cached := s.policies.get(currentPolicyKey(metadata)); cached != nil {
			return cached, nil
		}
		loadMetadata := metadata
		policy, err := s.policies.resolve(ctx, metadata, func(loadCtx context.Context) (CurrentPolicy, error) {
			return s.repository.GetCurrentPolicy(loadCtx, loadMetadata)
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return policy, err
	}
	return nil, ErrConflict
}
