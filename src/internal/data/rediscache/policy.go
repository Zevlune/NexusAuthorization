// Package rediscache caches current permission records using go-redis.
// PostgreSQL tokens ensure saves immediately invalidate stale cached records.
package rediscache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/redis/go-redis/v9"
)

const (
	CacheFormat = 1
	// Split bytes, not entities: even one enormous role or binding list is bounded.
	PolicyPartBytes        = 256 * 1024
	policyBatchParts       = 16
	maxPolicyBytes         = 512 * 1024 * 1024
	policyOperationTimeout = 30 * time.Second
	// Parts outlive the manifest, covering the bounded write and in-flight reads.
	policyPartGrace = time.Minute
)

type PolicyCache struct {
	client  *redis.Client
	prefix  string
	ttl     time.Duration
	timeout time.Duration
}

type policyEnvelope struct {
	Format        int       `json:"format"`
	ApplicationID uuid.UUID `json:"application_id"`
	Token         string    `json:"token"`
	Checksum      string    `json:"checksum"`
	Bytes         int       `json:"bytes"`
	Parts         int       `json:"parts"`
}

const partHeaderBytes = sha256.Size
const partPayloadBytes = PolicyPartBytes - partHeaderBytes

func New(cfg config.RedisConfig) (*PolicyCache, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	options, err := redis.ParseURL(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("redis.url must be a valid redis:// or rediss:// URL")
	}
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		return nil, err
	}
	ttl, err := cfg.TTLDuration()
	if err != nil {
		return nil, err
	}
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout = timeout, timeout, timeout
	options.PoolTimeout = timeout
	options.MaxRetries = -1
	options.ContextTimeoutEnabled = true
	return &PolicyCache{client: redis.NewClient(options), prefix: cfg.Prefix(), ttl: ttl, timeout: timeout}, nil
}

// Keys name current data only. Internal invalidation tokens never appear in keys.
func PolicyKey(prefix string, applicationID uuid.UUID) string {
	return fmt.Sprintf("%s:rbac:current:app:{%s}:manifest", prefix, applicationID)
}
func (c *PolicyCache) partKey(policy biz.CurrentPolicy, part int) string {
	return fmt.Sprintf("%s:rbac:current:app:{%s}:part:%d", c.prefix, policy.ApplicationID, part)
}
func validatePolicy(policy biz.CurrentPolicy) error {
	if policy.ApplicationID == uuid.Nil || policy.Token == "" || len(policy.Token) > 256 {
		return fmt.Errorf("invalid current policy identity")
	}
	return nil
}
func policyBytes(policy biz.CurrentPolicy) ([]byte, error) {
	if policy.Data == nil || policy.Data["application_id"] != policy.ApplicationID.String() {
		return nil, fmt.Errorf("policy application mismatch")
	}
	raw, err := json.Marshal(policy.Data)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPolicyBytes {
		return nil, fmt.Errorf("current records exceed Redis cache size limit")
	}
	return raw, nil
}
func validateChecksum(checksum string, raw []byte) error {
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != checksum {
		return fmt.Errorf("policy cache checksum mismatch")
	}
	return nil
}
func (c *PolicyCache) Load(ctx context.Context, policy biz.CurrentPolicy) (map[string]any, error) {
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, policyOperationTimeout)
	defer cancel()
	requestCtx, requestCancel := context.WithTimeout(ctx, c.timeout)
	data, err := c.client.Get(requestCtx, PolicyKey(c.prefix, policy.ApplicationID)).Bytes()
	requestCancel()
	if err == redis.Nil {
		return nil, biz.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 4096 {
		return nil, fmt.Errorf("oversized cached manifest")
	}
	var value policyEnvelope
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("invalid cached manifest JSON")
	}
	if value.Format != CacheFormat || value.ApplicationID != policy.ApplicationID || value.Token != policy.Token {
		return nil, fmt.Errorf("cached policy metadata mismatch")
	}
	if value.Bytes <= 0 || value.Bytes > maxPolicyBytes || value.Parts != (value.Bytes+partPayloadBytes-1)/partPayloadBytes {
		return nil, fmt.Errorf("invalid cached partition dimensions")
	}
	// Do not trust the manifest enough to preallocate hundreds of MB before reads.
	raw := make([]byte, 0)
	for start := 0; start < value.Parts; start += policyBatchParts {
		count := min(policyBatchParts, value.Parts-start)
		commands := make([]*redis.StringCmd, count)
		pipe := c.client.Pipeline()
		requestCtx, requestCancel := context.WithTimeout(ctx, c.timeout)
		for i := range commands {
			commands[i] = pipe.Get(requestCtx, c.partKey(policy, start+i))
		}
		_, err = pipe.Exec(requestCtx)
		requestCancel()
		if err == redis.Nil {
			return nil, biz.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		for i, command := range commands {
			part, err := command.Bytes()
			if err != nil {
				return nil, err
			}
			expected := min(partPayloadBytes, value.Bytes-(start+i)*partPayloadBytes)
			tokenHash := sha256.Sum256([]byte(policy.Token))
			if len(part) < partHeaderBytes || string(part[:partHeaderBytes]) != string(tokenHash[:]) {
				return nil, biz.ErrNotFound
			}
			part = part[partHeaderBytes:]
			if len(part) != expected {
				return nil, fmt.Errorf("cached partition size mismatch")
			}
			raw = append(raw, part...)
		}
	}
	if err := validateChecksum(value.Checksum, raw); err != nil {
		return nil, err
	}
	var records map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("invalid cached policy JSON")
	}
	if records["application_id"] != policy.ApplicationID.String() {
		return nil, fmt.Errorf("policy application identity mismatch")
	}
	return records, nil
}

func (c *PolicyCache) Store(ctx context.Context, policy biz.CurrentPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	raw, err := policyBytes(policy)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, policyOperationTimeout)
	defer cancel()
	parts := (len(raw) + partPayloadBytes - 1) / partPayloadBytes
	for start := 0; start < parts; start += policyBatchParts {
		pipe := c.client.Pipeline()
		requestCtx, requestCancel := context.WithTimeout(ctx, c.timeout)
		for part := start; part < min(start+policyBatchParts, parts); part++ {
			from := part * partPayloadBytes
			tokenHash := sha256.Sum256([]byte(policy.Token))
			payload := make([]byte, partHeaderBytes, PolicyPartBytes)
			copy(payload, tokenHash[:])
			payload = append(payload, raw[from:min(from+partPayloadBytes, len(raw))]...)
			pipe.Set(requestCtx, c.partKey(policy, part), payload, c.ttl+policyPartGrace)
		}
		_, err = pipe.Exec(requestCtx)
		requestCancel()
		if err != nil {
			return err
		}
	}
	hash := sha256.Sum256(raw)
	manifest, err := json.Marshal(policyEnvelope{CacheFormat, policy.ApplicationID, policy.Token, hex.EncodeToString(hash[:]), len(raw), parts})
	if err != nil {
		return err
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, c.timeout)
	defer requestCancel()
	// Complete last. Concurrent saves may overwrite current pages;
	// retries reuse the same keys. Tokens reject mixed pages and late writes after a save.
	return c.client.Set(requestCtx, PolicyKey(c.prefix, policy.ApplicationID), manifest, c.ttl).Err()
}

func (c *PolicyCache) Close() error { return c.client.Close() }
