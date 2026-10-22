package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type memoryMenuRepo struct{ menus map[uuid.UUID]*Menu }

type memoryRoleRepo struct {
	roles   map[string]*Role
	deleted []string
}

type memoryUserRoleRepo struct {
	subject         string
	serviceResource string
	roleIDs         []string
}

func (r *memoryUserRoleRepo) ReplaceRoles(_ context.Context, subject, serviceResource string, roleIDs []string) error {
	r.subject, r.serviceResource = subject, serviceResource
	r.roleIDs = append([]string(nil), roleIDs...)
	return nil
}

func (r *memoryUserRoleRepo) RoleIDs(_ context.Context, _, _ string) ([]string, error) {
	return append([]string(nil), r.roleIDs...), nil
}

func (r *memoryMenuRepo) Create(_ context.Context, menu *Menu) (*Menu, error) {
	menu.ID = uuid.New()
	r.menus[menu.ID] = menu
	return menu, nil
}
func (r *memoryMenuRepo) Get(_ context.Context, id uuid.UUID) (*Menu, error) {
	value, ok := r.menus[id]
	if !ok {
		return nil, ErrNotFound
	}
	return value, nil
}
func (r *memoryMenuRepo) ListByServiceResource(_ context.Context, serviceResource string) ([]*Menu, error) {
	values := []*Menu{}
	for _, value := range r.menus {
		if value.ServiceResource == serviceResource {
			values = append(values, value)
		}
	}
	return values, nil
}
func (r *memoryMenuRepo) Update(_ context.Context, menu *Menu) (*Menu, error) {
	if _, ok := r.menus[menu.ID]; !ok {
		return nil, ErrNotFound
	}
	r.menus[menu.ID] = menu
	return menu, nil
}
func (r *memoryMenuRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	menu, ok := r.menus[id]
	if !ok {
		return ErrNotFound
	}
	menu.IsDeleted = true
	menu.Enabled = false
	return nil
}

func (r *memoryRoleRepo) Create(_ context.Context, role *Role) (*Role, error) {
	if role.ID == "" {
		role.ID = "role-created"
	}
	if r.roles == nil {
		r.roles = map[string]*Role{}
	}
	r.roles[role.ID] = role
	return role, nil
}
func (r *memoryRoleRepo) Get(_ context.Context, id string) (*Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return nil, ErrNotFound
	}
	return role, nil
}
func (r *memoryRoleRepo) ListByServiceResource(_ context.Context, serviceResource string) ([]*Role, error) {
	roles := make([]*Role, 0)
	for _, role := range r.roles {
		if role.ServiceResource == serviceResource && !role.IsDeleted {
			roles = append(roles, role)
		}
	}
	return roles, nil
}
func (r *memoryRoleRepo) Update(_ context.Context, role *Role) (*Role, error) {
	if _, ok := r.roles[role.ID]; !ok {
		return nil, ErrNotFound
	}
	r.roles[role.ID] = role
	return role, nil
}
func (r *memoryRoleRepo) SoftDelete(_ context.Context, id string) error {
	role, ok := r.roles[id]
	if !ok {
		return ErrNotFound
	}
	role.IsDeleted = true
	role.Enabled = false
	r.deleted = append(r.deleted, id)
	return nil
}
func (r *memoryRoleRepo) ReplaceMenus(context.Context, string, []uuid.UUID) error { return nil }
func (r *memoryRoleRepo) MenuIDs(context.Context, string) ([]uuid.UUID, error)    { return nil, nil }

func TestCreateMenuRejectsButtonWithoutMenuParent(t *testing.T) {
	ctx := context.Background()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{}}
	service := NewPermissionService(nil, menus)
	if _, err := service.CreateMenu(ctx, &Menu{ServiceResource: "ops", Code: "create", Name: "Create", Type: MenuTypeButton, APIPath: "/v1/items"}); err == nil {
		t.Fatal("expected invalid root button")
	}
	menu, err := service.CreateMenu(ctx, &Menu{ServiceResource: "ops", Code: "items", Name: "Items", Type: MenuTypeMenu, Path: "/items"})
	if err != nil {
		t.Fatal(err)
	}
	button, err := service.CreateMenu(ctx, &Menu{ServiceResource: "ops", ParentID: &menu.ID, Code: "items:create", Name: "Create", Type: MenuTypeButton, APIPath: " /v1/items ", HTTPMethod: " post "})
	if err != nil {
		t.Fatal(err)
	}
	if button.ParentID == nil || *button.ParentID != menu.ID {
		t.Fatalf("button parent = %v", button.ParentID)
	}
	if button.APIPath != "/v1/items" || button.HTTPMethod != "POST" {
		t.Fatalf("button API metadata = %#v", button)
	}
	defaultMethodButton, err := service.CreateMenu(ctx, &Menu{ServiceResource: "ops", ParentID: &menu.ID, Code: "items:view", Name: "View", Type: MenuTypeButton, APIPath: "/v1/items/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	if defaultMethodButton.HTTPMethod != MenuHTTPMethodGet {
		t.Fatalf("default button HTTP method = %q", defaultMethodButton.HTTPMethod)
	}
}

func TestCreateMenuRejectsButtonWithoutAPIPath(t *testing.T) {
	parentID := uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		parentID: {ID: parentID, ServiceResource: "ops", Code: "items", Name: "Items", Type: MenuTypeMenu, Enabled: true},
	}}
	_, err := NewPermissionService(nil, menus).CreateMenu(context.Background(), &Menu{
		ServiceResource: "ops",
		ParentID:        &parentID,
		Code:            "items:create",
		Name:            "Create",
		Type:            MenuTypeButton,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing button API path error = %v", err)
	}
}

func TestCreateMenuRejectsMenuWithAPIPath(t *testing.T) {
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{}}
	_, err := NewPermissionService(nil, menus).CreateMenu(context.Background(), &Menu{
		ServiceResource: "ops",
		Code:            "items",
		Name:            "Items",
		Type:            MenuTypeMenu,
		APIPath:         "/v1/items",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("menu API path error = %v", err)
	}
}

func TestCreateMenuRejectsInvalidHTTPMethod(t *testing.T) {
	parentID := uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		parentID: {ID: parentID, ServiceResource: "ops", Code: "items", Name: "Items", Type: MenuTypeMenu, Enabled: true},
	}}
	service := NewPermissionService(nil, menus)
	_, err := service.CreateMenu(context.Background(), &Menu{
		ServiceResource: "ops",
		ParentID:        &parentID,
		Code:            "items:create",
		Name:            "Create",
		Type:            MenuTypeButton,
		APIPath:         "/v1/items",
		HTTPMethod:      "CONNECT",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid button HTTP method error = %v", err)
	}
	_, err = service.CreateMenu(context.Background(), &Menu{
		ServiceResource: "ops",
		Code:            "settings",
		Name:            "Settings",
		Type:            MenuTypeMenu,
		HTTPMethod:      MenuHTTPMethodGet,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("menu HTTP metadata error = %v", err)
	}
}

func TestMenuTreeBuildsHierarchy(t *testing.T) {
	rootID, childID := uuid.New(), uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		rootID:  {ID: rootID, ServiceResource: "admin", Type: MenuTypeMenu, Sort: 1},
		childID: {ID: childID, ServiceResource: "admin", ParentID: &rootID, Type: MenuTypeButton, Sort: 2},
	}}
	service := NewPermissionService(nil, menus)
	tree, err := service.MenuTree(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || len(tree[0].Children) != 1 || tree[0].Children[0].ID != childID {
		t.Fatalf("tree = %#v", tree)
	}
}

func TestUpdateRolePreservesImmutableAndCreationFields(t *testing.T) {
	roleID := "role-1"
	roles := &memoryRoleRepo{roles: map[string]*Role{
		roleID: {
			BaseFields:      BaseFields{CreatedByID: "creator", CreatedByName: "Creator", UpdatedByID: "old", UpdatedByName: "Old"},
			ID:              roleID,
			ServiceResource: "ops",
			Code:            "old-code",
			Name:            "Old name",
			Enabled:         false,
		},
	}}
	ctx := WithAuditActor(context.Background(), AuditActor{ID: "editor", Name: "Editor"})
	updated, err := NewPermissionService(roles, nil).UpdateRole(ctx, roleID, " new-code ", " New name ", " description ", true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != roleID || updated.ServiceResource != "ops" || updated.Code != "new-code" || updated.Name != "New name" || updated.Description != "description" || !updated.Enabled {
		t.Fatalf("updated role = %#v", updated)
	}
	if updated.CreatedByID != "creator" || updated.CreatedByName != "Creator" || updated.UpdatedByID != "editor" || updated.UpdatedByName != "Editor" {
		t.Fatalf("role audit fields = %#v", updated.BaseFields)
	}
}

func TestDeleteRoleValidatesIDAndDelegatesSoftDelete(t *testing.T) {
	roles := &memoryRoleRepo{roles: map[string]*Role{"role-1": {ID: "role-1", ServiceResource: "ops", Enabled: true}}}
	if err := NewPermissionService(roles, nil).DeleteRole(context.Background(), " role-1 "); err != nil {
		t.Fatal(err)
	}
	if len(roles.deleted) != 1 || roles.deleted[0] != "role-1" || !roles.roles["role-1"].IsDeleted {
		t.Fatalf("deleted roles = %#v", roles.deleted)
	}
	if err := NewPermissionService(roles, nil).DeleteRole(context.Background(), " "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid role id error = %v", err)
	}
}

func TestUpdateMenuPreservesImmutableAndCreationFields(t *testing.T) {
	parentID, menuID, otherParentID := uuid.New(), uuid.New(), uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		menuID: {
			BaseFields:      BaseFields{CreatedByID: "creator", CreatedByName: "Creator", UpdatedByID: "old", UpdatedByName: "Old"},
			ID:              menuID,
			ServiceResource: "ops",
			ParentID:        &parentID,
			Type:            MenuTypeButton,
			Code:            "old-code",
			Name:            "Old name",
			APIPath:         "/old",
			HTTPMethod:      "GET",
			Enabled:         true,
		},
	}}
	ctx := WithAuditActor(context.Background(), AuditActor{ID: "editor", Name: "Editor"})
	updated, err := NewPermissionService(nil, menus).UpdateMenu(ctx, &Menu{
		ID:              menuID,
		ServiceResource: "other-scope",
		ParentID:        &otherParentID,
		Type:            MenuTypeMenu,
		Code:            " new-code ",
		Name:            " New name ",
		Description:     " description ",
		Path:            "/new",
		Component:       "NewPage",
		APIPath:         "/new",
		HTTPMethod:      "POST",
		Icon:            "new-icon",
		Sort:            8,
		Enabled:         false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != menuID || updated.ServiceResource != "ops" || updated.ParentID == nil || *updated.ParentID != parentID || updated.Type != MenuTypeButton {
		t.Fatalf("immutable menu fields = %#v", updated)
	}
	if updated.Code != "new-code" || updated.Name != "New name" || updated.Description != "description" || updated.Path != "/new" || updated.Component != "NewPage" || updated.APIPath != "/new" || updated.HTTPMethod != "POST" || updated.Icon != "new-icon" || updated.Sort != 8 || updated.Enabled {
		t.Fatalf("mutable menu fields = %#v", updated)
	}
	if updated.CreatedByID != "creator" || updated.CreatedByName != "Creator" || updated.UpdatedByID != "editor" || updated.UpdatedByName != "Editor" {
		t.Fatalf("menu audit fields = %#v", updated.BaseFields)
	}
}

func TestDeleteMenuRejectsUndeletedChild(t *testing.T) {
	parentID, childID := uuid.New(), uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		parentID: {ID: parentID, ServiceResource: "ops", Type: MenuTypeMenu, Enabled: true},
		childID:  {ID: childID, ServiceResource: "ops", ParentID: &parentID, Type: MenuTypeButton, Enabled: false},
	}}
	if err := NewPermissionService(nil, menus).DeleteMenu(context.Background(), parentID); !errors.Is(err, ErrConflict) {
		t.Fatalf("child conflict error = %v", err)
	}
	if menus.menus[parentID].IsDeleted {
		t.Fatal("parent was deleted despite undeleted child")
	}
}

func TestDeleteMenuSoftDeletesLeaf(t *testing.T) {
	menuID := uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		menuID: {ID: menuID, ServiceResource: "ops", Type: MenuTypeMenu, Enabled: true},
	}}
	if err := NewPermissionService(nil, menus).DeleteMenu(context.Background(), menuID); err != nil {
		t.Fatal(err)
	}
	if !menus.menus[menuID].IsDeleted || menus.menus[menuID].Enabled {
		t.Fatalf("deleted menu = %#v", menus.menus[menuID])
	}
}

func TestListIncludesDisabledRecords(t *testing.T) {
	roleID := "disabled-role"
	roles := &memoryRoleRepo{roles: map[string]*Role{
		roleID: {ID: roleID, ServiceResource: "ops", Enabled: false},
	}}
	listedRoles, err := NewPermissionService(roles, nil).ListRoles(context.Background(), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if len(listedRoles) != 1 || listedRoles[0].ID != roleID || listedRoles[0].Enabled {
		t.Fatalf("listed roles = %#v", listedRoles)
	}

	menuID := uuid.New()
	menus := &memoryMenuRepo{menus: map[uuid.UUID]*Menu{
		menuID: {ID: menuID, ServiceResource: "ops", Type: MenuTypeMenu, Enabled: false},
	}}
	tree, err := NewPermissionService(nil, menus).MenuTree(context.Background(), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].ID != menuID || tree[0].Enabled {
		t.Fatalf("listed menu tree = %#v", tree)
	}
}

func TestReplaceUserRolesValidatesAndPreservesServiceResourceScope(t *testing.T) {
	userRoles := &memoryUserRoleRepo{}
	service := NewPermissionService(nil, nil, userRoles)
	if err := service.ReplaceUserRoles(context.Background(), " nexus-user-1 ", "admin", []string{"operator", "auditor"}); err != nil {
		t.Fatal(err)
	}
	if userRoles.subject != "nexus-user-1" || userRoles.serviceResource != "admin" {
		t.Fatalf("stored scope = %q / %q", userRoles.subject, userRoles.serviceResource)
	}
	if err := service.ReplaceUserRoles(context.Background(), "nexus-user-1", "admin", []string{"operator", "operator"}); err == nil {
		t.Fatal("expected duplicate role validation error")
	}
}

func TestAuditActorDefaultsAndNormalization(t *testing.T) {
	if actor := AuditActorFromContext(context.Background()); actor.ID != "system" || actor.Name != "system" {
		t.Fatalf("default actor = %#v", actor)
	}
	ctx := WithAuditActor(context.Background(), AuditActor{ID: " user-1 ", Name: " Alice "})
	actor := AuditActorFromContext(ctx)
	if actor.ID != "user-1" || actor.Name != "Alice" {
		t.Fatalf("normalized actor = %#v", actor)
	}
	base := NewBaseFields(actor)
	if base.CreatedByID != actor.ID || base.UpdatedByID != actor.ID || base.IsDeleted {
		t.Fatalf("base fields = %#v", base)
	}
}
