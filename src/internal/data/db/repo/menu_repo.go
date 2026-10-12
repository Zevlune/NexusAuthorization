package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

const menuColumns = `id, service_resource, parent_id, menu_type, code, name, description,
	COALESCE(route, ''), COALESCE(component, ''), COALESCE(action, ''),
	COALESCE(http_method, ''), COALESCE(icon, ''), sort_order, metadata,
	enabled, created_by_id, created_by_name, created_at, updated_by_id, updated_by_name,
	updated_at, is_deleted`

// MenuRepository 保存菜单和按钮节点，数据库负责父子节点的服务资源约束。
type MenuRepository struct {
	pool *pgxpool.Pool
}

func NewMenuRepository(pool *pgxpool.Pool) *MenuRepository {
	return &MenuRepository{pool: pool}
}

func nullableMenuHTTPMethod(method biz.MenuHTTPMethod) any {
	if method == "" {
		return nil
	}
	return string(method)
}

func (r *MenuRepository) Create(ctx context.Context, menu *biz.Menu) (*biz.Menu, error) {
	if menu == nil || strings.TrimSpace(menu.ServiceResource) == "" {
		return nil, fmt.Errorf("%w: menu and service_resource are required", biz.ErrInvalidArgument)
	}
	scope := strings.TrimSpace(menu.ServiceResource)
	menuID := menu.ID
	if menuID == uuid.Nil {
		menuID = uuid.New()
	}
	const query = `
		INSERT INTO menus (
			id, service_resource, parent_id, menu_type, code, name, description,
			route, component, action, http_method, icon, sort_order, metadata, enabled,
			created_by_id, created_by_name, updated_by_id, updated_by_name, is_deleted
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, '{}'::JSONB,
			$14, $15, $16, $15, $16, FALSE)
		RETURNING ` + menuColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanMenu(r.pool.QueryRow(ctx, query,
		menuID,
		scope,
		menu.ParentID,
		string(menu.Type),
		menu.Code,
		menu.Name,
		menu.Description,
		menu.Path,
		menu.Component,
		menu.APIPath,
		nullableMenuHTTPMethod(menu.HTTPMethod),
		menu.Icon,
		menu.Sort,
		menu.Enabled,
		actor.ID,
		actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizMenu(value), nil
}

func (r *MenuRepository) Get(ctx context.Context, id uuid.UUID) (*biz.Menu, error) {
	const query = `
		SELECT ` + menuColumns + `
		FROM menus
		WHERE id = $1 AND is_deleted = FALSE`
	value, err := scanMenu(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizMenu(value), nil
}

func (r *MenuRepository) ListByServiceResource(ctx context.Context, serviceResource string) ([]*biz.Menu, error) {
	const query = `
		SELECT ` + menuColumns + `
		FROM menus
		WHERE service_resource = $1 AND is_deleted = FALSE
		ORDER BY sort_order, name, id`
	rows, err := r.pool.Query(ctx, query, serviceResource)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	menus := make([]*biz.Menu, 0)
	for rows.Next() {
		value, err := scanMenu(rows)
		if err != nil {
			return nil, mapDBError(err)
		}
		menus = append(menus, toBizMenu(value))
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return menus, nil
}

// Update 更新菜单可变信息，不改变服务资源、父节点和类型，并写入更新审计信息。
func (r *MenuRepository) Update(ctx context.Context, menu *biz.Menu) (*biz.Menu, error) {
	if menu == nil || menu.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: menu id is required", biz.ErrInvalidArgument)
	}
	const query = `
		UPDATE menus
		SET code = $2, name = $3, description = $4, route = $5, component = $6,
			action = $7, http_method = $8, icon = $9, sort_order = $10, enabled = $11,
			updated_by_id = $12, updated_by_name = $13, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING ` + menuColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanMenu(r.pool.QueryRow(ctx, query,
		menu.ID, menu.Code, menu.Name, menu.Description, menu.Path,
		menu.Component, menu.APIPath, nullableMenuHTTPMethod(menu.HTTPMethod), menu.Icon, menu.Sort,
		menu.Enabled, actor.ID, actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizMenu(value), nil
}

func (r *MenuRepository) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*biz.Menu, error) {
	const query = `
		UPDATE menus
		SET enabled = $2, updated_by_id = $3, updated_by_name = $4, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING ` + menuColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanMenu(r.pool.QueryRow(ctx, query, id, enabled, actor.ID, actor.Name))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizMenu(value), nil
}

// SoftDelete 在一个事务中软删除菜单及其角色菜单关联，并拒绝仍有子节点的菜单。
func (r *MenuRepository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	actor := biz.AuditActorFromContext(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT TRUE
		FROM menus
		WHERE id = $1 AND is_deleted = FALSE
		FOR UPDATE`, id).Scan(&exists); err != nil {
		return mapDBError(err)
	}
	var hasChildren bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM menus
			WHERE parent_id = $1 AND is_deleted = FALSE
		)`, id).Scan(&hasChildren); err != nil {
		return mapDBError(err)
	}
	if hasChildren {
		return fmt.Errorf("%w: menu has undeleted child nodes", biz.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE menus
		SET is_deleted = TRUE, enabled = FALSE,
			updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE role_menus
		SET is_deleted = TRUE,
			updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE menu_id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}
