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
	if !strings.Contains(key, ":rbac:api:app:console:") {
		t.Fatalf("application key missing from %q", key)
	}
	if !strings.HasSuffix(key, ":domain:tenant-a") {
		t.Fatal(key)
	}
	// The value is the policy itself: no envelope, token, or checksum, and it
	// never expires.
	raw, err := server.Get(key)
	if err != nil {
		t.Fatal(err)
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
	for name, mutate := range map[string]func(*biz.SubjectAPIIdentity){
		"app":     func(i *biz.SubjectAPIIdentity) { i.ApplicationKey = "billing" },
		"domain":  func(i *biz.SubjectAPIIdentity) { i.Domain = "tenant-b" },
		"user":    func(i *biz.SubjectAPIIdentity) { i.Subject = "user-1002" },
		"service": func(i *biz.SubjectAPIIdentity) { i.ServiceResource = "billing" },
	} {
		identity := base
		mutate(&identity)
		if _, err := cache.LoadSubjectAPIs(ctx, identity); !errors.Is(err, biz.ErrNotFound) {
			t.Fatalf("%s scope leaked: %v", name, err)
		}
	}
	if _, err := cache.LoadSubjectAPIs(ctx, base); err != nil {
		t.Fatal(err)
	}
}

func TestSubjectAPIKeyEscapingAndGlobalAlias(t *testing.T) {
	prefix := "nexus:test"
	base := biz.SubjectAPIIdentity{ApplicationKey: "console", ApplicationID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Subject: "alice", ServiceResource: "orders", Domain: "global"}
	if SubjectAPIKey(prefix, base) != SubjectAPIKey(prefix, biz.SubjectAPIIdentity{ApplicationKey: "console", ApplicationID: base.ApplicationID, Subject: "alice", ServiceResource: "orders", Domain: ""}) {
		t.Fatal("global alias did not fold onto the default domain")
	}
	empty := base
	empty.Domain = ""
	if !strings.HasSuffix(SubjectAPIKey(prefix, empty), ":domain:") {
		t.Fatal("empty domain lost its trailing separator")
	}
	// Special characters must not let different scopes collide through the
	// structural separators.
	left := base
	left.Subject, left.ServiceResource = "a:b", "c"
	right := base
	right.Subject, right.ServiceResource = "a", "b:c"
	if SubjectAPIKey(prefix, left) == SubjectAPIKey(prefix, right) {
		t.Fatal("escaped user/service collided")
	}
	if !strings.Contains(SubjectAPIKey(prefix, left), "a%3Ab") {
		t.Fatal("subject was not escaped")
	}
	escapedApp := biz.SubjectAPIIdentity{ApplicationKey: "tenant:console", Subject: "alice", ServiceResource: "orders"}
	if !strings.Contains(SubjectAPIKey(prefix, escapedApp), ":app:tenant%3Aconsole:") {
		t.Fatal("application key was not escaped")
	}

	cache, server := testCache(t)
	ctx := context.Background()
	identity := testSubjectAPIIdentity()
	identity.Domain = "global"
	if err := cache.StoreSubjectAPIs(ctx, testSubjectAPIPolicy(identity)); err != nil {
		t.Fatal(err)
	}
	markSubjectAPIReady(t, server, cache.prefix, identity.ApplicationKey)
	alias := identity
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
	if err := cache.BlockSubjectAPIApplications(ctx, []string{policy.ApplicationKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, policy.SubjectAPIIdentity); err == nil || errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("served a gated record: %v", err)
	}
	if _, err := server.Get(SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity)); err != nil {
		t.Fatal("blocked read deleted the record it should only hide")
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, policy.ApplicationKey)); state != subjectAPIStateUpdating {
		t.Fatalf("state = %q", state)
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

	if err := cache.BlockSubjectAPIApplications(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, []biz.SubjectAPIPolicy{alice, bob}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []biz.SubjectAPIPolicy{alice, bob} {
		if _, err := cache.LoadSubjectAPIs(ctx, p.SubjectAPIIdentity); err != nil {
			t.Fatalf("initial replace did not publish %s: %v", p.Subject, err)
		}
	}

	// The next batch drops Bob (departed). His record must be deleted.
	if err := cache.BlockSubjectAPIApplications(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, []biz.SubjectAPIPolicy{alice}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, bob.SubjectAPIIdentity); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("departed user still served: %v", err)
	}
	if _, err := server.Get(SubjectAPIKey(cache.prefix, bob.SubjectAPIIdentity)); err == nil {
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

	if err := cache.BlockSubjectAPIApplications(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	// A disabled application is published as an empty policy list: it clears old
	// records and still opens the gate.
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, nil); err != nil {
		t.Fatal(err)
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, appKey)); state != subjectAPIStateReady {
		t.Fatalf("state = %q", state)
	}
	if _, err := server.Get(SubjectAPIKey(cache.prefix, stale.SubjectAPIIdentity)); err == nil {
		t.Fatal("stale record survived an empty replace")
	}
	if members, _ := server.SMembers(subjectAPIMembersKey(cache.prefix, appKey)); len(members) != 0 {
		t.Fatalf("members = %v", members)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, stale.SubjectAPIIdentity); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("cleared scope still resolved: %v", err)
	}
}

func TestSubjectAPIReplaceRequiresUpdatingGate(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	policy := testSubjectAPIPolicy(testSubjectAPIIdentity())
	appKey := policy.ApplicationKey
	// No gate at all.
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, []biz.SubjectAPIPolicy{policy}); err == nil {
		t.Fatal("replace ran without a gate")
	}
	// Ready is not a valid gate either.
	markSubjectAPIReady(t, server, cache.prefix, appKey)
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, []biz.SubjectAPIPolicy{policy}); err == nil {
		t.Fatal("replace ran while ready")
	}
	// A policy for another application is rejected.
	if err := cache.BlockSubjectAPIApplications(ctx, []string{appKey}); err != nil {
		t.Fatal(err)
	}
	foreign := policy
	foreign.ApplicationKey = "billing"
	if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, []biz.SubjectAPIPolicy{foreign}); err == nil {
		t.Fatal("replace accepted a foreign application policy")
	}
	if state, _ := server.Get(subjectAPIStateKey(cache.prefix, appKey)); state != subjectAPIStateUpdating {
		t.Fatalf("failed replace changed the gate to %q", state)
	}
	if err := cache.ReplaceSubjectAPIApplication(ctx, "", nil); err == nil {
		t.Fatal("replace accepted an empty application key")
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
	if err := server.Set(SubjectAPIKey(cache.prefix, policy.SubjectAPIIdentity), string(raw)); err != nil {
		t.Fatal(err)
	}
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

	if err := server.Set(key, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("accepted corrupt JSON")
	}

	otherIdentity := policy
	otherIdentity.Subject = "user-9999"
	otherRaw, _ := json.Marshal(otherIdentity)
	if err := server.Set(key, string(otherRaw)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LoadSubjectAPIs(ctx, request); err == nil {
		t.Fatal("accepted an identity that does not match the key")
	}

	if err := server.Set(key, strings.Repeat("x", maxSubjectAPIBytes+1)); err != nil {
		t.Fatal(err)
	}
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
	if err := cache.BlockSubjectAPIApplications(ctx, []string{policy.ApplicationKey}); err == nil {
		t.Fatal("outage ignored on block")
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
	if err := cache.BlockSubjectAPIApplications(ctx, []string{" "}); err == nil {
		t.Fatal("block accepted an empty application key")
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
	if err := cache.BlockSubjectAPIApplications(canceled, []string{identity.ApplicationKey}); !errors.Is(err, context.Canceled) {
		t.Fatalf("block cancel = %v", err)
	}
	if err := cache.ReplaceSubjectAPIApplication(canceled, identity.ApplicationKey, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace cancel = %v", err)
	}

	cache.timeout = 20 * time.Millisecond
	cache.client.AddHook(subjectAPIDelayHook{delay: time.Second})
	if _, err := cache.LoadSubjectAPIs(ctx, identity); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load timeout = %v", err)
	}
	if err := cache.StoreSubjectAPIs(ctx, policy); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("store timeout = %v", err)
	}
}
