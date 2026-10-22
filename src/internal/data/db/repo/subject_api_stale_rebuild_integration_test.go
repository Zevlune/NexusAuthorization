package repo

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

// pausingSubjectAPICache pauses the first rebuild right before it closes the
// gate, that is after it has read its database snapshot.
type pausingSubjectAPICache struct {
	SubjectAPICache
	once    sync.Once
	paused  chan struct{}
	release chan struct{}
}

func (c *pausingSubjectAPICache) AcquireSubjectAPILease(ctx context.Context, appKeys []string) (biz.SubjectAPILease, error) {
	lease, err := c.SubjectAPICache.AcquireSubjectAPILease(ctx, appKeys)
	if err != nil {
		return nil, err
	}
	return &pausingSubjectAPILease{SubjectAPILease: lease, cache: c}, nil
}

type pausingSubjectAPILease struct {
	biz.SubjectAPILease
	cache *pausingSubjectAPICache
}

func (l *pausingSubjectAPILease) Block(ctx context.Context, appKeys []string) error {
	l.cache.once.Do(func() {
		close(l.cache.paused)
		<-l.cache.release
	})
	return l.SubjectAPILease.Block(ctx, appKeys)
}

// A rebuild that read the database before a revoke committed must never
// publish after the revoke's own rebuild: the revoked grant would come back.
func TestSubjectAPIStaleRebuildCannotOverwriteRevoke(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	if err := f.repo.ReplaceSubjectRoles(ctx, f.app, "alice", "", []uuid.UUID{f.role.ID}); err != nil {
		t.Fatal(err)
	}
	pausing := &pausingSubjectAPICache{SubjectAPICache: f.repo.cache, paused: make(chan struct{}), release: make(chan struct{})}
	f.repo.WithPolicyCache(pausing)

	// Save A (an unrelated binding) reads its snapshot, with alice still bound,
	// and pauses before publishing.
	slow := make(chan error, 1)
	go func() { slow <- f.repo.ReplaceSubjectRoles(ctx, f.app, "bob", "", []uuid.UUID{f.role.ID}) }()
	select {
	case <-pausing.paused:
	case <-time.After(10 * time.Second):
		t.Fatal("first rebuild never reached the pause point")
	}

	// Save B revokes alice and commits while A is paused.
	revoke := make(chan error, 1)
	go func() { revoke <- f.repo.ReplaceSubjectRoles(ctx, f.app, "alice", "", nil) }()
	time.Sleep(200 * time.Millisecond)
	close(pausing.release)
	for name, done := range map[string]chan error{"unrelated save": slow, "revoke": revoke} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never finished", name)
		}
	}
	if f.allowed(t, "alice") {
		t.Fatal("a stale rebuild republished alice's revoked grant")
	}
	if !f.allowed(t, "bob") {
		t.Fatal("bob's grant was lost")
	}
}
