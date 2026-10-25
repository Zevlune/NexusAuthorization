package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/redis/go-redis/v9"
)

const (
	maxSubjectAPIBytes  = 16 * 1024 * 1024
	subjectAPIScanBatch = 1000
)

type subjectAPIRecord struct {
	key string
	raw []byte
}

// normalizeSubjectAPIIdentity keeps the service, subject and domain that form
// the Redis identity. Application metadata is deliberately discarded.
func normalizeSubjectAPIIdentity(identity biz.SubjectAPIIdentity) biz.SubjectAPIIdentity {
	identity.ApplicationKey = ""
	identity.ApplicationID = uuid.Nil
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Domain = biz.NormalizeRBACDomain(identity.Domain)
	identity.ServiceResource = strings.TrimSpace(identity.ServiceResource)
	return identity
}

func validateSubjectAPIIdentity(identity biz.SubjectAPIIdentity) error {
	identity = normalizeSubjectAPIIdentity(identity)
	if identity.ServiceResource == "" || identity.Subject == "" {
		return fmt.Errorf("invalid subject api identity")
	}
	return nil
}

func subjectAPIIdentityMatches(request, stored biz.SubjectAPIIdentity) bool {
	request = normalizeSubjectAPIIdentity(request)
	return request.ServiceResource == stored.ServiceResource &&
		request.Subject == stored.Subject &&
		request.Domain == biz.NormalizeRBACDomain(stored.Domain)
}

func policyHasApplicationMetadata(policy biz.SubjectAPIPolicy) bool {
	return policy.ApplicationKey != "" || policy.ApplicationID != uuid.Nil
}

// SubjectAPIKey returns the complete Redis key for one service, subject and
// normalized domain. The environment prefix is preserved verbatim.
func SubjectAPIKey(prefix string, identity biz.SubjectAPIIdentity) string {
	identity = normalizeSubjectAPIIdentity(identity)
	key := fmt.Sprintf("%s:%s:%s", prefix,
		url.QueryEscape(identity.ServiceResource),
		url.QueryEscape(identity.Subject))
	if identity.Domain != "" {
		key += ":" + url.QueryEscape(identity.Domain)
	}
	return key
}

// LoadSubjectAPIs reads the complete policy as a Redis string. A missing key is
// a cache miss; malformed, oversized, wrong-type and legacy identity records
// are rejected without a database fallback.
func (c *PolicyCache) LoadSubjectAPIs(ctx context.Context, identity biz.SubjectAPIIdentity) (biz.SubjectAPIPolicy, error) {
	if err := ctx.Err(); err != nil {
		return biz.SubjectAPIPolicy{}, err
	}
	if err := validateSubjectAPIIdentity(identity); err != nil {
		return biz.SubjectAPIPolicy{}, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	raw, err := c.client.Get(requestCtx, SubjectAPIKey(c.prefix, identity)).Bytes()
	if errors.Is(err, redis.Nil) {
		return biz.SubjectAPIPolicy{}, biz.ErrSubjectAPIRecordNotFound
	}
	if err != nil {
		return biz.SubjectAPIPolicy{}, err
	}
	if len(raw) > maxSubjectAPIBytes {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("oversized cached subject api payload")
	}

	var policy biz.SubjectAPIPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("invalid cached subject api JSON: %w", err)
	}
	if policyHasApplicationMetadata(policy) {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("cached subject api policy contains application metadata")
	}
	if err := validateSubjectAPIIdentity(policy.SubjectAPIIdentity); err != nil {
		return biz.SubjectAPIPolicy{}, err
	}
	if !subjectAPIIdentityMatches(identity, policy.SubjectAPIIdentity) {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("cached subject api identity mismatch")
	}
	return policy, nil
}

// StoreSubjectAPIs writes one record using the same string representation as a
// replace. Production publication uses ReplaceSubjectAPIs.
func (c *PolicyCache) StoreSubjectAPIs(ctx context.Context, policy biz.SubjectAPIPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := prepareSubjectAPIRecord(c.prefix, policy)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Set(requestCtx, record.key, record.raw, 0).Err()
}

// ReplaceSubjectAPIs makes Redis match the supplied globally aggregated
// policies for this environment. The input is fully validated before Redis is
// touched. SCAN only considers canonical service/subject/domain keys below the
// configured prefix; other key families are left untouched.
func (c *PolicyCache) ReplaceSubjectAPIs(ctx context.Context, policies []biz.SubjectAPIPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	records, err := prepareSubjectAPIRecords(c.prefix, policies)
	if err != nil {
		return err
	}

	existing, err := c.scanSubjectAPIKeys(ctx)
	if err != nil {
		return err
	}
	desired := make(map[string]subjectAPIRecord, len(records))
	for _, record := range records {
		desired[record.key] = record
	}

	for _, key := range existing {
		if _, keep := desired[key]; keep {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		err := c.client.Del(requestCtx, key).Err()
		cancel()
		if err != nil {
			return fmt.Errorf("delete subject api record %q: %w", key, err)
		}
	}

	for _, record := range records {
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		err := c.client.Set(requestCtx, record.key, record.raw, 0).Err()
		cancel()
		if err != nil {
			return fmt.Errorf("write subject api record %q: %w", record.key, err)
		}
	}
	return nil
}

func prepareSubjectAPIRecords(prefix string, policies []biz.SubjectAPIPolicy) ([]subjectAPIRecord, error) {
	records := make([]subjectAPIRecord, 0, len(policies))
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		record, err := prepareSubjectAPIRecord(prefix, policy)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[record.key]; duplicate {
			return nil, fmt.Errorf("duplicate subject api policy key %q", record.key)
		}
		seen[record.key] = struct{}{}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].key < records[j].key })
	return records, nil
}

func prepareSubjectAPIRecord(prefix string, policy biz.SubjectAPIPolicy) (subjectAPIRecord, error) {
	if err := validateSubjectAPIIdentity(policy.SubjectAPIIdentity); err != nil {
		return subjectAPIRecord{}, err
	}
	policy.SubjectAPIIdentity = normalizeSubjectAPIIdentity(policy.SubjectAPIIdentity)
	raw, err := json.Marshal(policy)
	if err != nil {
		return subjectAPIRecord{}, fmt.Errorf("marshal subject api policy: %w", err)
	}
	if len(raw) > maxSubjectAPIBytes {
		return subjectAPIRecord{}, fmt.Errorf("subject api record exceeds Redis cache size limit")
	}
	return subjectAPIRecord{key: SubjectAPIKey(prefix, policy.SubjectAPIIdentity), raw: raw}, nil
}

func (c *PolicyCache) scanSubjectAPIKeys(ctx context.Context) ([]string, error) {
	match := escapeRedisGlob(c.prefix) + ":*"
	seen := make(map[string]struct{})
	var cursor uint64
	for {
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		keys, next, err := c.client.Scan(requestCtx, cursor, match, subjectAPIScanBatch).Result()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("scan subject api keys: %w", err)
		}
		for _, key := range keys {
			if subjectAPIKeyCanonical(c.prefix, key) {
				seen[key] = struct{}{}
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func escapeRedisGlob(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, char := range value {
		switch char {
		case '\\', '*', '?', '[', ']':
			result.WriteByte('\\')
		}
		result.WriteRune(char)
	}
	return result.String()
}

func subjectAPIKeyCanonical(prefix, key string) bool {
	keyPrefix := prefix + ":"
	if !strings.HasPrefix(key, keyPrefix) {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(key, keyPrefix), ":")
	if len(segments) != 2 && len(segments) != 3 {
		return false
	}
	for _, segment := range segments[:2] {
		value, ok := canonicalSubjectAPIKeySegment(segment)
		if !ok || strings.TrimSpace(value) != value {
			return false
		}
	}
	if len(segments) == 3 {
		domain, ok := canonicalSubjectAPIKeySegment(segments[2])
		if !ok || strings.TrimSpace(domain) != domain || biz.NormalizeRBACDomain(domain) != domain {
			return false
		}
	}
	return true
}

func canonicalSubjectAPIKeySegment(segment string) (string, bool) {
	if segment == "" {
		return "", false
	}
	value, err := url.QueryUnescape(segment)
	if err != nil || value == "" || url.QueryEscape(value) != segment {
		return "", false
	}
	return value, true
}
