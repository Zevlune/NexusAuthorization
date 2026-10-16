package repo

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

// TestApplicationMenuButtonRulesIntegration verifies the persisted menu tree
// invariants: menus/directories nest to any depth, buttons are always leaves,
// and nothing may be attached below a button. It requires an isolated test
// database initialized with migrations/001_init.sql.
func TestApplicationMenuButtonRulesIntegration(t *testing.T) {
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
	r := NewRBACV1Repository(pool)

	appKey := "menu-button-" + uuid.NewString()
	registerMenuAppCleanup(t, ctx, pool, appKey)
	if _, err = r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Menu Button Rules", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	save := func(m biz.ApplicationMenu) biz.ApplicationMenu {
		t.Helper()
		saved, err := r.SaveApplicationMenu(ctx, appKey, m)
		if err != nil {
			t.Fatalf("save %q (%s): %v", m.Code, m.MenuType, err)
		}
		return saved
	}

	// Menus and directories may nest to arbitrary depth.
	dir := save(biz.ApplicationMenu{Code: "root", Name: "Root", MenuType: "directory", Enabled: true})
	level1 := save(biz.ApplicationMenu{Code: "l1", Name: "L1", MenuType: "menu", ParentID: &dir.ID, Enabled: true})
	level2 := save(biz.ApplicationMenu{Code: "l2", Name: "L2", MenuType: "menu", ParentID: &level1.ID, Enabled: true})
	level3 := save(biz.ApplicationMenu{Code: "l3", Name: "L3", MenuType: "directory", ParentID: &level2.ID, Enabled: true})
	leafMenu := save(biz.ApplicationMenu{Code: "leaf", Name: "Leaf", MenuType: "menu", ParentID: &dir.ID, Enabled: true})

	// A leaf button below a menu is accepted.
	button := save(biz.ApplicationMenu{Code: "create", Name: "Create", MenuType: "button", ParentID: &level1.ID, Enabled: true})

	// Nothing may be attached below a button: neither a menu nor another button.
	if _, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "menu-under-button", Name: "Nested", MenuType: "menu", ParentID: &button.ID, Enabled: true}); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("menu under button accepted: %v", err)
	}
	if _, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "button-under-button", Name: "Nested", MenuType: "button", ParentID: &button.ID, Enabled: true}); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("button under button accepted: %v", err)
	}

	// A menu/directory that already has children cannot be converted into a button.
	toButton := dir
	toButton.MenuType = "button"
	if _, err := r.SaveApplicationMenu(ctx, appKey, toButton); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("directory with children converted to button: %v", err)
	}
	toButton = level1
	toButton.MenuType = "button"
	if _, err := r.SaveApplicationMenu(ctx, appKey, toButton); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("menu with children converted to button: %v", err)
	}

	// A leaf menu can become a button.
	leafMenu.MenuType = "button"
	converted, err := r.SaveApplicationMenu(ctx, appKey, leafMenu)
	if err != nil {
		t.Fatalf("leaf menu could not become a button: %v", err)
	}
	if converted.MenuType != "button" {
		t.Fatalf("leaf menu not converted to button: %+v", converted)
	}

	// Reparenting an existing node below a button must be rejected as well.
	level3.ParentID = &button.ID
	if _, err := r.SaveApplicationMenu(ctx, appKey, level3); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("reparent below button accepted: %v", err)
	}

	verifyTreeInvariants(t, ctx, r, appKey)
}

// TestApplicationMenuButtonConcurrencyIntegration checks the button-leaf rule
// under contention. "list" starts empty and "detail" starts as its sibling, so
// converting "list" to a button and re-parenting "detail" under "list" are in
// genuine conflict: whichever writer commits first must cause the other to be
// rejected. The application FOR UPDATE lock serializes them; without it both
// could commit and leave a button with a child.
func TestApplicationMenuButtonConcurrencyIntegration(t *testing.T) {
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
	r := NewRBACV1Repository(pool)

	// seed creates an isolated tree where "list" has no children and "detail"
	// is a sibling of "list":
	//   orders
	//   |- list    (candidate to become a button)
	//   |- detail  (candidate to move under list)
	seed := func(t *testing.T) (appKey string, list, detail biz.ApplicationMenu) {
		t.Helper()
		appKey = "menu-button-race-" + uuid.NewString()
		registerMenuAppCleanup(t, ctx, pool, appKey)
		if _, err := r.SaveApplication(ctx, biz.Application{Key: appKey, DisplayName: "Menu Button Race", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		orders, err := r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "orders", Name: "Orders", MenuType: "menu", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		list, err = r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "list", Name: "List", MenuType: "menu", ParentID: &orders.ID, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		detail, err = r.SaveApplicationMenu(ctx, appKey, biz.ApplicationMenu{Code: "detail", Name: "Detail", MenuType: "menu", ParentID: &orders.ID, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return appKey, list, detail
	}

	// Deterministic serial order 1: "list" becomes a button first, so moving
	// "detail" under it must be rejected.
	t.Run("button-then-reparent", func(t *testing.T) {
		appKey, list, detail := seed(t)
		list.MenuType = "button"
		if _, err := r.SaveApplicationMenu(ctx, appKey, list); err != nil {
			t.Fatalf("list should convert to a button while empty: %v", err)
		}
		detail.ParentID = &list.ID
		if _, err := r.SaveApplicationMenu(ctx, appKey, detail); !errors.Is(err, biz.ErrInvalidArgument) {
			t.Fatalf("reparent under button accepted: %v", err)
		}
		verifyTreeInvariants(t, ctx, r, appKey)
	})

	// Deterministic serial order 2: "detail" moves under "list" first, so
	// converting "list" into a button must be rejected.
	t.Run("reparent-then-button", func(t *testing.T) {
		appKey, list, detail := seed(t)
		detail.ParentID = &list.ID
		if _, err := r.SaveApplicationMenu(ctx, appKey, detail); err != nil {
			t.Fatalf("initial reparent under menu should succeed: %v", err)
		}
		list.MenuType = "button"
		if _, err := r.SaveApplicationMenu(ctx, appKey, list); !errors.Is(err, biz.ErrInvalidArgument) {
			t.Fatalf("menu with child converted to button: %v", err)
		}
		verifyTreeInvariants(t, ctx, r, appKey)
	})

	// Concurrent race: exactly one writer must win for each interleaving.
	t.Run("concurrent", func(t *testing.T) {
		appKey, list, detail := seed(t)

		// Hold a row lock on "list" so both writers run their validation reads
		// before either can write. With the application FOR UPDATE lock only
		// one writer is inside its transaction at a time, so one commits and
		// the other must be rejected. Without that lock both could read stale
		// state and commit, leaving a button with a child.
		blocker, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		lockTx, err := blocker.Begin(ctx)
		if err != nil {
			blocker.Release()
			t.Fatal(err)
		}
		if _, err := lockTx.Exec(ctx, `SELECT id FROM menus WHERE id=$1 FOR UPDATE`, list.ID); err != nil {
			_ = lockTx.Rollback(ctx)
			blocker.Release()
			t.Fatal(err)
		}

		toButton := list
		toButton.MenuType = "button"
		reparent := detail
		reparent.ParentID = &list.ID

		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make(chan error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, e := r.SaveApplicationMenu(ctx, appKey, toButton); results <- e }()
		go func() { defer wg.Done(); <-start; _, e := r.SaveApplicationMenu(ctx, appKey, reparent); results <- e }()
		close(start)

		// Let both writers finish their validation reads and block on the held
		// row lock before releasing it.
		time.Sleep(250 * time.Millisecond)
		if err := lockTx.Rollback(ctx); err != nil {
			t.Errorf("release row lock: %v", err)
		}
		blocker.Release()

		wg.Wait()
		close(results)

		successes, rejections := 0, 0
		for e := range results {
			switch {
			case e == nil:
				successes++
			case errors.Is(e, biz.ErrInvalidArgument):
				rejections++
			default:
				t.Fatalf("unexpected concurrent save error: %v", e)
			}
		}
		if successes != 1 || rejections != 1 {
			t.Fatalf("race not serialized: successes=%d rejections=%d", successes, rejections)
		}
		verifyTreeInvariants(t, ctx, r, appKey)
	})
}

func verifyTreeInvariants(t *testing.T, ctx context.Context, r *RBACV1Repository, appKey string) {
	t.Helper()
	items, err := r.ListApplicationMenus(ctx, appKey)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]biz.ApplicationMenu{}
	childCount := map[uuid.UUID]int{}
	for _, item := range items {
		byID[item.ID] = item
		if item.ParentID != nil {
			childCount[*item.ParentID]++
		}
	}
	for _, item := range items {
		if item.ParentID != nil {
			if parent, ok := byID[*item.ParentID]; ok && parent.MenuType == "button" {
				t.Fatalf("menu %q is attached below a button parent", item.Code)
			}
		}
		if item.MenuType == "button" && childCount[item.ID] > 0 {
			t.Fatalf("button %q has child nodes", item.Code)
		}
	}
}

// registerMenuAppCleanup removes one isolated test application and fails the
// test if any step errors. Menus are deleted deepest-first so the RESTRICT
// parent foreign key never blocks the batch, then the application delete
// cascades roles, bindings and releases. Finally it verifies the application
// is gone so leaked test data cannot go unnoticed.
func registerMenuAppCleanup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appKey string) {
	t.Helper()
	t.Cleanup(func() {
		for {
			tag, err := pool.Exec(ctx, `
				DELETE FROM menus AS m
				WHERE m.application_id IN (SELECT id FROM applications WHERE app_key=$1)
				  AND NOT EXISTS (
					SELECT 1 FROM menus AS c
					WHERE c.parent_id = m.id AND c.application_id = m.application_id
				  )`, appKey)
			if err != nil {
				t.Errorf("cleanup menus for %s: %v", appKey, err)
				return
			}
			if tag.RowsAffected() == 0 {
				break
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM applications WHERE app_key=$1`, appKey); err != nil {
			t.Errorf("cleanup application %s: %v", appKey, err)
			return
		}
		var remaining int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM applications WHERE app_key=$1`, appKey).Scan(&remaining); err != nil {
			t.Errorf("verify cleanup of %s: %v", appKey, err)
			return
		}
		if remaining != 0 {
			t.Errorf("test application %s still present after cleanup", appKey)
		}
	})
}
