package repo

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

type blockingServiceCatalog struct {
	resource *biz.ServiceResource
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (c *blockingServiceCatalog) List(ctx context.Context) ([]*biz.ServiceResource, error) {
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.release:
		return []*biz.ServiceResource{c.resource}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *blockingServiceCatalog) Get(ctx context.Context, key string) (*biz.ServiceResource, error) {
	values, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.Key == key {
			return value, nil
		}
	}
	return nil, biz.ErrNotFound
}

func TestReplaceApplicationServicesReadsRemoteCatalogBeforeLockingApplication(t *testing.T) {
	dsn := os.Getenv("NEXUSAUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEXUSAUTHORIZATION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := newRepositoryTestPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()
	appKey := "remote-lock-" + suffix
	serviceKey := "remote-service-" + suffix
	remote := &blockingServiceCatalog{
		resource: &biz.ServiceResource{Key: serviceKey, Name: serviceKey, DisplayName: serviceKey, IsActive: true},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service, err := biz.NewServiceResourceCatalog(NewServiceResourceRepository(pool), remote, biz.ServiceResourceSourceNexusAuth)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRBACV1Repository(pool, service)
	if _, err := repository.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Remote Lock", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey)
	})

	replaceDone := make(chan error, 1)
	go func() {
		replaceDone <- repository.ReplaceApplicationServices(ctx, appKey, []string{})
	}()
	select {
	case <-remote.started:
	case <-time.After(2 * time.Second):
		t.Fatal("remote catalog was not read")
	}

	updateCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	updatedDisplayName := "Updated Without Row Lock"
	_, updateErr := pool.Exec(updateCtx, `UPDATE applications SET display_name=$2 WHERE app_key=$1`, appKey, updatedDisplayName)
	close(remote.release)
	select {
	case replaceErr := <-replaceDone:
		if replaceErr != nil {
			t.Fatalf("replace application services: %v", replaceErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replace application services did not finish")
	}
	if updateErr != nil {
		t.Fatalf("application was locked during remote read: %v", updateErr)
	}
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM applications WHERE app_key=$1`, appKey).Scan(&displayName); err != nil {
		t.Fatal(err)
	}
	if displayName != updatedDisplayName {
		t.Fatalf("display name = %q, want %q", displayName, updatedDisplayName)
	}
}
