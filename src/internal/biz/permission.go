package biz

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// MenuType 表示菜单节点的类型。
type MenuType string

const (
	// MenuTypeMenu 表示可导航的菜单节点。
	MenuTypeMenu MenuType = "menu"
	// MenuTypeButton 表示菜单下的操作按钮节点。
	MenuTypeButton MenuType = "button"
)

// MenuHTTPMethod 表示按钮元数据允许记录的 HTTP 方法。
type MenuHTTPMethod string

const (
	MenuHTTPMethodGet     MenuHTTPMethod = "GET"
	MenuHTTPMethodPost    MenuHTTPMethod = "POST"
	MenuHTTPMethodPut     MenuHTTPMethod = "PUT"
	MenuHTTPMethodPatch   MenuHTTPMethod = "PATCH"
	MenuHTTPMethodDelete  MenuHTTPMethod = "DELETE"
	MenuHTTPMethodHead    MenuHTTPMethod = "HEAD"
	MenuHTTPMethodOptions MenuHTTPMethod = "OPTIONS"
)

// Role 表示按服务资源作用域归属的角色及其审计信息。
type Role struct {
	BaseFields
	ID              string `json:"id"`
	ServiceResource string `json:"service_resource"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Enabled         bool   `json:"enabled"`
}

// Menu 表示导航菜单或操作按钮；根节点的 ParentID 为 nil。
type Menu struct {
	BaseFields
	ID              uuid.UUID      `json:"id"`
	ServiceResource string         `json:"service_resource"`
	ParentID        *uuid.UUID     `json:"parent_id"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Type            MenuType       `json:"type"`
	Path            string         `json:"path"`
	Component       string         `json:"component"`
	APIPath         string         `json:"api_path"`
	HTTPMethod      MenuHTTPMethod `json:"http_method"`
	Icon            string         `json:"icon"`
	Sort            int            `json:"sort"`
	Enabled         bool           `json:"enabled"`
}

// MenuTreeNode 表示包含子节点的菜单树节点。
type MenuTreeNode struct {
	Menu
	Children []*MenuTreeNode `json:"children"`
}

// RoleRepository 定义角色及其菜单关联的持久化操作。
type RoleRepository interface {
	Create(context.Context, *Role) (*Role, error)
	Get(context.Context, string) (*Role, error)
	ListByServiceResource(context.Context, string) ([]*Role, error)
	Update(context.Context, *Role) (*Role, error)
	SoftDelete(context.Context, string) error
	ReplaceMenus(context.Context, string, []uuid.UUID) error
	MenuIDs(context.Context, string) ([]uuid.UUID, error)
}

// MenuRepository 定义菜单的持久化操作。
type MenuRepository interface {
	Create(context.Context, *Menu) (*Menu, error)
	Get(context.Context, uuid.UUID) (*Menu, error)
	ListByServiceResource(context.Context, string) ([]*Menu, error)
	Update(context.Context, *Menu) (*Menu, error)
	SoftDelete(context.Context, uuid.UUID) error
}

// UserRoleRepository 定义外部 OIDC subject 与服务资源作用域角色的关联操作。
// 该接口不维护本地用户表，身份权威来源为 NexusAuth。
type UserRoleRepository interface {
	ReplaceRoles(context.Context, string, string, []string) error
	RoleIDs(context.Context, string, string) ([]string, error)
}

// PermissionService 提供角色、菜单和用户角色关联的权限业务操作。
// 所有权限校验均按服务资源作用域执行。
type PermissionService struct {
	roles            RoleRepository
	menus            MenuRepository
	userRoles        UserRoleRepository
	serviceResources ServiceResourceCatalog
}

// WithServiceResourceCatalog 配置服务资源目录作为作用域校验的权威来源。
func (s *PermissionService) WithServiceResourceCatalog(catalog ServiceResourceCatalog) *PermissionService {
	s.serviceResources = catalog
	return s
}

// ensureServiceResource 校验服务资源存在且处于启用状态；未配置目录时跳过校验。
func (s *PermissionService) ensureServiceResource(ctx context.Context, serviceResource string) error {
	if s.serviceResources != nil {
		value, err := s.serviceResources.Get(ctx, serviceResource)
		if err != nil {
			return err
		}
		if !value.IsActive {
			return fmt.Errorf("%w: service resource is disabled", ErrConflict)
		}
		return nil
	}
	return nil
}

// NewPermissionService 创建权限业务服务，并注入角色、菜单及可选的用户角色仓储。
func NewPermissionService(roles RoleRepository, menus MenuRepository, userRoles ...UserRoleRepository) *PermissionService {
	service := &PermissionService{roles: roles, menus: menus}
	if len(userRoles) > 0 {
		service.userRoles = userRoles[0]
	}
	return service
}

// CreateRole 在指定服务资源作用域创建启用的角色，并校验资源状态、编码和名称。
func (s *PermissionService) CreateRole(ctx context.Context, serviceResource, code, name, description string) (*Role, error) {
	serviceResource, err := validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	code, name, err = validateCodeAndName(code, name)
	if err != nil {
		return nil, err
	}
	return s.roles.Create(ctx, &Role{BaseFields: NewBaseFields(AuditActorFromContext(ctx)), ServiceResource: serviceResource, Code: code, Name: name, Description: strings.TrimSpace(description), Enabled: true})
}

// ListRoles 返回指定服务资源作用域下的全部角色。
func (s *PermissionService) ListRoles(ctx context.Context, serviceResource string) ([]*Role, error) {
	serviceResource, err := validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	return s.roles.ListByServiceResource(ctx, serviceResource)
}

// UpdateRole 更新角色的可变字段，并保留服务资源、角色 ID 和创建审计字段。
func (s *PermissionService) UpdateRole(ctx context.Context, roleID, code, name, description string, enabled bool) (*Role, error) {
	roleID, err := validateID(roleID, "role_id")
	if err != nil {
		return nil, err
	}
	existing, err := s.roles.Get(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNotFound
	}
	if err := s.ensureServiceResource(ctx, existing.ServiceResource); err != nil {
		return nil, err
	}
	code, name, err = validateCodeAndName(code, name)
	if err != nil {
		return nil, err
	}
	updated := *existing
	updated.ID = roleID
	updated.Code = code
	updated.Name = name
	updated.Description = strings.TrimSpace(description)
	updated.Enabled = enabled
	actor := AuditActorFromContext(ctx)
	updated.UpdatedByID = actor.ID
	updated.UpdatedByName = actor.Name
	return s.roles.Update(ctx, &updated)
}

// DeleteRole 软删除角色及其关联授权。
func (s *PermissionService) DeleteRole(ctx context.Context, roleID string) error {
	roleID, err := validateID(roleID, "role_id")
	if err != nil {
		return err
	}
	return s.roles.SoftDelete(ctx, roleID)
}

// CreateMenu 创建菜单或操作按钮，并校验服务资源作用域及父菜单归属。
func (s *PermissionService) CreateMenu(ctx context.Context, menu *Menu) (*Menu, error) {
	if menu == nil {
		return nil, fmt.Errorf("%w: menu is required", ErrInvalidArgument)
	}
	serviceResource, err := validateServiceResource(menu.ServiceResource)
	if err != nil {
		return nil, err
	}
	menu.ServiceResource = serviceResource
	if err := s.ensureServiceResource(ctx, serviceResource); err != nil {
		return nil, err
	}
	code, name, err := validateCodeAndName(menu.Code, menu.Name)
	if err != nil {
		return nil, err
	}
	menu.Code, menu.Name = code, name
	if err := validateMenu(menu); err != nil {
		return nil, err
	}
	if menu.ParentID != nil {
		parent, err := s.menus.Get(ctx, *menu.ParentID)
		if err != nil {
			return nil, err
		}
		if parent.ServiceResource != serviceResource || parent.Type != MenuTypeMenu || !parent.Enabled {
			return nil, fmt.Errorf("%w: parent must be an enabled menu in the same service resource", ErrConflict)
		}
	}
	menu.Enabled = true
	menu.BaseFields = NewBaseFields(AuditActorFromContext(ctx))
	return s.menus.Create(ctx, menu)
}

// UpdateMenu 更新菜单的可变字段，并保留服务资源、父节点、类型和创建审计字段。
func (s *PermissionService) UpdateMenu(ctx context.Context, menu *Menu) (*Menu, error) {
	if menu == nil || menu.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: menu id is required", ErrInvalidArgument)
	}
	existing, err := s.menus.Get(ctx, menu.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNotFound
	}
	if err := s.ensureServiceResource(ctx, existing.ServiceResource); err != nil {
		return nil, err
	}
	code, name, err := validateCodeAndName(menu.Code, menu.Name)
	if err != nil {
		return nil, err
	}
	updated := *menu
	updated.ID = existing.ID
	updated.ServiceResource = existing.ServiceResource
	updated.ParentID = existing.ParentID
	updated.Type = existing.Type
	updated.Code = code
	updated.Name = name
	updated.Description = strings.TrimSpace(menu.Description)
	updated.BaseFields = existing.BaseFields
	if err := validateMenu(&updated); err != nil {
		return nil, err
	}
	actor := AuditActorFromContext(ctx)
	updated.UpdatedByID = actor.ID
	updated.UpdatedByName = actor.Name
	return s.menus.Update(ctx, &updated)
}

// DeleteMenu 软删除没有未删除子节点的菜单及其角色关联。
func (s *PermissionService) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: menu id is required", ErrInvalidArgument)
	}
	existing, err := s.menus.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	menus, err := s.menus.ListByServiceResource(ctx, existing.ServiceResource)
	if err != nil {
		return err
	}
	for _, child := range menus {
		if child == nil || child.IsDeleted || child.ParentID == nil || child.ID == id {
			continue
		}
		if *child.ParentID == id {
			return fmt.Errorf("%w: menu has undeleted child nodes", ErrConflict)
		}
	}
	return s.menus.SoftDelete(ctx, id)
}

// MenuTree 返回指定服务资源作用域下按父子关系组装的菜单树。
func (s *PermissionService) MenuTree(ctx context.Context, serviceResource string) ([]*MenuTreeNode, error) {
	serviceResource, err := validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	menus, err := s.menus.ListByServiceResource(ctx, serviceResource)
	if err != nil {
		return nil, err
	}
	nodes := make(map[uuid.UUID]*MenuTreeNode, len(menus))
	for _, menu := range menus {
		nodes[menu.ID] = &MenuTreeNode{Menu: *menu}
	}
	roots := make([]*MenuTreeNode, 0)
	for _, menu := range menus {
		node := nodes[menu.ID]
		if menu.ParentID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*menu.ParentID]
		if !ok {
			return nil, fmt.Errorf("%w: menu parent does not belong to service resource", ErrConflict)
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, nil
}

// GrantRoleMenus 将指定菜单集合授权给角色，并要求角色与菜单属于同一服务资源作用域。
func (s *PermissionService) GrantRoleMenus(ctx context.Context, roleID string, menuIDs []uuid.UUID) error {
	roleID, err := validateID(roleID, "role_id")
	if err != nil {
		return err
	}
	role, err := s.roles.Get(ctx, roleID)
	if err != nil {
		return err
	}
	if role == nil || !role.Enabled {
		return fmt.Errorf("%w: role must be enabled", ErrConflict)
	}
	seen := make(map[uuid.UUID]struct{}, len(menuIDs))
	for _, menuID := range menuIDs {
		if menuID == uuid.Nil {
			return fmt.Errorf("%w: menu_id is required", ErrInvalidArgument)
		}
		if _, ok := seen[menuID]; ok {
			return fmt.Errorf("%w: duplicate menu_id", ErrInvalidArgument)
		}
		seen[menuID] = struct{}{}
		menu, err := s.menus.Get(ctx, menuID)
		if err != nil {
			return err
		}
		if menu == nil || !menu.Enabled {
			return fmt.Errorf("%w: menu must be enabled", ErrConflict)
		}
		if menu.ServiceResource != role.ServiceResource {
			return fmt.Errorf("%w: role and menu must belong to the same service resource", ErrConflict)
		}
	}
	return s.roles.ReplaceMenus(ctx, roleID, menuIDs)
}

// RoleMenus 返回角色已关联的菜单 ID 列表，并在查询前校验角色存在。
func (s *PermissionService) RoleMenus(ctx context.Context, roleID string) ([]uuid.UUID, error) {
	roleID, err := validateID(roleID, "role_id")
	if err != nil {
		return nil, err
	}
	if _, err := s.roles.Get(ctx, roleID); err != nil {
		return nil, err
	}
	return s.roles.MenuIDs(ctx, roleID)
}

// ReplaceUserRoles 原子替换用户在指定服务资源作用域下的角色集合。
// 角色 ID 的有效性及其作用域归属由仓储进一步校验。
func (s *PermissionService) ReplaceUserRoles(ctx context.Context, subject, serviceResource string, roleIDs []string) error {
	if s.userRoles == nil {
		return fmt.Errorf("user role repository is not configured")
	}
	subject, err := validateID(subject, "user_id")
	if err != nil {
		return err
	}
	serviceResource, err = validateServiceResource(serviceResource)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(roleIDs))
	for index, roleID := range roleIDs {
		roleID, err = validateID(roleID, "role_id")
		if err != nil {
			return err
		}
		if _, exists := seen[roleID]; exists {
			return fmt.Errorf("%w: duplicate role_id", ErrInvalidArgument)
		}
		seen[roleID] = struct{}{}
		roleIDs[index] = roleID
	}
	return s.userRoles.ReplaceRoles(ctx, subject, serviceResource, roleIDs)
}

// UserRoleIDs 返回用户在指定服务资源作用域下关联的角色 ID 列表。
func (s *PermissionService) UserRoleIDs(ctx context.Context, subject, serviceResource string) ([]string, error) {
	if s.userRoles == nil {
		return nil, fmt.Errorf("user role repository is not configured")
	}
	subject, err := validateID(subject, "user_id")
	if err != nil {
		return nil, err
	}
	serviceResource, err = validateServiceResource(serviceResource)
	if err != nil {
		return nil, err
	}
	return s.userRoles.RoleIDs(ctx, subject, serviceResource)
}

// validateCodeAndName 校验并规范角色或菜单的编码和名称。
func validateCodeAndName(code, name string) (string, string, error) {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	if code == "" || len(code) > 100 {
		return "", "", fmt.Errorf("%w: code must be 1-100 characters", ErrInvalidArgument)
	}
	if name == "" || len([]rune(name)) > 100 {
		return "", "", fmt.Errorf("%w: name must be 1-100 characters", ErrInvalidArgument)
	}
	return code, name, nil
}

// validateID 校验并规范业务对象的字符串标识符。
func validateID(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return "", fmt.Errorf("%w: %s must be 1-80 characters", ErrInvalidArgument, name)
	}
	return value, nil
}

// validateMenu 校验菜单类型以及按钮父节点和 API 路径配置。
func validateMenu(menu *Menu) error {
	code, name, err := validateCodeAndName(menu.Code, menu.Name)
	if err != nil {
		return err
	}
	menu.Code, menu.Name = code, name
	if menu.Type != MenuTypeMenu && menu.Type != MenuTypeButton {
		return fmt.Errorf("%w: type must be menu or button", ErrInvalidArgument)
	}
	if menu.Type == MenuTypeButton && menu.ParentID == nil {
		return fmt.Errorf("%w: button must have a menu parent", ErrInvalidArgument)
	}
	menu.APIPath = strings.TrimSpace(menu.APIPath)
	menu.HTTPMethod = MenuHTTPMethod(strings.ToUpper(strings.TrimSpace(string(menu.HTTPMethod))))
	if menu.Type == MenuTypeMenu && (menu.APIPath != "" || menu.HTTPMethod != "") {
		return fmt.Errorf("%w: menu cannot define API metadata", ErrInvalidArgument)
	}
	if menu.Type == MenuTypeButton && menu.APIPath == "" {
		return fmt.Errorf("%w: button api_path is required", ErrInvalidArgument)
	}
	if menu.Type == MenuTypeButton && menu.HTTPMethod == "" {
		menu.HTTPMethod = MenuHTTPMethodGet
	}
	if menu.Type == MenuTypeButton && !menu.HTTPMethod.Valid() {
		return fmt.Errorf("%w: unsupported button http_method %q", ErrInvalidArgument, menu.HTTPMethod)
	}
	return nil
}

// Valid 判断按钮 HTTP 方法是否属于受支持的枚举集合。
func (m MenuHTTPMethod) Valid() bool {
	switch m {
	case MenuHTTPMethodGet, MenuHTTPMethodPost, MenuHTTPMethodPut, MenuHTTPMethodPatch,
		MenuHTTPMethodDelete, MenuHTTPMethodHead, MenuHTTPMethodOptions:
		return true
	default:
		return false
	}
}
