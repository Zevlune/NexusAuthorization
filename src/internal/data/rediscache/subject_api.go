package rediscache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/redis/go-redis/v9"
)

// One user+service grant set is cached as a single Redis string holding the
// SubjectAPIPolicy JSON itself: there is no envelope, invalidation token, or
// checksum, so the value can be replaced in place and is read back verbatim.
// Stored values never expire; freshness is controlled by the per-application
// state gate instead of a TTL.
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
local payload = redis.call('GET', KEYS[2])
if not payload then
  return {2, ''}
end
return {1, payload}
`

// storeSubjectAPIScript writes the record and its membership index together so
// a replace always finds every key that StoreSubjectAPIs has written. The set
// write has no expiry.
const storeSubjectAPIScript = `
redis.call('SET', KEYS[1], ARGV[1])
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

// validateSubjectAPIIdentity requires the fields that make up the key. The
// application id and active flag are not part of the key and may be omitted.
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

// SubjectAPIKey names one user+service grant set. The application key replaces
// the application id as the scope selector; QueryEscape keeps the application
// key, user, service, and domain text from colliding with the structural
// separators. An empty domain keeps the literal empty tail.
func SubjectAPIKey(prefix string, identity biz.SubjectAPIIdentity) string {
	domain := biz.NormalizeRBACDomain(identity.Domain)
	return fmt.Sprintf("%s:rbac:api:app:%s:user:%s:service:%s:domain:%s",
		prefix, url.QueryEscape(strings.TrimSpace(identity.ApplicationKey)),
		url.QueryEscape(identity.Subject), url.QueryEscape(identity.ServiceResource), url.QueryEscape(domain))
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
		subjectAPIStateReady).Result()
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
	case 0, 2:
		// 0: the application has never been published. 2: the gate is ready but
		// this identity has no record. Both are ordinary misses.
		return biz.SubjectAPIPolicy{}, biz.ErrNotFound
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

// StoreSubjectAPIs writes one plain JSON record for a user+service scope and
// indexes its key in the application's membership set. It is used for external
// single-record updates and as part of a replace, so it never touches the
// application write gate: that state is owned by the batch writer. The record
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
		[]string{SubjectAPIKey(c.prefix, policy.SubjectAPIIdentity), membersKey}, raw).Err()
}

// BlockSubjectAPIApplications marks every application as "updating" so reads
// are refused while a batch replace rewrites its records. The gate is written
// with a pipeline and never expires.
func (c *PolicyCache) BlockSubjectAPIApplications(ctx context.Context, appKeys []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	keys := make([]string, 0, len(appKeys))
	seen := make(map[string]struct{}, len(appKeys))
	for _, appKey := range appKeys {
		appKey = strings.TrimSpace(appKey)
		if appKey == "" {
			return fmt.Errorf("invalid subject api application key")
		}
		if _, ok := seen[appKey]; ok {
			continue
		}
		seen[appKey] = struct{}{}
		keys = append(keys, appKey)
	}
	if len(keys) == 0 {
		return nil
	}
	operationCtx, cancel := context.WithTimeout(ctx, subjectAPIReplaceDeadline)
	defer cancel()
	for start := 0; start < len(keys); start += subjectAPIReplaceBatch {
		end := min(start+subjectAPIReplaceBatch, len(keys))
		requestCtx, requestCancel := context.WithTimeout(operationCtx, c.timeout)
		pipe := c.client.Pipeline()
		for _, appKey := range keys[start:end] {
			pipe.Set(requestCtx, subjectAPIStateKey(c.prefix, appKey), subjectAPIStateUpdating, 0)
		}
		_, err := pipe.Exec(requestCtx)
		requestCancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// ReplaceSubjectAPIApplication rewrites every record for one application under
// the "updating" gate. The caller marks the application updating first (with
// BlockSubjectAPIApplications); this method refuses to run otherwise. It reads
// the old membership index, deletes the superseded records, writes the new ones
// with their index entries, and only then marks the gate ready. Any failure
// leaves the application gated so the next attempt can retry from a clean
// state. The records are written in batches, each bounded by the configured
// timeout, and the whole operation is bounded by a 30 second deadline.
func (c *PolicyCache) ReplaceSubjectAPIApplication(ctx context.Context, appKey string, policies []biz.SubjectAPIPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	appKey = strings.TrimSpace(appKey)
	if appKey == "" {
		return fmt.Errorf("invalid subject api application key")
	}
	operationCtx, cancel := context.WithTimeout(ctx, subjectAPIReplaceDeadline)
	defer cancel()

	stateKey := subjectAPIStateKey(c.prefix, appKey)
	membersKey := subjectAPIMembersKey(c.prefix, appKey)

	// Gate: the application must be marked updating by the batch writer.
	requestCtx, requestCancel := context.WithTimeout(operationCtx, c.timeout)
	state, err := c.client.Get(requestCtx, stateKey).Result()
	requestCancel()
	if err == redis.Nil {
		return fmt.Errorf("subject api application %q is not gated for replacement", appKey)
	}
	if err != nil {
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
	if err := c.runSubjectAPIBatches(operationCtx, len(removed), func(pipe redis.Pipeliner, requestCtx context.Context, i int) {
		pipe.Del(requestCtx, removed[i])
		pipe.SRem(requestCtx, membersKey, removed[i])
	}); err != nil {
		return err
	}
	if err := c.runSubjectAPIBatches(operationCtx, len(records), func(pipe redis.Pipeliner, requestCtx context.Context, i int) {
		pipe.Set(requestCtx, records[i].key, records[i].raw, 0)
		pipe.SAdd(requestCtx, membersKey, records[i].key)
	}); err != nil {
		return err
	}

	// Open the gate only after every record is durable.
	requestCtx, requestCancel = context.WithTimeout(operationCtx, c.timeout)
	err = c.client.Set(requestCtx, stateKey, subjectAPIStateReady, 0).Err()
	requestCancel()
	return err
}

// runSubjectAPIBatches runs count entries in batches, giving each batch its own
// timeout while the caller's deadline still bounds the whole operation.
func (c *PolicyCache) runSubjectAPIBatches(ctx context.Context, count int, build func(pipe redis.Pipeliner, requestCtx context.Context, i int)) error {
	for start := 0; start < count; start += subjectAPIReplaceBatch {
		end := min(start+subjectAPIReplaceBatch, count)
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		pipe := c.client.Pipeline()
		for i := start; i < end; i++ {
			build(pipe, requestCtx, i)
		}
		_, err := pipe.Exec(requestCtx)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
