package repo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

func TestSubjectMenusDoNotDependOnRemoteServiceCatalog(t *testing.T) {
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
	catalog := &changingServiceCatalog{}
	r := NewRBACV1Repository(pool, catalog)
	appKey := "menu-policy-" + uuid.NewString()
	registerMenuAppCleanup(t, ctx, pool, appKey)
	app, err := r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Menus", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "menu", SortOrder: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	button, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "create", Name: "Create", MenuType: "button", ParentID: &parent.ID, SortOrder: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	role, err := r.SaveRole(ctx, appKey, biz.RBACRole{Code: "editor", Name: "Editor", Enabled: true, MenuIDs: []uuid.UUID{button.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceSubjectRoles(ctx, appKey, "alice", "", []uuid.UUID{role.ID}); err != nil {
		t.Fatal(err)
	}
	catalog.err = errors.New("remote catalog is unavailable")
	service := biz.NewRBACV1Service(r)
	menus, err := service.SubjectMenus(ctx, appKey, "alice", "global")
	if err != nil || len(menus) != 2 {
		t.Fatalf("menus must survive a catalog outage: %+v, %v", menus, err)
	}
	for i, want := range []uuid.UUID{parent.ID, button.ID} {
		if menus[i].ID != want || menus[i].ApplicationID != app.ID {
			t.Fatalf("menu %d = %+v, want %s in %s", i, menus[i], want, app.ID)
		}
	}
	for _, identity := range []struct{ subject, domain string }{{"bob", ""}, {"alice", "tenant-a"}} {
		menus, err := service.SubjectMenus(ctx, appKey, identity.subject, identity.domain)
		if err != nil || len(menus) != 0 {
			t.Fatalf("menu identity boundary %+v: %+v, %v", identity, menus, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE menus SET enabled=FALSE WHERE id=$1`, parent.ID); err != nil {
		t.Fatal(err)
	}
	if menus, err := service.SubjectMenus(ctx, appKey, "alice", ""); err != nil || len(menus) != 0 {
		t.Fatalf("disabled ancestor must hide its branch: %+v, %v", menus, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE applications SET enabled=FALSE WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubjectMenus(ctx, appKey, "alice", ""); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("disabled application: %v", err)
	}
	if _, err := service.SubjectMenus(ctx, "missing-"+appKey, "alice", ""); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("missing application: %v", err)
	}
}
