package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
)

func testSubjectAPIIdentity() biz.SubjectAPIIdentity {
	return biz.SubjectAPIIdentity{
		Subject:         "user-1001",
		Domain:          "tenant-a",
		ServiceResource: "orders",
		ServiceActive:   true,
	}
}

func testSubjectAPIPolicy(identity biz.SubjectAPIIdentity) biz.SubjectAPIPolicy {
	return biz.SubjectAPIPolicy{
		SubjectAPIIdentity: identity,
		APIs: []biz.SubjectAPIEndpoint{
			{ID: uuid.New(), Method: "GET", PathTemplate: "/orders/{id}", Enabled: true},
			{ID: uuid.New(), Method: "POST", PathTemplate: "/orders", Enabled: true},
		},
		Catalog: []biz.SubjectAPIEndpoint{
			{ID: uuid.New(), Method: "GET", PathTemplate: "/orders/{id}", Enabled: true},
			{ID: uuid.New(), Method: "DELETE", PathTemplate: "/orders/{id}", Enabled: false},
		},
		RoleCodes: []string{"reader", "writer"},
	}
}

func testCacheWithPrefix(t *testing.T, prefix string) (*PolicyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	cache, err := New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache, server
}

func sortedKeys(server *miniredis.Miniredis) []string {
	keys := server.Keys()
	sort.Strings(keys)
	return keys
}

func TestSubjectAPIStringRoundTripClearsApplicationMetadata(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	identity := biz.SubjectAPIIdentity{
		ApplicationKey:  " console ",
		ApplicationID:   uuid.New(),
		ServiceResource: " orders/API ",
		Subject:         " alice:team ",
		Domain:          " tenant:a ",
		ServiceActive:   true,
	}
	policy := testSubjectAPIPolicy(identity)
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}

	key := cache.prefix + ":orders%2FAPI:alice%3Ateam:tenant%3Aa"
	if server.Type(key) != "string" {
		t.Fatalf("cached type = %q, want string", server.Type(key))
	}
	if ttl := server.TTL(key); ttl != 0 {
		t.Fatalf("ttl = %s, want no expiry", ttl)
	}
	if keys := sortedKeys(server); len(keys) != 1 || keys[0] != key {
		t.Fatalf("redis keys = %v, want only %q", keys, key)
	}

	raw, err := server.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"application_key"`) || strings.Contains(raw, `"application_id"`) {
		t.Fatalf("stored JSON retained application fields: %s", raw)
	}
	var stored biz.SubjectAPIPolicy
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("stored policy is not complete policy JSON: %v", err)
	}
	if stored.ApplicationKey != "" || stored.ApplicationID != uuid.Nil {
		t.Fatalf("application metadata was not cleared: %+v", stored.SubjectAPIIdentity)
	}
	if stored.ServiceResource != "orders/API" || stored.Subject != "alice:team" || stored.Domain != "tenant:a" {
		t.Fatalf("stored identity is not canonical: %+v", stored.SubjectAPIIdentity)
	}
	if len(stored.APIs) != 2 || len(stored.Catalog) != 2 ||
		strings.Join(stored.RoleCodes, ",") != "reader,writer" {
		t.Fatalf("stored policy lost fields: %+v", stored)
	}

	request := stored.SubjectAPIIdentity
	request.ApplicationKey = "billing"
	request.ApplicationID = uuid.New()
	request.ServiceActive = false
	loaded, err := cache.LoadSubjectAPIs(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.APIs) != 2 || loaded.APIs[0].PathTemplate != "/orders/{id}" {
		t.Fatalf("loaded APIs = %+v", loaded.APIs)
	}
	if loaded.ApplicationKey != "" || loaded.ApplicationID != uuid.Nil {
		t.Fatalf("loaded application metadata = %+v", loaded.SubjectAPIIdentity)
	}
}

func TestSubjectAPILoadMissingReturnsRecordNotFound(t *testing.T) {
	cache, _ := testCache(t)
	_, err := cache.LoadSubjectAPIs(context.Background(), testSubjectAPIIdentity())
	if !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("load missing record = %v, want ErrSubjectAPIRecordNotFound", err)
	}
}

func TestSubjectAPIReplaceRevokesDepartedRecords(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	alice := testSubjectAPIPolicy(testSubjectAPIIdentity())
	bobIdentity := testSubjectAPIIdentity()
	bobIdentity.Subject = "user-2002"
	bob := testSubjectAPIPolicy(bobIdentity)
	carolIdentity := testSubjectAPIIdentity()
	carolIdentity.ServiceResource = "billing"
	carolIdentity.Subject = "user-3003"
	carol := testSubjectAPIPolicy(carolIdentity)

	if err := cache.ReplaceSubjectAPIs(ctx, []biz.SubjectAPIPolicy{alice, bob, carol}); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []biz.SubjectAPIPolicy{alice, bob, carol} {
		if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); err != nil {
			t.Fatalf("load %q: %v", policy.Subject, err)
		}
	}

	if err := cache.ReplaceSubjectAPIs(ctx, []biz.SubjectAPIPolicy{bob}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, alice.SubjectAPIIdentity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("revoked alice record = %v", err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, carol.SubjectAPIIdentity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("revoked carol record = %v", err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, bob.SubjectAPIIdentity); err != nil {
		t.Fatalf("kept bob record: %v", err)
	}
	if keys := sortedKeys(server); len(keys) != 1 ||
		keys[0] != SubjectAPIKey(cache.prefix, bob.SubjectAPIIdentity) {
		t.Fatalf("keys after revoke = %v", keys)
	}
}

func TestSubjectAPIEmptyReplaceDeletesOnlyCanonicalBusinessKeysUnderEscapedPrefix(t *testing.T) {
	const prefix = "nexusauthorization:test*"
	cache, server := testCacheWithPrefix(t, prefix)
	ctx := context.Background()

	firstIdentity := testSubjectAPIIdentity()
	secondIdentity := testSubjectAPIIdentity()
	secondIdentity.ServiceResource = "billing/api"
	secondIdentity.Subject = "user:2002"
	secondIdentity.Domain = ""
	firstKey := SubjectAPIKey(prefix, firstIdentity)
	secondKey := SubjectAPIKey(prefix, secondIdentity)
	if err := server.Set(firstKey, `{"old":true}`); err != nil {
		t.Fatal(err)
	}
	server.HSet(secondKey, "legacy-application", `{"old":true}`)

	rbacKey := prefix + ":rbac:api:app:console:state"
	otherPrefixKey := "nexusauthorization:test-other:orders:alice"
	preserved := []string{
		rbacKey,
		otherPrefixKey,
		prefix + ":orders:alice:global",
		prefix + ":orders%20:alice",
		prefix + ":orders:alice:tenant%3aa",
		prefix + ":orders::alice",
		prefix + ":orders:alice%ZZ",
		prefix + ":only-one-segment",
		prefix,
	}
	server.HSet(rbacKey, "state", "ready")
	server.SetTTL(rbacKey, time.Hour)
	if err := server.Set(otherPrefixKey, `{"different-prefix":true}`); err != nil {
		t.Fatal(err)
	}
	for _, key := range preserved[2:] {
		if err := server.Set(key, key); err != nil {
			t.Fatal(err)
		}
	}

	if err := cache.ReplaceSubjectAPIs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if server.Exists(firstKey) || server.Exists(secondKey) {
		t.Fatalf("empty replace left canonical business keys: %v", sortedKeys(server))
	}
	for _, key := range preserved {
		if !server.Exists(key) {
			t.Fatalf("empty replace removed preserved key %q: %v", key, sortedKeys(server))
		}
	}
	if server.Type(rbacKey) != "hash" || server.HGet(rbacKey, "state") != "ready" {
		t.Fatalf("rbac metadata was modified: type=%s value=%q", server.Type(rbacKey), server.HGet(rbacKey, "state"))
	}
	if ttl := server.TTL(rbacKey); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("rbac metadata ttl = %s", ttl)
	}
}

func TestSubjectAPIReplaceOverwritesLegacyHashAndClearsTTL(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	key := SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity)
	server.HSet(key, "application", `{"legacy":true}`)
	server.SetTTL(key, time.Hour)

	if err := cache.ReplaceSubjectAPIs(ctx, []biz.SubjectAPIPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	if got := server.Type(key); got != "string" {
		t.Fatalf("migrated key type = %q, want string", got)
	}
	if ttl := server.TTL(key); ttl != 0 {
		t.Fatalf("migrated key ttl = %s, want no expiry", ttl)
	}
	loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.APIs) != len(policy.APIs) {
		t.Fatalf("loaded APIs = %+v", loaded.APIs)
	}
}

func TestSubjectAPILoadRejectsBadRecordsAndIdentity(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	identity := testSubjectAPIIdentity()
	key := SubjectAPIKey(cache.prefix, identity)

	tests := []struct {
		name    string
		store   func()
		wantErr string
	}{
		{
			name: "wrong Redis type",
			store: func() {
				server.HSet(key, "legacy", `{"apis":[]}`)
			},
			wantErr: "WRONGTYPE",
		},
		{
			name: "malformed JSON",
			store: func() {
				if err := server.Set(key, `{"apis":`); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "invalid cached subject api JSON",
		},
		{
			name: "oversized JSON",
			store: func() {
				if err := server.Set(key, strings.Repeat(" ", maxSubjectAPIBytes)+"{}"); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "oversized",
		},
		{
			name: "legacy application key",
			store: func() {
				legacy := testSubjectAPIPolicy(identity)
				legacy.ApplicationKey = "console"
				raw, err := json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.Set(key, string(raw)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "application metadata",
		},
		{
			name: "legacy application id",
			store: func() {
				legacy := testSubjectAPIPolicy(identity)
				legacy.ApplicationID = uuid.New()
				raw, err := json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.Set(key, string(raw)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "application metadata",
		},
		{
			name: "legacy whitespace application key",
			store: func() {
				legacy := testSubjectAPIPolicy(identity)
				legacy.ApplicationKey = " \t "
				raw, err := json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.Set(key, string(raw)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "application metadata",
		},
		{
			name: "invalid stored identity",
			store: func() {
				invalid := testSubjectAPIPolicy(identity)
				invalid.ServiceResource = ""
				raw, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.Set(key, string(raw)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "invalid subject api identity",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server.FlushAll()
			test.store()
			if _, err := cache.LoadSubjectAPIs(ctx, identity); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("load error = %v, want substring %q", err, test.wantErr)
			}
		})
	}

	t.Run("identity mismatches", func(t *testing.T) {
		for _, mutate := range []struct {
			name   string
			mutate func(*biz.SubjectAPIIdentity)
		}{
			{"service", func(value *biz.SubjectAPIIdentity) { value.ServiceResource = "billing" }},
			{"subject", func(value *biz.SubjectAPIIdentity) { value.Subject = "another-user" }},
			{"domain", func(value *biz.SubjectAPIIdentity) { value.Domain = "tenant-b" }},
			{"service whitespace", func(value *biz.SubjectAPIIdentity) { value.ServiceResource = " orders " }},
			{"subject whitespace", func(value *biz.SubjectAPIIdentity) { value.Subject = " user-1001 " }},
		} {
			t.Run(mutate.name, func(t *testing.T) {
				server.FlushAll()
				stored := testSubjectAPIPolicy(identity)
				mutate.mutate(&stored.SubjectAPIIdentity)
				raw, err := json.Marshal(stored)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.Set(key, string(raw)); err != nil {
					t.Fatal(err)
				}
				if _, err := cache.LoadSubjectAPIs(ctx, identity); err == nil ||
					!strings.Contains(err.Error(), "identity mismatch") {
					t.Fatalf("%s mismatch error = %v", mutate.name, err)
				}
			})
		}
	})

	t.Run("invalid lookup identity", func(t *testing.T) {
		for name, invalid := range map[string]biz.SubjectAPIIdentity{
			"service": {ServiceResource: " ", Subject: "alice"},
			"subject": {ServiceResource: "orders", Subject: " "},
		} {
			if _, err := cache.LoadSubjectAPIs(ctx, invalid); err == nil {
				t.Fatalf("invalid %s identity was accepted", name)
			}
		}
	})
}

func TestSubjectAPIKeyPrefixEncodingAndDomainIsolation(t *testing.T) {
	const prefix = "nexus:test"
	base := biz.SubjectAPIIdentity{
		ApplicationKey:  "console",
		ServiceResource: " orders/api ",
		Subject:         " alice:team ",
		Domain:          " tenant:a ",
	}
	if got, want := SubjectAPIKey(prefix, base), prefix+":orders%2Fapi:alice%3Ateam:tenant%3Aa"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	global := base
	global.Domain = "global"
	defaultDomain := base
	defaultDomain.Domain = ""
	if globalKey, defaultKey := SubjectAPIKey(prefix, global), SubjectAPIKey(prefix, defaultDomain); globalKey != defaultKey {
		t.Fatalf("global alias key = %q, default key = %q", globalKey, defaultKey)
	}
	withApplication := base
	withApplication.ApplicationKey = "billing"
	withApplication.ApplicationID = uuid.New()
	if SubjectAPIKey(prefix, withApplication) != SubjectAPIKey(prefix, base) {
		t.Fatal("application metadata changed the Redis key")
	}

	left := base
	left.ServiceResource, left.Subject, left.Domain = "c", "a:b", ""
	right := base
	right.ServiceResource, right.Subject, right.Domain = "b:c", "a", ""
	if SubjectAPIKey(prefix, left) == SubjectAPIKey(prefix, right) {
		t.Fatalf("escaped identities collided: %q", SubjectAPIKey(prefix, left))
	}

	cache, server := testCache(t)
	ctx := context.Background()
	defaultIdentity := base
	defaultIdentity.Domain = ""
	tenantA := base
	tenantB := base
	tenantB.Domain = "tenant-b"
	policies := []biz.SubjectAPIPolicy{
		testSubjectAPIPolicy(defaultIdentity),
		testSubjectAPIPolicy(tenantA),
		testSubjectAPIPolicy(tenantB),
	}
	if err := cache.ReplaceSubjectAPIs(ctx, policies); err != nil {
		t.Fatal(err)
	}
	if keys := sortedKeys(server); len(keys) != 3 {
		t.Fatalf("domain records = %v, want three isolated keys", keys)
	}
	for _, policy := range policies {
		loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
		if err != nil {
			t.Fatalf("load domain %q: %v", policy.Domain, err)
		}
		if got, want := loaded.Domain, biz.NormalizeRBACDomain(policy.Domain); got != want {
			t.Fatalf("loaded domain = %q, want %q", got, want)
		}
	}
	missingDomain := tenantA
	missingDomain.Domain = "tenant-c"
	if _, err := cache.LoadSubjectAPIs(ctx, missingDomain); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("cross-domain load = %v", err)
	}
}

func TestSubjectAPIReplacePrevalidatesEntireInput(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	sentinelIdentity := testSubjectAPIIdentity()
	sentinelIdentity.Subject = "sentinel"
	sentinelKey := SubjectAPIKey(cache.prefix, sentinelIdentity)
	server.HSet(sentinelKey, "legacy", `{"apis":[]}`)
	originalKeys := sortedKeys(server)

	base := testSubjectAPIIdentity()
	base.Subject = "new-user"
	newKey := SubjectAPIKey(cache.prefix, base)
	duplicate := base
	duplicate.Subject = " new-user "
	duplicate.Domain = " tenant-a "

	oversized := testSubjectAPIPolicy(base)
	oversized.RoleCodes = []string{strings.Repeat("x", maxSubjectAPIBytes)}

	tests := []struct {
		name     string
		policies []biz.SubjectAPIPolicy
		wantErr  string
	}{
		{
			name: "invalid identity",
			policies: []biz.SubjectAPIPolicy{
				testSubjectAPIPolicy(base),
				testSubjectAPIPolicy(biz.SubjectAPIIdentity{ServiceResource: "orders", Subject: " "}),
			},
			wantErr: "invalid subject api identity",
		},
		{
			name: "duplicate canonical key",
			policies: []biz.SubjectAPIPolicy{
				testSubjectAPIPolicy(base),
				testSubjectAPIPolicy(duplicate),
			},
			wantErr: "duplicate subject api policy key",
		},
		{
			name: "oversized record",
			policies: []biz.SubjectAPIPolicy{
				testSubjectAPIPolicy(base),
				oversized,
			},
			wantErr: "exceeds Redis cache size limit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := cache.ReplaceSubjectAPIs(ctx, test.policies)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("replace error = %v, want substring %q", err, test.wantErr)
			}
			if server.Type(sentinelKey) != "hash" || server.HGet(sentinelKey, "legacy") != `{"apis":[]}` {
				t.Fatal("prevalidation failure modified the existing business record")
			}
			if server.Exists(newKey) {
				t.Fatal("prevalidation failure wrote a new record")
			}
			gotKeys := sortedKeys(server)
			if strings.Join(gotKeys, "\x00") != strings.Join(originalKeys, "\x00") {
				t.Fatalf("prevalidation failure changed keys: got %v, want %v", gotKeys, originalKeys)
			}
		})
	}
}

func TestSubjectAPIContextErrorsAreReturned(t *testing.T) {
	cache, _ := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())

	if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); !errors.Is(err, context.Canceled) {
		t.Fatalf("load canceled error = %v", err)
	}
	if err := cache.StoreSubjectAPIs(ctx, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("store canceled error = %v", err)
	}
	if err := cache.ReplaceSubjectAPIs(ctx, []biz.SubjectAPIPolicy{policy}); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace canceled error = %v", err)
	}
}
