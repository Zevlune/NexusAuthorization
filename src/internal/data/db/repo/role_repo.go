package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

const roleColumns = `id, service_resource, code, name, description, enabled,
	created_by_id, created_by_name, created_at, updated_by_id, updated_by_name,
	updated_at, is_deleted`

// RoleRepository 保存服务资源作用域内的角色及菜单授权。
type RoleRepository struct {
	pool *pgxpool.Pool
}

var _ biz.RoleRepository = (*RoleRepository)(nil)

func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{pool: pool}
}

func (r *RoleRepository) Create(ctx context.Context, role *biz.Role) (*biz.Role, error) {
	if role == nil || strings.TrimSpace(role.ServiceResource) == "" {
		return nil, fmt.Errorf("%w: role and service_resource are required", biz.ErrInvalidArgument)
	}
	scope := strings.TrimSpace(role.ServiceResource)
	storedID := strings.TrimSpace(role.ID)
	if storedID == "" {
		generatedID, err := newRoleID()
		if err != nil {
			return nil, err
		}
		storedID = generatedID
	}
	const query = `
		INSERT INTO roles (
			id, service_resource, code, name, description, enabled,
			created_by_id, created_by_name, updated_by_id, updated_by_name, is_deleted
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $7, $8, FALSE)
		RETURNING ` + roleColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanRole(r.pool.QueryRow(ctx, query,
		storedID,
		scope,
		role.Code,
		role.Name,
		role.Description,
		role.Enabled,
		actor.ID,
		actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizRole(value), nil
}

func newRoleID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate role id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func (r *RoleRepository) Get(ctx context.Context, id string) (*biz.Role, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	const query = `SELECT ` + roleColumns + `
		FROM roles
		WHERE id = $1 AND is_deleted = FALSE`
	value, err := scanRole(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizRole(value), nil
}

func (r *RoleRepository) ListByServiceResource(ctx context.Context, serviceResource string) ([]*biz.Role, error) {
	const query = `SELECT ` + roleColumns + `
		FROM roles
		WHERE service_resource = $1 AND is_deleted = FALSE
		ORDER BY name, id`
	rows, err := r.pool.Query(ctx, query, serviceResource)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	roles := make([]*biz.Role, 0)
	for rows.Next() {
		value, err := scanRole(rows)
		if err != nil {
			return nil, mapDBError(err)
		}
		roles = append(roles, toBizRole(value))
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return roles, nil
}

// Update 更新角色的可变字段并写入更新审计信息，保留服务资源和创建审计字段。
func (r *RoleRepository) Update(ctx context.Context, role *biz.Role) (*biz.Role, error) {
	if role == nil || strings.TrimSpace(role.ID) == "" {
		return nil, fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	roleID := strings.TrimSpace(role.ID)
	const query = `
		UPDATE roles
		SET code = $2, name = $3, description = $4, enabled = $5,
			updated_by_id = $6, updated_by_name = $7, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING ` + roleColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanRole(r.pool.QueryRow(ctx, query,
		roleID,
		role.Code,
		role.Name,
		role.Description,
		role.Enabled,
		actor.ID,
		actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizRole(value), nil
}

func (r *RoleRepository) SetEnabled(ctx context.Context, id string, enabled bool) (*biz.Role, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	const query = `
		UPDATE roles
		SET enabled = $2, updated_by_id = $3, updated_by_name = $4, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING ` + roleColumns
	actor := biz.AuditActorFromContext(ctx)
	value, err := scanRole(r.pool.QueryRow(ctx, query, id, enabled, actor.ID, actor.Name))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizRole(value), nil
}

// SoftDelete 在一个事务中软删除角色及其菜单、用户角色关联。
func (r *RoleRepository) SoftDelete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	id = strings.TrimSpace(id)
	actor := biz.AuditActorFromContext(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
		UPDATE roles
		SET is_deleted = TRUE, enabled = FALSE,
			updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name)
	if err != nil {
		return mapDBError(err)
	}
	if result.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE role_menus
		SET is_deleted = TRUE,
			updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE role_id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_roles
		SET is_deleted = TRUE,
			updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE role_id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}

// ReplaceMenus 原子替换角色的菜单和按钮授权，数据库同时校验服务资源作用域。
func (r *RoleRepository) ReplaceMenus(ctx context.Context, roleID string, menuIDs []uuid.UUID) error {
	if strings.TrimSpace(roleID) == "" {
		return fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	roleID = strings.TrimSpace(roleID)
	for _, menuID := range menuIDs {
		if menuID == uuid.Nil {
			return fmt.Errorf("%w: menu id is required", biz.ErrInvalidArgument)
		}
	}
	actor := biz.AuditActorFromContext(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)

	var roleExists bool
	if err := tx.QueryRow(ctx, `
		SELECT TRUE FROM roles
		WHERE id = $1 AND is_deleted = FALSE AND enabled = TRUE
		FOR UPDATE`, roleID).Scan(&roleExists); err != nil {
		return mapDBError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE role_menus
		SET is_deleted = TRUE, updated_by_id = $2, updated_by_name = $3, updated_at = NOW()
		WHERE role_id = $1 AND is_deleted = FALSE`, roleID, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	for _, menuID := range menuIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_menus (
				role_id, menu_id, created_by_id, created_by_name,
				updated_by_id, updated_by_name, is_deleted
			)
			VALUES ($1, $2, $3, $4, $3, $4, FALSE)
			ON CONFLICT (role_id, menu_id) DO UPDATE SET
				is_deleted = FALSE,
				updated_by_id = EXCLUDED.updated_by_id,
				updated_by_name = EXCLUDED.updated_by_name,
				updated_at = NOW()`, roleID, menuID, actor.ID, actor.Name); err != nil {
			return mapDBError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}

func (r *RoleRepository) MenuIDs(ctx context.Context, roleID string) ([]uuid.UUID, error) {
	if strings.TrimSpace(roleID) == "" {
		return nil, fmt.Errorf("%w: role id is required", biz.ErrInvalidArgument)
	}
	const query = `
		SELECT rm.menu_id
		FROM role_menus rm
		JOIN roles ro ON ro.id = rm.role_id
		JOIN menus me ON me.id = rm.menu_id
		WHERE rm.role_id = $1
		  AND rm.is_deleted = FALSE
		  AND ro.is_deleted = FALSE AND ro.enabled = TRUE
		  AND me.is_deleted = FALSE AND me.enabled = TRUE
		ORDER BY me.sort_order, me.name, me.id`
	rows, err := r.pool.Query(ctx, query, roleID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	menuIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var menuID uuid.UUID
		if err := rows.Scan(&menuID); err != nil {
			return nil, mapDBError(err)
		}
		menuIDs = append(menuIDs, menuID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return menuIDs, nil
}
