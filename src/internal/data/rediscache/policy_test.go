package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/redis/go-redis/v9"
)

func testCache(t *testing.T) (*PolicyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	cache, err := New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization:test", CacheTTL: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache, server
}
func testPolicy() biz.CurrentPolicy {
	app := uuid.New()
	return biz.CurrentPolicy{ApplicationID: app, Token: uuid.NewString(), Data: map[string]any{"application_id": app.String(), "app_key": "console", "roles": []any{map[string]any{"code": "reader", "enabled": true}}}}
}
func TestCurrentPolicyRoundTripSaveInvalidationAndExpiration(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	first := testPolicy()
	if err := cache.Store(ctx, first); err != nil {
		t.Fatal(err)
	}
	key := PolicyKey(cache.prefix, first.ApplicationID)
	if strings.Contains(key, first.Token) || !strings.Contains(key, ":rbac:current:") {
		t.Fatal(key)
	}
	if server.TTL(key) != time.Hour {
		t.Fatal(server.TTL(key))
	}
	loaded, err := cache.Load(ctx, first)
	if err != nil || loaded["app_key"] != "console" {
		t.Fatalf("round trip: %v %v", loaded, err)
	}
	second := first
	second.Token = uuid.NewString()
	second.Data = map[string]any{"application_id": first.ApplicationID.String(), "roles": []any{}}
	if _, err := cache.Load(ctx, second); err == nil {
		t.Fatal("save did not invalidate current cache")
	}
	if err := cache.Store(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(server.Keys()) != 2 {
		t.Fatal("save created a new set of keys")
	}
	// A delayed old fill can only cause a miss, never validate old grants with the new token.
	if err := cache.Store(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(ctx, second); err == nil {
		t.Fatal("late fill resurrected old grants")
	}
	other := first
	other.ApplicationID = uuid.New()
	if _, err := cache.Load(ctx, other); !errors.Is(err, biz.ErrNotFound) {
		t.Fatal(err)
	}
	server.FastForward(time.Hour)
	if _, err := cache.Load(ctx, first); !errors.Is(err, biz.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestCurrentPolicyRejectsCorruptManifestAndParts(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testPolicy()
	key := PolicyKey(cache.prefix, policy.ApplicationID)
	for _, mutate := range []func(*policyEnvelope){
		func(v *policyEnvelope) { v.Format++ }, func(v *policyEnvelope) { v.ApplicationID = uuid.New() }, func(v *policyEnvelope) { v.Token = uuid.NewString() },
		func(v *policyEnvelope) { v.Bytes++ }, func(v *policyEnvelope) { v.Parts++ }, func(v *policyEnvelope) { v.Bytes = maxPolicyBytes + 1 }, func(v *policyEnvelope) { v.Checksum = strings.Repeat("0", 64) },
	} {
		if err := cache.Store(ctx, policy); err != nil {
			t.Fatal(err)
		}
		raw, _ := server.Get(key)
		var manifest policyEnvelope
		if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
			t.Fatal(err)
		}
		mutate(&manifest)
		data, _ := json.Marshal(manifest)
		if err := server.Set(key, string(data)); err != nil {
			t.Fatal(err)
		}
		if data, err := cache.Load(ctx, policy); err == nil || data != nil {
			t.Fatal("accepted corrupt metadata")
		}
	}
	if err := cache.Store(ctx, policy); err != nil {
		t.Fatal(err)
	}
	partKey := cache.partKey(policy, 0)
	raw, _ := server.Get(partKey)
	if err := server.Set(partKey, raw[:partHeaderBytes]+"X"+raw[partHeaderBytes+1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(ctx, policy); err == nil {
		t.Fatal("accepted corrupt content")
	}
	policy.Data["application_id"] = uuid.NewString()
	if err := cache.Store(ctx, policy); err == nil {
		t.Fatal("cross-app store accepted")
	}
}
func TestCurrentPolicyBoundedKeysLargeBindingsAndRetry(t *testing.T) {
	cache, server := testCache(t)
	policy := testPolicy()
	ctx := context.Background()
	grants, bindings := make([]any, 20000), make([]any, 100000)
	for i := range grants {
		grants[i] = fmt.Sprintf("endpoint-%06d", i)
	}
	for i := range bindings {
		bindings[i] = map[string]any{"subject": fmt.Sprintf("user-%06d", i), "domain": "tenant", "role_id": "one-role"}
	}
	policy.Data["roles"] = []any{map[string]any{"id": "one-role", "endpoint_ids": grants}}
	policy.Data["subject_roles"] = bindings
	if err := cache.Store(ctx, policy); err != nil {
		t.Fatal(err)
	}
	raw, _ := policyBytes(policy)
	parts := (len(raw) + partPayloadBytes - 1) / partPayloadBytes
	if len(server.Keys()) != parts+1 {
		t.Fatalf("keys=%d want=%d", len(server.Keys()), parts+1)
	}
	for _, key := range server.Keys() {
		value, _ := server.Get(key)
		if len(value) > PolicyPartBytes {
			t.Fatal("large key", key)
		}
	}
	var group sync.WaitGroup
	for i := 0; i < 3; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := cache.Store(ctx, policy); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if len(server.Keys()) != parts+1 {
		t.Fatal("retry created extra keys")
	}
	loaded, err := cache.Load(ctx, policy)
	if err != nil || len(loaded["subject_roles"].([]any)) != len(bindings) {
		t.Fatal(err)
	}
	server.Del(cache.partKey(policy, 1))
	if data, err := cache.Load(ctx, policy); err == nil || data != nil {
		t.Fatal("accepted incomplete cache")
	}
	if err := cache.Store(ctx, policy); err != nil {
		t.Fatal(err)
	}
	server.FastForward(time.Hour)
	if _, err := cache.Load(ctx, policy); !errors.Is(err, biz.ErrNotFound) {
		t.Fatal(err)
	}
	server.FastForward(policyPartGrace)
	if len(server.Keys()) != 0 {
		t.Fatal("expired pages leaked")
	}
	t.Logf("bindings=%d grants=%d JSON_bytes=%d parts=%d keys=%d", len(bindings), len(grants), len(raw), parts, parts+1)
}

type failLaterBatchHook struct{ enabled atomic.Bool }

func (h *failLaterBatchHook) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *failLaterBatchHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *failLaterBatchHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		if h.enabled.Load() && len(commands) > 0 && commands[0].Name() == "set" && strings.HasSuffix(fmt.Sprint(commands[0].Args()[1]), ":part:16") {
			return errors.New("injected second batch failure")
		}
		return next(ctx, commands)
	}
}
func TestCurrentPolicyPartialWriteMixedTokensAndCancellation(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	first := testPolicy()
	first.Data["large_field"] = strings.Repeat("x", policyBatchParts*PolicyPartBytes+100)
	if err := cache.Store(ctx, first); err != nil {
		t.Fatal(err)
	}
	next := first
	next.Token = uuid.NewString()
	hook := &failLaterBatchHook{}
	hook.enabled.Store(true)
	cache.client.AddHook(hook)
	if err := cache.Store(ctx, next); err == nil {
		t.Fatal("expected partial write failure")
	}
	if data, err := cache.Load(ctx, next); err == nil || data != nil {
		t.Fatal("new data visible before complete")
	}
	// Old manifest + partially new pages must be rejected as well.
	if data, err := cache.Load(ctx, first); err == nil || data != nil {
		t.Fatal("mixed tokens accepted")
	}
	hook.enabled.Store(false)
	if err := cache.Store(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(ctx, next); err != nil {
		t.Fatal(err)
	}
	server.SetError("ERR outage")
	if _, err := cache.Load(ctx, next); err == nil {
		t.Fatal("outage ignored")
	}
	if err := cache.Store(ctx, next); err == nil {
		t.Fatal("outage store succeeded")
	}
	server.SetError("")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := cache.Store(canceled, next); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
