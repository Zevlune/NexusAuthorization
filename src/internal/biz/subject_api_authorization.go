package biz

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// subjectAPICacheScope identifies the authorization identity of one aggregate.
// It is used only to keep one compiled generation per scope in the local LRU.
type subjectAPICacheScope struct {
	applicationKey  string
	applicationID   uuid.UUID
	subject         string
	domain          string
	serviceResource string
	serviceActive   bool
}

// subjectAPICacheKey is the SHA-256 of the normalized aggregate JSON. The key
// is process-local and never persisted; every authorization still performs the
// repository read before consulting this cache.
type subjectAPICacheKey [sha256.Size]byte

// normalizedSubjectAPIIdentity canonicalizes the fields that are compared and
// cached: subject and service are trimmed and the domain is folded so the
// default domain and its legacy alias share one identity. ServiceActive is
// preserved so an inactive service never reuses an active compiled index.
func normalizedSubjectAPIIdentity(identity SubjectAPIIdentity) SubjectAPIIdentity {
	identity.ApplicationKey = strings.TrimSpace(identity.ApplicationKey)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.ServiceResource = strings.TrimSpace(identity.ServiceResource)
	identity.Domain = NormalizeRBACDomain(identity.Domain)
	return identity
}

// normalizedSubjectAPIPolicy normalizes the aggregate identity before any
// comparison or compilation. Endpoint arrays remain in the repository's order.
func normalizedSubjectAPIPolicy(policy SubjectAPIPolicy) SubjectAPIPolicy {
	policy.SubjectAPIIdentity = normalizedSubjectAPIIdentity(policy.SubjectAPIIdentity)
	return policy
}

// subjectAPIPolicyScope derives the local cache scope from an aggregate.
func subjectAPIPolicyScope(policy SubjectAPIPolicy) subjectAPICacheScope {
	policy = normalizedSubjectAPIPolicy(policy)
	return subjectAPICacheScope{
		applicationKey:  policy.ApplicationKey,
		applicationID:   policy.ApplicationID,
		subject:         policy.Subject,
		domain:          policy.Domain,
		serviceResource: policy.ServiceResource,
		serviceActive:   policy.ServiceActive,
	}
}

// subjectAPIPolicyKey hashes the normalized aggregate. Equal aggregates share
// one compiled index; any identity, grant, catalog, or role change yields a new
// key and therefore a fresh compile.
func subjectAPIPolicyKey(policy SubjectAPIPolicy) (subjectAPICacheKey, error) {
	raw, err := json.Marshal(normalizedSubjectAPIPolicy(policy))
	if err != nil {
		return subjectAPICacheKey{}, err
	}
	return sha256.Sum256(raw), nil
}

// subjectAPIPolicyIsEmpty reports the zero aggregate that a repository may
// return with a nil error. It must be rejected rather than treated as a
// legitimate policy with no grants.
func subjectAPIPolicyIsEmpty(policy SubjectAPIPolicy) bool {
	return policy.ApplicationKey == "" &&
		policy.ApplicationID == uuid.Nil &&
		policy.Subject == "" &&
		policy.Domain == "" &&
		policy.ServiceResource == "" &&
		!policy.ServiceActive &&
		len(policy.APIs) == 0 &&
		len(policy.Catalog) == 0 &&
		len(policy.RoleCodes) == 0
}

// cachedSubjectAPIPolicy pairs the content hash with its immutable compiled
// index so eviction and same-scope replacement can compare stored metadata.
type cachedSubjectAPIPolicy struct {
	key    subjectAPICacheKey
	scope  subjectAPICacheScope
	policy *compiledRBACPolicy
}

// subjectAPICache is a bounded LRU of immutable compiled aggregates. In-flight
// requests keep their own pointer even if an entry is evicted.
type subjectAPICache struct {
	mu       sync.Mutex
	entries  map[subjectAPICacheKey]*list.Element
	order    *list.List
	capacity int
	loads    singleflight.Group
}

// newSubjectAPICache creates a bounded cache. A non-positive capacity is
// treated as one entry.
func newSubjectAPICache(capacity int) *subjectAPICache {
	if capacity < 1 {
		capacity = 1
	}
	return &subjectAPICache{entries: make(map[subjectAPICacheKey]*list.Element), order: list.New(), capacity: capacity}
}

// get returns a cached compiled index and refreshes its LRU position.
func (c *subjectAPICache) get(key subjectAPICacheKey) *compiledRBACPolicy {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(cachedSubjectAPIPolicy).policy
	}
	return nil
}

// put installs a compiled index. Only the newest generation of a scope is
// retained, then the least recently used entry is evicted at capacity.
func (c *subjectAPICache) put(key subjectAPICacheKey, scope subjectAPICacheScope, policy *compiledRBACPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[key]; existing != nil {
		c.order.MoveToFront(existing)
		return
	}
	for prior, element := range c.entries {
		if element.Value.(cachedSubjectAPIPolicy).scope == scope {
			delete(c.entries, prior)
			c.order.Remove(element)
		}
	}
	c.entries[key] = c.order.PushFront(cachedSubjectAPIPolicy{key: key, scope: scope, policy: policy})
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		delete(c.entries, oldest.Value.(cachedSubjectAPIPolicy).key)
		c.order.Remove(oldest)
	}
}

// resolve returns the compiled index for an already-read aggregate. Concurrent
// compiles of the same hash are shared, but the repository read that produced
// the aggregate always happens in the caller before resolve is reached.
func (c *subjectAPICache) resolve(ctx context.Context, policy SubjectAPIPolicy) (*compiledRBACPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy = normalizedSubjectAPIPolicy(policy)
	key, err := subjectAPIPolicyKey(policy)
	if err != nil {
		return nil, err
	}
	if cached := c.get(key); cached != nil {
		return cached, nil
	}
	scope := subjectAPIPolicyScope(policy)
	flightKey := hex.EncodeToString(key[:])
	result := c.loads.DoChan(flightKey, func() (any, error) {
		if cached := c.get(key); cached != nil {
			return cached, nil
		}
		compiled := compileSubjectAPIPolicy(policy)
		c.put(key, scope, compiled)
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

// authorizeSubjectAPI authorizes one user's access to one service. It reads the
// aggregated Redis-backed policy on every request, validates that the returned
// identity is exactly the requested scope, and only then consults the local
// content-hash compiled LRU.
func (s *RBACV1Service) authorizeSubjectAPI(ctx context.Context, scope string, input map[string]any, repository SubjectAPIRepository) (RBACDecision, error) {
	app := normalizeRBACScope(scope)
	service, _ := input["service_resource"].(string)
	subject, _ := input["subject"].(string)
	domain, _ := input["domain"].(string)
	subject = strings.TrimSpace(subject)
	service = strings.TrimSpace(service)
	domain = NormalizeRBACDomain(domain)

	policy, err := repository.GetSubjectAPIs(ctx, app, subject, domain, service)
	if errors.Is(err, ErrSubjectAPIRecordNotFound) {
		// No service/user record exists: missing bindings are an ordinary deny.
		return RBACDecision{Effect: "deny", Reason: "endpoint_not_granted"}, nil
	}
	if err != nil {
		return RBACDecision{}, err
	}
	if err := ctx.Err(); err != nil {
		return RBACDecision{}, err
	}
	policy = normalizedSubjectAPIPolicy(policy)
	if subjectAPIPolicyIsEmpty(policy) {
		return RBACDecision{}, ErrNotFound
	}
	if (policy.ApplicationKey != "" && policy.ApplicationKey != app) ||
		policy.Subject != subject ||
		policy.Domain != domain ||
		policy.ServiceResource != service {
		return RBACDecision{}, ErrConflict
	}
	if !policy.ServiceActive {
		return RBACDecision{Effect: "deny", Reason: "service_inactive"}, nil
	}

	var compiled *compiledRBACPolicy
	if s.subjectAPIs == nil {
		compiled = compileSubjectAPIPolicy(policy)
	} else {
		compiled, err = s.subjectAPIs.resolve(ctx, policy)
		if err != nil {
			return RBACDecision{}, err
		}
	}
	return compiled.Authorize(subjectAPIInput(input, policy.SubjectAPIIdentity)), nil
}

// subjectAPIInput copies the caller's map so trimming and the aggregate-derived
// identity never mutate caller state. The compiled decision normalizes the
// domain itself.
func subjectAPIInput(input map[string]any, identity SubjectAPIIdentity) map[string]any {
	decoded := make(map[string]any, len(input))
	for key, value := range input {
		decoded[key] = value
	}
	decoded["subject"] = identity.Subject
	decoded["service_resource"] = identity.ServiceResource
	decoded["domain"] = identity.Domain
	return decoded
}

// compileSubjectAPIPolicy builds an immutable index for one already-aggregated
// user's service APIs. The full catalog is compiled so route priority,
// ambiguity and disabled endpoints behave exactly like the current-policy
// path; only endpoints present in APIs receive the aggregate grant. Matching
// the catalog rather than only the granted routes prevents a wide granted
// template from authorizing a more specific ungranted path.
func compileSubjectAPIPolicy(policy SubjectAPIPolicy) *compiledRBACPolicy {
	policy = normalizedSubjectAPIPolicy(policy)
	compiled := &compiledRBACPolicy{routes: make(map[policyRouteScope]*policyRoutes), principals: make(map[policySubject]*policyPrincipal)}
	granted := make(map[uuid.UUID]struct{}, len(policy.APIs))
	for _, api := range policy.APIs {
		granted[api.ID] = struct{}{}
	}
	// Role ordinal 0 is the single aggregate grant shared by every authorized
	// endpoint, so no role-per-API bitmap is materialized per user.
	aggregate := roleBitmap{words: []roleWord{{index: 0, bits: 1}}}
	for _, endpoint := range policy.Catalog {
		roles := roleBitmap{}
		if _, ok := granted[endpoint.ID]; ok {
			roles = aggregate
		}
		ordinal := len(compiled.endpoints)
		compiled.endpoints = append(compiled.endpoints, policyEndpoint{id: endpoint.ID.String(), enabled: endpoint.Enabled, roles: roles})
		routeScope := policyRouteScope{policy.ServiceResource, policyMethod(endpoint.Method)}
		routes := compiled.routes[routeScope]
		if routes == nil {
			routes = &policyRoutes{exact: make(map[string]routeMatch)}
			compiled.routes[routeScope] = routes
		}
		score := 0
		for _, part := range strings.Split(endpoint.PathTemplate, "/") {
			if !(strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
				score++
			}
		}
		routes.add(endpoint.PathTemplate, routeMatch{endpoint: ordinal, score: score, found: true}, compiled.endpoints)
	}
	compiled.principals[policySubject{policy.Subject, NormalizeRBACDomain(policy.Domain)}] = &policyPrincipal{
		roles: aggregate,
		names: normalizedSubjectAPIRoleCodes(policy.RoleCodes),
	}
	return compiled
}

// normalizedSubjectAPIRoleCodes deduplicates the role union while preserving
// the repository's display order.
func normalizedSubjectAPIRoleCodes(roleCodes []string) []string {
	if len(roleCodes) == 0 {
		return nil
	}
	result := make([]string, 0, len(roleCodes))
	seen := make(map[string]struct{}, len(roleCodes))
	for _, code := range roleCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result
}
