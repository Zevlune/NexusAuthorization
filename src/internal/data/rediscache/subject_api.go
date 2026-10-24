package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/redis/go-redis/v9"
)

// Each service+user scope is a Redis hash. Application keys are hash fields,
// each holding its SubjectAPIPolicy JSON without an envelope or checksum.
// Shared services therefore group users together without merging applications'
// grants. Hashes never expire; freshness is controlled by application gates.
const maxSubjectAPIBytes = 16 * 1024 * 1024

// Per-application write gate values. "updating" blocks every read so a batch
// replace can rewrite one application's records without ever exposing a
// partially written set.
const (
	subjectAPIStateReady    = "ready"
	subjectAPIStateUpdating = "updating"
)

const (
	// ReplaceSubjectAPIApplication must finish (or fail) within this deadline.
	subjectAPIReplaceDeadline = 30 * time.Second
	// Commands are grouped so one large application does not build an unbounded
	// pipeline while still amortizing round trips.
	subjectAPIReplaceBatch = 64
)

// loadSubjectAPIScript reads the write gate and the record in one atomic step,
// so a read that races a replace either sees the value behind the old gate or
// the new gate, never a half-written set.
//
// Status codes: 1 = ready with a value, 2 = ready but no value for this
// identity, 0 = no gate (application never published), -1 = gate is updating.
const loadSubjectAPIScript = `
local state = redis.call('GET', KEYS[1])
if not state then
  return {0, ''}
end
if state ~= ARGV[1] then
  return {-1, ''}
end
local payload = redis.call('HGET', KEYS[2], ARGV[2])
if not payload then
  return {2, ''}
end
return {1, payload}
`

// storeSubjectAPIScript writes the record and its membership index together so
// a replace always finds every key that StoreSubjectAPIs has written. The set
// write has no expiry.
const storeSubjectAPIScript = `
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('SADD', KEYS[2], KEYS[1])
return 1
`

type subjectAPIRecord struct {
	key string
	raw []byte
}

// normalizeSubjectAPIIdentity folds the retired "global" domain alias onto the
// default empty scope and trims the application key so both spellings address
// the same cached value.
func normalizeSubjectAPIIdentity(identity biz.SubjectAPIIdentity) biz.SubjectAPIIdentity {
	identity.Domain = biz.NormalizeRBACDomain(identity.Domain)
	identity.ApplicationKey = strings.TrimSpace(identity.ApplicationKey)
	return identity
}

// validateSubjectAPIIdentity requires the hash identity and application field.
// The application id and active flag are metadata and may be omitted.
func validateSubjectAPIIdentity(identity biz.SubjectAPIIdentity) error {
	if strings.TrimSpace(identity.ApplicationKey) == "" || strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(identity.ServiceResource) == "" {
		return fmt.Errorf("invalid subject api identity")
	}
	return nil
}

// subjectAPIKeyIdentityMatches compares the identity stored in the payload with
// the requested one. The request may omit the application id, and the service
// active flag is ignored because it is not part of the key.
func subjectAPIKeyIdentityMatches(request, stored biz.SubjectAPIIdentity) bool {
	request = normalizeSubjectAPIIdentity(request)
	stored = normalizeSubjectAPIIdentity(stored)
	if request.ApplicationKey != stored.ApplicationKey || request.Subject != stored.Subject ||
		request.ServiceResource != stored.ServiceResource || request.Domain != stored.Domain {
		return false
	}
	if request.ApplicationID != uuid.Nil && request.ApplicationID != stored.ApplicationID {
		return false
	}
	return true
}

// SubjectAPIKey groups the environment's users under their service resource.
// Application isolation lives in hash fields rather than the Redis key.
// QueryEscape protects separators; only named domains add a final segment.
func SubjectAPIKey(prefix string, identity biz.SubjectAPIIdentity) string {
	key := fmt.Sprintf("%s:%s:%s", prefix,
		url.QueryEscape(strings.TrimSpace(identity.ServiceResource)),
		url.QueryEscape(strings.TrimSpace(identity.Subject)))
	domain := biz.NormalizeRBACDomain(identity.Domain)
	if domain != "" {
		key += ":" + url.QueryEscape(domain)
	}
	return key
}

// subjectAPIStateKey holds the per-application write gate ("ready"/"updating").
func subjectAPIStateKey(prefix, appKey string) string {
	return fmt.Sprintf("%s:rbac:api:app:%s:state", prefix, url.QueryEscape(strings.TrimSpace(appKey)))
}

// subjectAPIMembersKey is the set of record keys that belong to one
// application, so a replace can find and delete the records it supersedes.
func subjectAPIMembersKey(prefix, appKey string) string {
	return fmt.Sprintf("%s:rbac:api:app:%s:members", prefix, url.QueryEscape(strings.TrimSpace(appKey)))
}

// dedupeSubjectAPIEndpoints collapses the union of several roles' grants keyed
// by endpoint identity while preserving first-seen order. Endpoints without an
// identity are kept as-is rather than merged by accident.
func dedupeSubjectAPIEndpoints(endpoints []biz.SubjectAPIEndpoint) []biz.SubjectAPIEndpoint {
	if len(endpoints) < 2 {
		return endpoints
	}
	seen := make(map[uuid.UUID]struct{}, len(endpoints))
	result := make([]biz.SubjectAPIEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.ID != uuid.Nil {
			if _, ok := seen[endpoint.ID]; ok {
				continue
			}
			seen[endpoint.ID] = struct{}{}
		}
		result = append(result, endpoint)
	}
	return result
}

// LoadSubjectAPIs returns the stored grant set for one user+service scope. A
// Lua script checks the application's write gate and reads the record in one
// atomic step, so a replace that is still running can never expose a partial
// set. There is no database fallback: a missing gate, a gated application, or a
// missing record is reported to the caller.
func (c *PolicyCache) LoadSubjectAPIs(ctx context.Context, identity biz.SubjectAPIIdentity) (biz.SubjectAPIPolicy, error) {
	if err := validateSubjectAPIIdentity(identity); err != nil {
		return biz.SubjectAPIPolicy{}, err
	}
	identity = normalizeSubjectAPIIdentity(identity)
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	result, err := c.client.Eval(requestCtx, loadSubjectAPIScript,
		[]string{subjectAPIStateKey(c.prefix, identity.ApplicationKey), SubjectAPIKey(c.prefix, identity)},
		subjectAPIStateReady, identity.ApplicationKey).Result()
	if err != nil {
		return biz.SubjectAPIPolicy{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("invalid cached subject api result")
	}
	status, ok := values[0].(int64)
	if !ok {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("invalid cached subject api status")
	}
	switch status {
	case 0:
		// The application has never been published (or is unknown).
		return biz.SubjectAPIPolicy{}, biz.ErrNotFound
	case 2:
		// The gate is ready but this subject has no record for the service: no
		// role binding, or the service is not linked to the application.
		return biz.SubjectAPIPolicy{}, biz.ErrSubjectAPIRecordNotFound
	case -1:
		// The application is mid-replace; refuse the read instead of serving a
		// stale or partial set.
		return biz.SubjectAPIPolicy{}, fmt.Errorf("subject api application is updating")
	}
	payload, ok := values[1].(string)
	if !ok || payload == "" {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("invalid cached subject api payload")
	}
	if len(payload) > maxSubjectAPIBytes {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("oversized cached subject api payload")
	}
	var policy biz.SubjectAPIPolicy
	if err := json.Unmarshal([]byte(payload), &policy); err != nil {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("invalid cached subject api JSON")
	}
	if !subjectAPIKeyIdentityMatches(identity, policy.SubjectAPIIdentity) {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("cached subject api identity mismatch")
	}
	return policy, nil
}

// StoreSubjectAPIs writes plain JSON to the application's field in a
// user+service hash and indexes its key in the application's membership set.
// It is used for external single-record updates and as part of a replace,
// so it never touches the application write gate, owned by the batch writer. The record
// and the index entry are written atomically and never expire.
func (c *PolicyCache) StoreSubjectAPIs(ctx context.Context, policy biz.SubjectAPIPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSubjectAPIIdentity(policy.SubjectAPIIdentity); err != nil {
		return err
	}
	policy.SubjectAPIIdentity = normalizeSubjectAPIIdentity(policy.SubjectAPIIdentity)
	policy.APIs = dedupeSubjectAPIEndpoints(policy.APIs)
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if len(raw) > maxSubjectAPIBytes {
		return fmt.Errorf("subject api records exceed Redis cache size limit")
	}
	membersKey := subjectAPIMembersKey(c.prefix, policy.SubjectAPIIdentity.ApplicationKey)
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Eval(requestCtx, storeSubjectAPIScript,
		[]string{SubjectAPIKey(c.prefix, policy.SubjectAPIIdentity), membersKey}, policy.ApplicationKey, raw).Err()
}

// Rebuild lease. Each application has a lock key holding a random token; every
// rebuild write runs as a Lua script that first checks the token, so a holder
// whose lease expired can never overwrite a newer rebuild. The request counter
// is incremented after each committed save; a holder reads it after acquiring
// the lease and before taking its database snapshot, and records it as
// "published" once the rebuild is visible. Saves that committed earlier are
// therefore always covered, and a reconciler can detect applications whose
// published value fell behind.

const (
	// The lease outlives a full rebuild (bounded by subjectAPIReplaceDeadline
	// plus the database read), but frees the application if a holder dies.
	subjectAPILeaseTTL      = 2 * time.Minute
	subjectAPILeaseMinRetry = 20 * time.Millisecond
	subjectAPILeaseMaxRetry = 250 * time.Millisecond
	subjectAPILeaseLostCode = "SUBJECT_API_LEASE_LOST"
)

// ErrSubjectAPILeaseLost reports a rebuild whose lease expired or was taken over.
var ErrSubjectAPILeaseLost = errors.New("subject api rebuild lease lost")

const releaseSubjectAPILeaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

// KEYS[1]=lock KEYS[2]=state; ARGV[1]=token ARGV[2]=state value.
const fencedSubjectAPIStateScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return redis.error_reply('` + subjectAPILeaseLostCode + `')
end
redis.call('SET', KEYS[2], ARGV[2])
return 1
`

// KEYS[1]=lock KEYS[2]=members KEYS[3..]=hashes;
// ARGV[1]=token ARGV[2]=application field ARGV[3..]=JSON values.
const fencedSubjectAPIWriteScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return redis.error_reply('` + subjectAPILeaseLostCode + `')
end
for i = 3, #KEYS do
  redis.call('HSET', KEYS[i], ARGV[2], ARGV[i])
  redis.call('SADD', KEYS[2], KEYS[i])
end
return 1
`

// KEYS[1]=lock KEYS[2]=members KEYS[3..]=hashes;
// ARGV[1]=token ARGV[2]=application field. Other applications remain intact.
const fencedSubjectAPIDeleteScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return redis.error_reply('` + subjectAPILeaseLostCode + `')
end
for i = 3, #KEYS do
  redis.call('HDEL', KEYS[i], ARGV[2])
  redis.call('SREM', KEYS[2], KEYS[i])
end
return 1
`

// KEYS[1]=lock KEYS[2]=state KEYS[3]=published; ARGV[1]=token ARGV[2]=horizon.
const fencedSubjectAPIPublishScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return redis.error_reply('` + subjectAPILeaseLostCode + `')
end
redis.call('SET', KEYS[2], '` + subjectAPIStateReady + `')
local current = tonumber(redis.call('GET', KEYS[3]) or '0')
if tonumber(ARGV[2]) > current then
  redis.call('SET', KEYS[3], ARGV[2])
end
return 1
`

func subjectAPILockKey(prefix, appKey string) string {
	return fmt.Sprintf("%s:rbac:api:app:%s:lock", prefix, url.QueryEscape(strings.TrimSpace(appKey)))
}

func subjectAPIRequestedKey(prefix, appKey string) string {
	return fmt.Sprintf("%s:rbac:api:app:%s:requested", prefix, url.QueryEscape(strings.TrimSpace(appKey)))
}

func subjectAPIPublishedKey(prefix, appKey string) string {
	return fmt.Sprintf("%s:rbac:api:app:%s:published", prefix, url.QueryEscape(strings.TrimSpace(appKey)))
}

// normalizeSubjectAPIApplications trims, deduplicates and sorts application
// keys. Sorting gives every caller the same lease order, so two rebuilds of
// overlapping application sets cannot deadlock.
func normalizeSubjectAPIApplications(appKeys []string) ([]string, error) {
	seen := make(map[string]struct{}, len(appKeys))
	keys := make([]string, 0, len(appKeys))
	for _, appKey := range appKeys {
		appKey = strings.TrimSpace(appKey)
		if appKey == "" {
			return nil, fmt.Errorf("invalid subject api application key")
		}
		if _, ok := seen[appKey]; ok {
			continue
		}
		seen[appKey] = struct{}{}
		keys = append(keys, appKey)
	}
	sort.Strings(keys)
	return keys, nil
}

func subjectAPILeaseError(err error) error {
	if err != nil && strings.Contains(err.Error(), subjectAPILeaseLostCode) {
		return ErrSubjectAPILeaseLost
	}
	return err
}

// RequestSubjectAPIRefresh records that the given applications changed and
// returns each application's request number. Call it after the database commit.
func (c *PolicyCache) RequestSubjectAPIRefresh(ctx context.Context, appKeys []string) (map[string]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys, err := normalizeSubjectAPIApplications(appKeys)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	pipe := c.client.Pipeline()
	commands := make([]*redis.IntCmd, len(keys))
	for i, appKey := range keys {
		commands[i] = pipe.Incr(requestCtx, subjectAPIRequestedKey(c.prefix, appKey))
	}
	if _, err := pipe.Exec(requestCtx); err != nil {
		return nil, err
	}
	tickets := make(map[string]int64, len(keys))
	for i, appKey := range keys {
		tickets[appKey] = commands[i].Val()
	}
	return tickets, nil
}

// SubjectAPIStatuses reads the gate and request counters of the given
// applications. Missing keys read as an empty state and zero counters.
func (c *PolicyCache) SubjectAPIStatuses(ctx context.Context, appKeys []string) (map[string]biz.SubjectAPIPublication, error) {
	keys, err := normalizeSubjectAPIApplications(appKeys)
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]biz.SubjectAPIPublication, len(keys))
	for start := 0; start < len(keys); start += subjectAPIReplaceBatch {
		end := min(start+subjectAPIReplaceBatch, len(keys))
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		pipe := c.client.Pipeline()
		commands := make([][3]*redis.StringCmd, 0, end-start)
		for _, appKey := range keys[start:end] {
			commands = append(commands, [3]*redis.StringCmd{
				pipe.Get(requestCtx, subjectAPIStateKey(c.prefix, appKey)),
				pipe.Get(requestCtx, subjectAPIRequestedKey(c.prefix, appKey)),
				pipe.Get(requestCtx, subjectAPIPublishedKey(c.prefix, appKey)),
			})
		}
		_, err := pipe.Exec(requestCtx)
		cancel()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		for i, appKey := range keys[start:end] {
			var status biz.SubjectAPIPublication
			status.State, _ = commands[i][0].Result()
			status.Requested, _ = commands[i][1].Int64()
			status.Published, _ = commands[i][2].Int64()
			statuses[appKey] = status
		}
	}
	return statuses, nil
}

// subjectAPILease implements biz.SubjectAPILease on top of PolicyCache.
type subjectAPILease struct {
	cache *PolicyCache
	token string
	// apps holds the leased applications; horizon holds each request counter
	// read right after the lease was acquired.
	apps    []string
	horizon map[string]int64
}

// AcquireSubjectAPILease waits until it holds the rebuild lease of every given
// application, in sorted order, or the context ends.
func (c *PolicyCache) AcquireSubjectAPILease(ctx context.Context, appKeys []string) (biz.SubjectAPILease, error) {
	keys, err := normalizeSubjectAPIApplications(appKeys)
	if err != nil {
		return nil, err
	}
	lease := &subjectAPILease{cache: c, token: uuid.NewString(), horizon: make(map[string]int64, len(keys))}
	for _, appKey := range keys {
		if err := lease.acquire(ctx, appKey); err != nil {
			lease.Release(ctx)
			return nil, err
		}
		lease.apps = append(lease.apps, appKey)
	}
	// Read the request counters only after every lease is held: each save that
	// committed before this point has already incremented its counter, and the
	// caller's database snapshot is taken afterwards, so it includes them.
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	pipe := c.client.Pipeline()
	commands := make([]*redis.StringCmd, len(keys))
	for i, appKey := range keys {
		commands[i] = pipe.Get(requestCtx, subjectAPIRequestedKey(c.prefix, appKey))
	}
	if _, err := pipe.Exec(requestCtx); err != nil && err != redis.Nil {
		lease.Release(ctx)
		return nil, err
	}
	for i, appKey := range keys {
		lease.horizon[appKey], _ = commands[i].Int64()
	}
	return lease, nil
}

func (l *subjectAPILease) acquire(ctx context.Context, appKey string) error {
	key := subjectAPILockKey(l.cache.prefix, appKey)
	delay := subjectAPILeaseMinRetry
	for {
		requestCtx, cancel := context.WithTimeout(ctx, l.cache.timeout)
		acquired, err := l.cache.client.SetNX(requestCtx, key, l.token, subjectAPILeaseTTL).Result()
		cancel()
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for subject api rebuild lease of %q: %w", appKey, ctx.Err())
		case <-timer.C:
		}
		delay = min(delay*2, subjectAPILeaseMaxRetry)
	}
}

func (l *subjectAPILease) holds(appKey string) bool {
	_, ok := l.horizon[appKey]
	return ok
}

// Pending returns the leased applications whose gate is not ready or whose
// published request number is below the ticket.
func (l *subjectAPILease) Pending(ctx context.Context, tickets map[string]int64) ([]string, error) {
	statuses, err := l.cache.SubjectAPIStatuses(ctx, l.apps)
	if err != nil {
		return nil, err
	}
	pending := make([]string, 0, len(l.apps))
	for _, appKey := range l.apps {
		status := statuses[appKey]
		ticket, requested := tickets[appKey]
		if status.State != subjectAPIStateReady || (requested && status.Published < ticket) {
			pending = append(pending, appKey)
		}
	}
	return pending, nil
}

// Block closes the read gate of the given leased applications so a replace
// never exposes a partially written record set.
func (l *subjectAPILease) Block(ctx context.Context, appKeys []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	operationCtx, cancel := context.WithTimeout(ctx, subjectAPIReplaceDeadline)
	defer cancel()
	for _, appKey := range appKeys {
		appKey = strings.TrimSpace(appKey)
		if !l.holds(appKey) {
			return fmt.Errorf("subject api application %q is not leased", appKey)
		}
		requestCtx, requestCancel := context.WithTimeout(operationCtx, l.cache.timeout)
		err := l.cache.client.Eval(requestCtx, fencedSubjectAPIStateScript,
			[]string{subjectAPILockKey(l.cache.prefix, appKey), subjectAPIStateKey(l.cache.prefix, appKey)},
			l.token, subjectAPIStateUpdating).Err()
		requestCancel()
		if err != nil {
			return subjectAPILeaseError(err)
		}
	}
	return nil
}

// Replace rewrites one leased application's hash fields under its "updating"
// gate. It removes superseded fields without touching other applications,
// writes the new fields and membership index, and only then publishes ready.
// Any failure leaves the gate closed for the next rebuild to retry.
func (l *subjectAPILease) Replace(ctx context.Context, appKey string, policies []biz.SubjectAPIPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	appKey = strings.TrimSpace(appKey)
	if appKey == "" {
		return fmt.Errorf("invalid subject api application key")
	}
	if !l.holds(appKey) {
		return fmt.Errorf("subject api application %q is not leased", appKey)
	}
	c := l.cache
	operationCtx, cancel := context.WithTimeout(ctx, subjectAPIReplaceDeadline)
	defer cancel()

	lockKey := subjectAPILockKey(c.prefix, appKey)
	stateKey := subjectAPIStateKey(c.prefix, appKey)
	membersKey := subjectAPIMembersKey(c.prefix, appKey)

	requestCtx, requestCancel := context.WithTimeout(operationCtx, c.timeout)
	state, err := c.client.Get(requestCtx, stateKey).Result()
	requestCancel()
	if err != nil && err != redis.Nil {
		return err
	}
	if state != subjectAPIStateUpdating {
		return fmt.Errorf("subject api application %q is not gated for replacement", appKey)
	}

	// Old keys come from the membership set written alongside every record.
	requestCtx, requestCancel = context.WithTimeout(operationCtx, c.timeout)
	oldKeys, err := c.client.SMembers(requestCtx, membersKey).Result()
	requestCancel()
	if err != nil {
		return err
	}

	index := make(map[string]int, len(policies))
	records := make([]subjectAPIRecord, 0, len(policies))
	for _, policy := range policies {
		if err := validateSubjectAPIIdentity(policy.SubjectAPIIdentity); err != nil {
			return err
		}
		policy.SubjectAPIIdentity = normalizeSubjectAPIIdentity(policy.SubjectAPIIdentity)
		if policy.SubjectAPIIdentity.ApplicationKey != appKey {
			return fmt.Errorf("subject api policy application key mismatch")
		}
		policy.APIs = dedupeSubjectAPIEndpoints(policy.APIs)
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if len(raw) > maxSubjectAPIBytes {
			return fmt.Errorf("subject api records exceed Redis cache size limit")
		}
		key := SubjectAPIKey(c.prefix, policy.SubjectAPIIdentity)
		if position, ok := index[key]; ok {
			records[position].raw = raw
			continue
		}
		index[key] = len(records)
		records = append(records, subjectAPIRecord{key: key, raw: raw})
	}

	removed := make([]string, 0, len(oldKeys))
	for _, key := range oldKeys {
		if _, ok := index[key]; !ok {
			removed = append(removed, key)
		}
	}

	// Deletes first, so a later batch failure can never leave a departed user's
	// record addressable once the gate reopens.
	for start := 0; start < len(removed); start += subjectAPIReplaceBatch {
		batch := removed[start:min(start+subjectAPIReplaceBatch, len(removed))]
		if err := l.evalFenced(operationCtx, fencedSubjectAPIDeleteScript, append([]string{lockKey, membersKey}, batch...), appKey); err != nil {
			return err
		}
	}
	for start := 0; start < len(records); start += subjectAPIReplaceBatch {
		batch := records[start:min(start+subjectAPIReplaceBatch, len(records))]
		keys := make([]string, 0, len(batch)+2)
		values := make([]any, 0, len(batch)+1)
		values = append(values, appKey)
		keys = append(keys, lockKey, membersKey)
		for _, record := range batch {
			keys = append(keys, record.key)
			values = append(values, record.raw)
		}
		if err := l.evalFenced(operationCtx, fencedSubjectAPIWriteScript, keys, values...); err != nil {
			return err
		}
	}

	// Open the gate only after every record is durable.
	return l.evalFenced(operationCtx, fencedSubjectAPIPublishScript,
		[]string{lockKey, stateKey, subjectAPIPublishedKey(c.prefix, appKey)}, l.horizon[appKey])
}

func (l *subjectAPILease) evalFenced(ctx context.Context, script string, keys []string, values ...any) error {
	requestCtx, cancel := context.WithTimeout(ctx, l.cache.timeout)
	defer cancel()
	args := append([]any{l.token}, values...)
	return subjectAPILeaseError(l.cache.client.Eval(requestCtx, script, keys, args...).Err())
}

// Release deletes every lock this lease still owns. It ignores the caller's
// cancellation so an aborted rebuild still frees the applications promptly.
func (l *subjectAPILease) Release(ctx context.Context) {
	for _, appKey := range l.apps {
		requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.cache.timeout)
		_ = l.cache.client.Eval(requestCtx, releaseSubjectAPILeaseScript, []string{subjectAPILockKey(l.cache.prefix, appKey)}, l.token).Err()
		cancel()
	}
	l.apps = nil
}
