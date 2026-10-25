package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/redis/go-redis/v9"
)

func testSubjectAPIIdentity() biz.SubjectAPIIdentity {
	return biz.SubjectAPIIdentity{
		ApplicationKey:  "console",
		ApplicationID:   uuid.New(),
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

// markSubjectAPIReady opens the application write gate the way the batch writer
// would after a successful replace.
func markSubjectAPIReady(t *testing.T, server *miniredis.Miniredis, prefix, appKey string) {
	t.Helper()
	if err := server.Set(subjectAPIStateKey(prefix, appKey), subjectAPIStateReady); err != nil {
		t.Fatal(err)
	}
}

// leaseFor acquires the rebuild lease of the given applications.
func leaseFor(t *testing.T, cache *PolicyCache, appKeys ...string) biz.SubjectAPILease {
	t.Helper()
	lease, err := cache.AcquireSubjectAPILease(context.Background(), appKeys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Release(context.Background()) })
	return lease
}

// publishSubjectAPIs runs one complete lease-protected rebuild of an application.
func publishSubjectAPIs(t *testing.T, cache *PolicyCache, appKey string, policies ...biz.SubjectAPIPolicy) {
	t.Helper()
	ctx := context.Background()
	lease := leaseFor(t, cache, appKey)
	if err := lease.Block(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Replace(ctx, appKey, policies); err != nil {
		t.Fatal(err)
	}
	lease.Release(ctx)
}

func TestSubjectAPINoReadyGateRejectsReads(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	// A published record behind a missing gate is not readable: the gate, not
	// the record, decides whether an application is safe to serve.
	if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("load without ready gate = %v", err)
	}
}

func TestSubjectAPIRoundTripPlainJSONNoTTL(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	key := SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity)
	if want := cache.prefix + ":orders:user-1001:tenant-a"; key != want {
		t.Fatalf("key = %q want %q", key, want)
	}
	if got := server.Type(key); got != "hash" {
		t.Fatalf("cached record type = %q want hash", got)
	}
	// The field value is the policy itself: no envelope, token, or checksum,
	// and the shared hash never expires.
	raw := server.HGet(key, policy.ApplicationKey)
	if raw == "" {
		t.Fatal("policy field is missing")
	}
	for _, banned := range []string{"checksum", "format", "payload", "token"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("stored value leaked %q: %s", banned, raw)
		}
	}
	if ttl := server.TTL(key); ttl != 0 {
		t.Fatalf("ttl = %s want no expiry", ttl)
	}
	members, err := server.SMembers(subjectAPIMembersKey(cache.prefix, policy.ApplicationKey))
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0] != key {
		t.Fatalf("members = %v want [%s]", members, key)
	}
	if got, want := subjectAPIStateKey(cache.prefix, policy.ApplicationKey), cache.prefix+":rbac:api:app:console:state"; got != want {
		t.Fatalf("state key = %q want %q", got, want)
	}
	if got, want := subjectAPIMembersKey(cache.prefix, policy.ApplicationKey), cache.prefix+":rbac:api:app:console:members"; got != want {
		t.Fatalf("members key = %q want %q", got, want)
	}
	for name, got := range map[string]string{
		"lock":      subjectAPILockKey(cache.prefix, policy.ApplicationKey),
		"requested": subjectAPIRequestedKey(cache.prefix, policy.ApplicationKey),
		"published": subjectAPIPublishedKey(cache.prefix, policy.ApplicationKey),
	} {
		if want := cache.prefix + ":rbac:api:app:console:" + name; got != want {
			t.Fatalf("%s key = %q want %q", name, got, want)
		}
	}

	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)
	loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SubjectAPIIdentity != policy.SubjectAPIIdentity {
		t.Fatalf("identity = %+v", loaded.SubjectAPIIdentity)
	}
	if len(loaded.APIs) != 2 || loaded.APIs[0].PathTemplate != "/orders/{id}" {
		t.Fatalf("apis = %+v", loaded.APIs)
	}
	if len(loaded.Catalog) != 2 || loaded.Catalog[1].Enabled {
		t.Fatalf("catalog = %+v", loaded.Catalog)
	}
	if strings.Join(loaded.RoleCodes, ",") != "reader,writer" {
		t.Fatalf("roles = %v", loaded.RoleCodes)
	}

	// The request may omit the application id, and the active flag is ignored.
	request := policy.SubjectAPIIdentity
	request.ApplicationID = uuid.Nil
	request.ServiceActive = false
	if _, err := cache.LoadSubjectAPIs(ctx, request); err != nil {
		t.Fatalf("request tolerances rejected: %v", err)
	}
}

func TestSubjectAPIDeduplicatesAPIs(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	policy.APIs = append([]biz.SubjectAPIEndpoint{policy.APIs[0], policy.APIs[0]}, policy.APIs[1])
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)
	loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.APIs) != 2 {
		t.Fatalf("apis = %+v", loaded.APIs)
	}
}

func TestSubjectAPIEmptyGrantsRoundTrip(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := biz.SubjectAPIPolicy{SubjectAPIIdentity: testSubjectAPIIdentity()}
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)
	loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.APIs) != 0 || len(loaded.Catalog) != 0 || len(loaded.RoleCodes) != 0 {
		t.Fatalf("expected empty grants, got %+v", loaded)
	}
}

func TestSubjectAPIKeyIsolatesScope(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	base := testSubjectAPIIdentity()
	if err := cache.StoreSubjectAPIs(ctx, testSubjectAPIPolicy(base)); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, base.ApplicationKey)

	// The application is a field, not part of the shared hash key.
	billing := base
	billing.ApplicationKey = "billing"
	if got, want := SubjectAPIKey(cache.prefix, billing), SubjectAPIKey(cache.prefix, base); got != want {
		t.Fatalf("application changed hash key: %q != %q", got, want)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, billing); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("unpublished application field was served: %v", err)
	}
	markSubjectAPIReady(t, server, cache.prefix, billing.ApplicationKey)
	if _, err := cache.LoadSubjectAPIs(ctx, billing); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("another application's field was served: %v", err)
	}

	for name, mutate := range map[string]func(*biz.SubjectAPIIdentity){
		"domain":  func(i *biz.SubjectAPIIdentity) { i.Domain = "tenant-b" },
		"user":    func(i *biz.SubjectAPIIdentity) { i.Subject = "user-1002" },
		"service": func(i *biz.SubjectAPIIdentity) { i.ServiceResource = "billing" },
	} {
		identity := base
		mutate(&identity)
		if got, original := SubjectAPIKey(cache.prefix, identity), SubjectAPIKey(cache.prefix, base); got == original {
			t.Fatalf("%s did not change the shared hash key", name)
		}
		if _, err := cache.LoadSubjectAPIs(ctx, identity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
			t.Fatalf("%s scope leaked: %v", name, err)
		}
	}
	if _, err := cache.LoadSubjectAPIs(ctx, base); err != nil {
		t.Fatal(err)
	}
}

func TestSubjectAPIKeyEscapingAndGlobalAlias(t *testing.T) {
	prefix := "nexus:test"
	defaultKey := prefix + ":orders:alice"
	base := biz.SubjectAPIIdentity{ApplicationKey: "console", ApplicationID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Subject: "alice", ServiceResource: "orders", Domain: ""}
	if got := SubjectAPIKey(prefix, base); got != defaultKey {
		t.Fatalf("default-domain key = %q want %q", got, defaultKey)
	}
	global := base
	global.Domain = "global"
	if got := SubjectAPIKey(prefix, global); got != defaultKey {
		t.Fatalf("global alias key = %q want %q", got, defaultKey)
	}

	// Special characters must not let different scopes collide through the
	// structural separators.
	left := base
	left.Subject, left.ServiceResource = "a:b", "c"
	right := base
	right.Subject, right.ServiceResource = "a", "b:c"
	if got, want := SubjectAPIKey(prefix, left), prefix+":c:a%3Ab"; got != want {
		t.Fatalf("left escaped key = %q want %q", got, want)
	}
	if got, want := SubjectAPIKey(prefix, right), prefix+":b%3Ac:a"; got != want {
		t.Fatalf("right escaped key = %q want %q", got, want)
	}
	escaped := biz.SubjectAPIIdentity{
		ApplicationKey:  " tenant:console ",
		Subject:         " alice:team ",
		ServiceResource: " orders/api ",
		Domain:          " tenant:a ",
	}
	if got, want := SubjectAPIKey(prefix, escaped), prefix+":orders%2Fapi:alice%3Ateam:tenant%3Aa"; got != want {
		t.Fatalf("escaped named-domain key = %q want %q", got, want)
	}
	if got, want := subjectAPIStateKey(prefix, escaped.ApplicationKey), prefix+":rbac:api:app:tenant%3Aconsole:state"; got != want {
		t.Fatalf("escaped state key = %q want %q", got, want)
	}
	if got, want := subjectAPIMembersKey(prefix, escaped.ApplicationKey), prefix+":rbac:api:app:tenant%3Aconsole:members"; got != want {
		t.Fatalf("escaped members key = %q want %q", got, want)
	}

	cache, server := testCache(t)
	ctx := context.Background()
	escaped.Domain = "global"
	if err := cache.StoreSubjectAPIs(ctx, testSubjectAPIPolicy(escaped)); err != nil {
		t.Fatal(err)
	}
	key := SubjectAPIKey(cache.prefix, escaped)
	fields, err := server.HKeys(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0] != "tenant:console" {
		t.Fatalf("application fields = %v want [tenant:console]", fields)
	}
	markSubjectAPIReady(t, server, cache.prefix, escaped.ApplicationKey)
	alias := escaped
	alias.Domain = ""
	if _, err := cache.LoadSubjectAPIs(ctx, alias); err != nil {
		t.Fatalf("global alias store did not serve the default domain: %v", err)
	}
}

func TestSubjectAPIGateBlocksStaleReads(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)
	if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); err != nil {
		t.Fatal(err)
	}

	// Blocking writes the gate. The old record is still present, but a read must
	// refuse it because the gate is checked and the value fetched atomically.
	lease := leaseFor(t, cache, policy.ApplicationKey)
	if err := lease.Block(ctx, []string{policy.ApplicationKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); err == nil || errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("served a gated record: %v", err)
	}
	if raw := server.HGet(SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity), policy.ApplicationKey); raw == "" {
		t.Fatal("blocked read deleted the record it should only hide")
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, policy.ApplicationKey)); state != subjectAPIStateUpdating {
		t.Fatalf("state = %q", state)
	}
}

func TestSubjectAPIMissingRecordBehindReadyGate(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	alice := testSubjectAPIPolicy(testSubjectAPIIdentity())
	publishSubjectAPIs(t, cache, alice.ApplicationKey, alice)

	// A published application without a record for this subject is a normal
	// "no grant" miss, distinct from an application that was never published.
	bob := alice.SubjectAPIIdentity
	bob.Subject = "user-without-roles"
	if _, err := cache.LoadSubjectAPIs(ctx, bob); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("missing record = %v", err)
	}
	unpublished := alice.SubjectAPIIdentity
	unpublished.ApplicationKey = "never-published"
	_, err := cache.LoadSubjectAPIs(ctx, unpublished)
	if !errors.Is(err, biz.ErrNotFound) || errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("unpublished application = %v", err)
	}
}

func TestSubjectAPIReplaceRemovesDepartedUsers(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	base := testSubjectAPIIdentity()
	appKey := base.ApplicationKey
	alice := testSubjectAPIPolicy(base)
	bobIdentity := base
	bobIdentity.Subject = "user-2002"
	bob := testSubjectAPIPolicy(bobIdentity)

	publishSubjectAPIs(t, cache, appKey, alice, bob)
	for _, p := range []biz.SubjectAPIPolicy{alice, bob} {
		if _, err := cache.LoadSubjectAPIs(ctx, p.SubjectAPIIdentity); err != nil {
			t.Fatalf("initial replace did not publish %s: %v", p.Subject, err)
		}
	}

	// The next batch drops Bob (departed). His record must be deleted.
	publishSubjectAPIs(t, cache, appKey, alice)
	if _, err := cache.LoadSubjectAPIs(ctx, bob.SubjectAPIIdentity); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("departed user still served: %v", err)
	}
	if raw := server.HGet(SubjectAPIKey(cache.prefix, bob.SubjectAPIIdentity), appKey); raw != "" {
		t.Fatal("departed record not deleted")
	}
	if _, err := cache.LoadSubjectAPIs(ctx, alice.SubjectAPIIdentity); err != nil {
		t.Fatal(err)
	}
	members, err := server.SMembers(subjectAPIMembersKey(cache.prefix, appKey))
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0] != SubjectAPIKey(cache.prefix, alice.SubjectAPIIdentity) {
		t.Fatalf("members = %v", members)
	}
}

func TestSubjectAPIReplaceEmptyClearsOldUsers(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	appKey := "console"
	stale := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, stale); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, appKey)

	// An application without bindings is published as an empty policy list: it
	// clears old records and still opens the gate.
	publishSubjectAPIs(t, cache, appKey)
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, appKey)); state != subjectAPIStateReady {
		t.Fatalf("state = %q", state)
	}
	if raw := server.HGet(SubjectAPIKey(cache.prefix, stale.SubjectAPIIdentity), appKey); raw != "" {
		t.Fatal("stale record survived an empty replace")
	}
	if members, _ := server.SMembers(subjectAPIMembersKey(cache.prefix, appKey)); len(members) != 0 {
		t.Fatalf("members = %v", members)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, stale.SubjectAPIIdentity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("cleared scope still resolved: %v", err)
	}
}

func TestSubjectAPIApplicationsShareHashWithoutSharingData(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	consoleIdentity := testSubjectAPIIdentity()
	billingIdentity := consoleIdentity
	billingIdentity.ApplicationKey = "billing"
	billingIdentity.ApplicationID = uuid.New()

	consolePolicy := testSubjectAPIPolicy(consoleIdentity)
	billingPolicy := testSubjectAPIPolicy(billingIdentity)
	billingPolicy.RoleCodes = []string{"billing-only"}

	publishSubjectAPIs(t, cache, consoleIdentity.ApplicationKey, consolePolicy)
	publishSubjectAPIs(t, cache, billingIdentity.ApplicationKey, billingPolicy)

	key := SubjectAPIKey(cache.prefix, consoleIdentity)
	if got := SubjectAPIKey(cache.prefix, billingIdentity); got != key {
		t.Fatalf("applications use different hashes: %q != %q", got, key)
	}
	if got := server.Type(key); got != "hash" {
		t.Fatalf("cached record type = %q want hash", got)
	}
	fields, err := server.HKeys(key)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fields, ","); got != "billing,console" {
		t.Fatalf("application fields = %q want billing,console", got)
	}
	for _, policy := range []biz.SubjectAPIPolicy{consolePolicy, billingPolicy} {
		if raw := server.HGet(key, policy.ApplicationKey); raw == "" {
			t.Fatalf("%s field is missing", policy.ApplicationKey)
		}
		loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ApplicationKey != policy.ApplicationKey {
			t.Fatalf("loaded application = %q want %q", loaded.ApplicationKey, policy.ApplicationKey)
		}
	}

	// Revoking A's grants updates only A's field.
	consolePolicy.APIs = nil
	consolePolicy.RoleCodes = []string{"revoked"}
	publishSubjectAPIs(t, cache, consoleIdentity.ApplicationKey, consolePolicy)
	revoked, err := cache.LoadSubjectAPIs(ctx, consoleIdentity)
	if err != nil {
		t.Fatal(err)
	}
	stillShared, err := cache.LoadSubjectAPIs(ctx, billingIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.APIs) != 0 || strings.Join(stillShared.RoleCodes, ",") != "billing-only" {
		t.Fatalf("revoke leaked across applications: A=%+v B=%+v", revoked, stillShared)
	}

	// Replacing A with an empty set removes only A's field.
	publishSubjectAPIs(t, cache, consoleIdentity.ApplicationKey)
	if raw := server.HGet(key, consoleIdentity.ApplicationKey); raw != "" {
		t.Fatal("empty replace retained A's field")
	}
	if raw := server.HGet(key, billingIdentity.ApplicationKey); raw == "" {
		t.Fatal("empty replace deleted B's field")
	}
	if _, err := cache.LoadSubjectAPIs(ctx, consoleIdentity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("A's removed field = %v", err)
	}
	stillShared, err = cache.LoadSubjectAPIs(ctx, billingIdentity)
	if err != nil || strings.Join(stillShared.RoleCodes, ",") != "billing-only" {
		t.Fatalf("B after A empty replace = %+v, %v", stillShared, err)
	}
}

func TestSubjectAPIGateIsApplicationScoped(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	consoleIdentity := testSubjectAPIIdentity()
	billingIdentity := consoleIdentity
	billingIdentity.ApplicationKey = "billing"
	billingIdentity.ApplicationID = uuid.New()
	consolePolicy := testSubjectAPIPolicy(consoleIdentity)
	billingPolicy := testSubjectAPIPolicy(billingIdentity)

	publishSubjectAPIs(t, cache, consoleIdentity.ApplicationKey, consolePolicy)
	publishSubjectAPIs(t, cache, billingIdentity.ApplicationKey, billingPolicy)
	lease := leaseFor(t, cache, consoleIdentity.ApplicationKey)
	if err := lease.Block(ctx, []string{consoleIdentity.ApplicationKey}); err != nil {
		t.Fatal(err)
	}

	if _, err := cache.LoadSubjectAPIs(ctx, consoleIdentity); err == nil || errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("served gated application A: %v", err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, billingIdentity); err != nil {
		t.Fatalf("gating A blocked B: %v", err)
	}
	key := SubjectAPIKey(cache.prefix, billingIdentity)
	if raw := server.HGet(key, billingIdentity.ApplicationKey); raw == "" {
		t.Fatal("gating A changed B's shared hash field")
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, consoleIdentity.ApplicationKey)); state != subjectAPIStateUpdating {
		t.Fatalf("A state = %q", state)
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, billingIdentity.ApplicationKey)); state != subjectAPIStateReady {
		t.Fatalf("B state = %q", state)
	}
}

func TestSubjectAPIReplaceRequiresUpdatingGate(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	appKey := policy.ApplicationKey
	lease := leaseFor(t, cache, appKey)
	// No gate at all.
	if err := lease.Replace(ctx, appKey, []biz.SubjectAPIPolicy{policy}); err == nil {
		t.Fatal("replace ran without a gate")
	}
	// Ready is not a valid gate either.
	markSubjectAPIReady(t, server, cache.prefix, appKey)
	if err := lease.Replace(ctx, appKey, []biz.SubjectAPIPolicy{policy}); err == nil {
		t.Fatal("replace ran while ready")
	}
	// A policy for another application is rejected.
	if err := lease.Block(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	foreign := policy
	foreign.ApplicationKey = "billing"
	if err := lease.Replace(ctx, appKey, []biz.SubjectAPIPolicy{foreign}); err == nil {
		t.Fatal("replace accepted a foreign application policy")
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, appKey)); state != subjectAPIStateUpdating {
		t.Fatalf("failed replace changed the gate to %q", state)
	}
	if err := lease.Replace(ctx, "", nil); err == nil {
		t.Fatal("replace accepted an empty application key")
	}
	// Applications outside the lease cannot be touched.
	if err := lease.Block(ctx, []string{"billing"}); err == nil {
		t.Fatal("blocked an application the lease does not hold")
	}
	if err := lease.Replace(ctx, "billing", nil); err == nil {
		t.Fatal("replaced an application the lease does not hold")
	}
}

func TestSubjectAPILeaseIsExclusive(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	first, err := cache.AcquireSubjectAPILease(ctx, []string{"console", "billing"})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := cache.AcquireSubjectAPILease(waitCtx, []string{"billing"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lease while held = %v", err)
	}
	// Releasing frees every application, and a waiter then gets the lease.
	released := make(chan error, 1)
	go func() {
		lease, err := cache.AcquireSubjectAPILease(ctx, []string{"billing", "console"})
		if err == nil {
			lease.Release(ctx)
		}
		released <- err
	}()
	time.Sleep(50 * time.Millisecond)
	first.Release(ctx)
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never acquired the released lease")
	}
	if _, err := cache.AcquireSubjectAPILease(ctx, []string{" "}); err == nil {
		t.Fatal("lease accepted an empty application key")
	}
}

// A holder whose lease expired (for example after a long pause) must not be
// able to overwrite the records of the rebuild that took over.
func TestSubjectAPILostLeaseCannotWrite(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	identity := testSubjectAPIIdentity()
	appKey := identity.ApplicationKey
	stale := testSubjectAPIPolicy(identity)
	stale.RoleCodes = []string{"revoked-role"}
	current := testSubjectAPIPolicy(identity)
	current.APIs = nil
	current.RoleCodes = nil

	old := leaseFor(t, cache, appKey)
	if err := old.Block(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	server.FastForward(subjectAPILeaseTTL + time.Second)
	newer := leaseFor(t, cache, appKey)
	if err := newer.Block(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	if err := newer.Replace(ctx, appKey, []biz.SubjectAPIPolicy{current}); err != nil {
		t.Fatal(err)
	}

	if err := old.Block(ctx, []string{appKey}); !errors.Is(err, ErrSubjectAPILeaseLost) {
		t.Fatalf("expired lease blocked the gate: %v", err)
	}
	if err := server.Set(subjectAPIStateKey(cache.prefix, appKey), subjectAPIStateUpdating); err != nil {
		t.Fatal(err)
	}
	if err := old.Replace(ctx, appKey, []biz.SubjectAPIPolicy{stale}); !errors.Is(err, ErrSubjectAPILeaseLost) {
		t.Fatalf("expired lease replaced records: %v", err)
	}
	raw := server.HGet(SubjectAPIKey(cache.prefix, identity), appKey)
	if strings.Contains(raw, "revoked-role") {
		t.Fatal("stale rebuild overwrote the newer record")
	}
	// Releasing the expired lease must not free the newer holder's lock.
	old.Release(ctx)
	if lock, _ := server.Get(subjectAPILockKey(cache.prefix, appKey)); lock == "" {
		t.Fatal("expired lease released the current holder's lock")
	}
}

func TestSubjectAPIRequestAndPublishedCounters(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	appKey := "console"

	tickets, err := cache.RequestSubjectAPIRefresh(ctx, []string{appKey})
	if err != nil || tickets[appKey] != 1 {
		t.Fatalf("first ticket = %v, %v", tickets, err)
	}
	lease := leaseFor(t, cache, appKey)
	pending, err := lease.Pending(ctx, tickets)
	if err != nil || len(pending) != 1 {
		t.Fatalf("unpublished application pending = %v, %v", pending, err)
	}
	// A save that commits while this rebuild holds the lease asks for a newer
	// rebuild; this one only covers what it saw when it took the lease.
	later, err := cache.RequestSubjectAPIRefresh(ctx, []string{appKey})
	if err != nil || later[appKey] != 2 {
		t.Fatalf("second ticket = %v, %v", later, err)
	}
	if err := lease.Block(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Replace(ctx, appKey, nil); err != nil {
		t.Fatal(err)
	}
	if published, _ := server.Get(subjectAPIPublishedKey(cache.prefix, appKey)); published != "1" {
		t.Fatalf("published = %q, want 1", published)
	}
	statuses, err := cache.SubjectAPIStatuses(ctx, []string{appKey, "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if status := statuses[appKey]; status.State != subjectAPIStateReady || status.Requested != 2 || status.Published != 1 || status.Ready() {
		t.Fatalf("status = %+v", status)
	}
	if status := statuses["unknown"]; status.State != "" || status.Requested != 0 || status.Published != 0 {
		t.Fatalf("unknown status = %+v", status)
	}
	if pending, _ := lease.Pending(ctx, tickets); len(pending) != 0 {
		t.Fatalf("covered ticket still pending: %v", pending)
	}
	if pending, _ := lease.Pending(ctx, later); len(pending) != 1 {
		t.Fatalf("newer ticket not pending: %v", pending)
	}
	lease.Release(ctx)

	// The next holder covers the second save.
	publishSubjectAPIs(t, cache, appKey)
	statuses, _ = cache.SubjectAPIStatuses(ctx, []string{appKey})
	if !statuses[appKey].Ready() || statuses[appKey].Published != 2 {
		t.Fatalf("status after second rebuild = %+v", statuses[appKey])
	}
}

func TestSubjectAPIStoredValueIsPlainPolicy(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)

	// Rewriting the raw value in place (as an external writer would) must be
	// visible on the very next read: there is no checksum to reject it.
	tampered := policy
	tampered.RoleCodes = []string{"editor"}
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	server.HSet(SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity), policy.ApplicationKey, string(raw))
	loaded, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.RoleCodes) != 1 || loaded.RoleCodes[0] != "editor" {
		t.Fatalf("roles = %v", loaded.RoleCodes)
	}
}

func TestSubjectAPIClosedOnCorruptOversizeAndMismatch(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, policy.ApplicationKey)
	key := SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity)
	request := policy.SubjectAPIIdentity

	server.HSet(key, request.ApplicationKey, "{not json")
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("accepted corrupt JSON")
	}

	otherIdentity := policy
	otherIdentity.Subject = "user-9999"
	otherRaw, _ := json.Marshal(otherIdentity)
	server.HSet(key, request.ApplicationKey, string(otherRaw))
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("accepted an identity that does not match the key")
	}

	server.HSet(key, request.ApplicationKey, strings.Repeat("x", maxSubjectAPIBytes+1))
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("accepted an oversized payload")
	}

	server.SetError("ERR outage")
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("outage ignored on load")
	}
	if err := cache.StoreSubjectAPIs(ctx, policy); err == nil {
		t.Fatal("outage ignored on store")
	}
	if _, err := cache.AcquireSubjectAPILease(ctx, []string{policy.ApplicationKey}); err == nil {
		t.Fatal("outage ignored on lease")
	}
	if _, err := cache.RequestSubjectAPIRefresh(ctx, []string{policy.ApplicationKey}); err == nil {
		t.Fatal("outage ignored on refresh request")
	}
}

func TestSubjectAPIDoesNotReadLegacyStringKey(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	identity := testSubjectAPIIdentity()
	policy := testSubjectAPIPolicy(identity)
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	legacyKey := cache.prefix + ":console:user-1001:orders:tenant-a"
	if err := server.Set(legacyKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, identity.ApplicationKey)

	if _, err := cache.LoadSubjectAPIs(ctx, identity); !errors.Is(err, biz.ErrSubjectAPIRecordNotFound) {
		t.Fatalf("legacy string fallback = %v", err)
	}
	if raw := server.HGet(SubjectAPIKey(cache.prefix, identity), identity.ApplicationKey); raw != "" {
		t.Fatalf("load populated a hash field from the legacy string: %s", raw)
	}
}

func TestSubjectAPIRejectsInvalidIdentity(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	base := testSubjectAPIIdentity()
	for name, mutate := range map[string]func(*biz.SubjectAPIIdentity){
		"app":     func(i *biz.SubjectAPIIdentity) { i.ApplicationKey = "  " },
		"subject": func(i *biz.SubjectAPIIdentity) { i.Subject = "  " },
		"service": func(i *biz.SubjectAPIIdentity) { i.ServiceResource = "" },
	} {
		identity := base
		mutate(&identity)
		if err := cache.StoreSubjectAPIs(ctx, biz.SubjectAPIPolicy{SubjectAPIIdentity: identity}); err == nil {
			t.Fatalf("store accepted invalid %s", name)
		}
		if _, err := cache.LoadSubjectAPIs(ctx, identity); err == nil {
			t.Fatalf("load accepted invalid %s", name)
		}
	}
	if _, err := cache.RequestSubjectAPIRefresh(ctx, []string{" "}); err == nil {
		t.Fatal("refresh request accepted an empty application key")
	}
}

type subjectAPIDelayHook struct{ delay time.Duration }

func (h subjectAPIDelayHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h subjectAPIDelayHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		select {
		case <-time.After(h.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		return next(ctx, cmd)
	}
}
func (h subjectAPIDelayHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestSubjectAPITimeoutOutageAndCancellation(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	identity := testSubjectAPIIdentity()
	policy := testSubjectAPIPolicy(identity)
	if err := cache.StoreSubjectAPIs(ctx, policy); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, identity.ApplicationKey)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := cache.StoreSubjectAPIs(canceled, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("store cancel = %v", err)
	}
	if _, err := cache.LoadSubjectAPIs(canceled, identity); err == nil {
		t.Fatal("canceled load succeeded")
	}
	lease := leaseFor(t, cache, identity.ApplicationKey)
	if err := lease.Block(canceled, []string{identity.ApplicationKey}); !errors.Is(err, context.Canceled) {
		t.Fatalf("block cancel = %v", err)
	}
	if err := lease.Replace(canceled, identity.ApplicationKey, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace cancel = %v", err)
	}
	if _, err := cache.AcquireSubjectAPILease(canceled, []string{"other-app"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lease cancel = %v", err)
	}
	lease.Release(ctx)

	cache.timeout = 20 * time.Millisecond
	cache.client.AddHook(subjectAPIDelayHook{delay: time.Second})
	if _, err := cache.LoadSubjectAPIs(ctx, identity); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load timeout = %v", err)
	}
	if err := cache.StoreSubjectAPIs(ctx, policy); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("store timeout = %v", err)
	}
}
