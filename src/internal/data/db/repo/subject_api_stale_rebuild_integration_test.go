package repo

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

// pausingSubjectAPICache pauses the first ReplaceSubjectAPIs call. Production
// calls it only after the repeatable-read snapshot has been read and while the
// PostgreSQL publication lock is still held.
type pausingSubjectAPICache struct {
	SubjectAPICache

	mu            sync.Mutex
	replaceCalls  int
	firstSnapshot chan []biz.SubjectAPIPolicy
	firstPaused   chan struct{}
	releaseFirst  chan struct{}
	secondStarted chan struct{}
}

func (c *pausingSubjectAPICache) ReplaceSubjectAPIs(ctx context.Context, policies []biz.SubjectAPIPolicy) error {
	c.mu.Lock()
	c.replaceCalls++
	call := c.replaceCalls
	c.mu.Unlock()

	switch call {
	case 1:
		c.firstSnapshot <- policies
		close(c.firstPaused)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.releaseFirst:
		}
	case 2:
		close(c.secondStarted)
	}
	return c.SubjectAPICache.ReplaceSubjectAPIs(ctx, policies)
}

func policyPathsForSubject(policies []biz.SubjectAPIPolicy, service, subject, domain string) map[string]bool {
	for _, policy := range policies {
		if policy.ServiceResource == service &&
			policy.Subject == subject &&
			biz.NormalizeRBACDomain(policy.Domain) == biz.NormalizeRBACDomain(domain) {
			paths := make(map[string]bool, len(policy.APIs))
			for _, endpoint := range policy.APIs {
				paths[endpoint.PathTemplate] = true
			}
			return paths
		}
	}
	return nil
}

// A rebuild that read the database before a revoke committed must never
// overwrite the newer cross-instance rebuild. The second repository commits
// while the first cache replacement is paused; the PostgreSQL lock must keep
// its snapshot and replacement behind the first publisher.
func TestSubjectAPIStaleRebuildCannotOverwriteRevoke(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, "alice", "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}

	pausing := &pausingSubjectAPICache{
		SubjectAPICache: f.repo.cache,
		firstSnapshot:   make(chan []biz.SubjectAPIPolicy, 1),
		firstPaused:     make(chan struct{}),
		releaseFirst:    make(chan struct{}),
		secondStarted:   make(chan struct{}),
	}
	firstRepo := f.repo.WithPolicyCache(pausing)
	secondRepo := NewRBACV1Repository(f.repo.pool).WithPolicyCache(pausing)

	slow := make(chan error, 1)
	go func() {
		slow <- firstRepo.ReplaceSubjectRoles(ctx, f.app, "bob", "", []uuid.UUID{f.role.ID})
	}()
	select {
	case <-pausing.firstPaused:
	case <-time.After(10 * time.Second):
		t.Fatal("first rebuild never reached ReplaceSubjectAPIs")
	}

	firstPolicies := <-pausing.firstSnapshot
	if first := policyPathsForSubject(firstPolicies, f.service, "alice", ""); !first["/orders"] {
		t.Fatalf("paused snapshot did not contain alice's pre-revoke grant: %v", first)
	}

	// Commit the revoke through another pool connection. The channel-free
	// commit boundary is explicit: reconciliation starts only after Commit
	// returns, so this test does not infer commit completion from a sleep.
	tx, err := f.repo.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM subject_role_bindings b USING applications a
		WHERE b.application_id=a.id AND a.app_key=$1 AND b.subject='alice'`, f.app)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("revoke affected %d rows, want 1", tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	revoke := make(chan error, 1)
	go func() {
		_, err := secondRepo.ReconcileSubjectAPIs(ctx, false)
		revoke <- err
	}()

	// Probe from a third session while the first ReplaceSubjectAPIs is paused.
	// The lock is still held, so another instance cannot enter its snapshot.
	conn, err := f.repo.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, subjectAPIPublicationLock).Scan(&acquired)
	if err != nil {
		conn.Release()
		t.Fatal(err)
	}
	if acquired {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, subjectAPIPublicationLock)
	}
	conn.Release()
	if acquired {
		t.Fatal("publication lock was released before ReplaceSubjectAPIs completed")
	}
	select {
	case <-pausing.secondStarted:
		t.Fatal("second repository entered ReplaceSubjectAPIs while the first publisher was paused")
	default:
	}

	close(pausing.releaseFirst)
	select {
	case err := <-slow:
		if err != nil {
			t.Fatalf("unrelated save: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("unrelated save never finished")
	}
	select {
	case <-pausing.secondStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("second repository never entered ReplaceSubjectAPIs")
	}
	select {
	case err := <-revoke:
		if err != nil {
			t.Fatalf("revoke rebuild: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("revoke rebuild never finished")
	}

	if f.allowed(t, "alice") {
		t.Fatal("a stale rebuild republished alice's revoked grant")
	}
	if !f.allowed(t, "bob") {
		t.Fatal("bob's grant was lost")
	}
}

func newSingleConnectionRepositoryTestPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 1
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = repositoryTestSchema
	return pgxpool.NewWithConfig(ctx, cfg)
}

func TestReconcileSubjectAPIsWithSingleConnectionPool(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := newSingleConnectionRepositoryTestPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	server := miniredis.RunT(t)
	cache, err := rediscache.New(config.RedisConfig{
		URL:       "redis://" + server.Addr(),
		KeyPrefix: "nexusauthorization:test:" + uuid.NewString(),
		CacheTTL:  "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	appKey := "single-connection-" + uuid.NewString()
	repo := NewRBACV1Repository(pool)
	if _, err := repo.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: appKey, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM applications WHERE app_key=$1`, appKey)
	})
	repo.WithPolicyCache(cache)

	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	count, err := repo.ReconcileSubjectAPIs(requestCtx, false)
	if err != nil {
		t.Fatalf("reconcile with maxConns=1: %v", err)
	}
	if count == 0 {
		t.Fatal("reconcile reported no participating applications")
	}
}
