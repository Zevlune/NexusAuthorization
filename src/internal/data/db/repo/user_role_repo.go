package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/data/db/model"
)

// UserRoleRepository 管理用户与角色的关系；服务资源作用域由角色表统一提供。
type UserRoleRepository struct {
	pool *pgxpool.Pool
}

var _ biz.UserRoleRepository = (*UserRoleRepository)(nil)

func NewUserRoleRepository(pool *pgxpool.Pool) *UserRoleRepository {
	return &UserRoleRepository{pool: pool}
}

// ListRoleIDs 查询用户在指定服务资源下拥有的有效角色 ID。
func (r *UserRoleRepository) ListRoleIDs(ctx context.Context, subject, serviceResource string) ([]string, error) {
	subject, serviceResource, err := normalizeUserRoleScope(subject, serviceResource)
	if err != nil {
		return nil, err
	}
	const query = `
		SELECT ur.role_id
		FROM user_roles ur
		JOIN roles ro ON ro.id = ur.role_id
		WHERE ur.subject = $1
		  AND ro.service_resource = $2
		  AND ur.is_deleted = FALSE
		  AND ro.is_deleted = FALSE
		  AND ro.enabled = TRUE
		ORDER BY ro.name, ro.id`
	rows, err := r.pool.Query(ctx, query, subject, serviceResource)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	roleIDs := make([]string, 0)
	for rows.Next() {
		value := &model.UserRole{}
		if err := rows.Scan(&value.RoleID); err != nil {
			return nil, mapDBError(err)
		}
		roleIDs = append(roleIDs, value.RoleID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return roleIDs, nil
}

// RoleIDs 查询用户在指定服务资源下拥有的有效角色 ID。
func (r *UserRoleRepository) RoleIDs(ctx context.Context, subject, serviceResource string) ([]string, error) {
	return r.ListRoleIDs(ctx, subject, serviceResource)
}

// Replace 在事务中替换用户在指定服务资源下的全部角色，并保留其他服务资源的角色。
func (r *UserRoleRepository) Replace(ctx context.Context, subject, serviceResource string, roleIDs []string) error {
	subject, serviceResource, err := normalizeUserRoleScope(subject, serviceResource)
	if err != nil {
		return err
	}
	roleIDs, err = normalizeRoleIDs(roleIDs)
	if err != nil {
		return err
	}
	actor := biz.AuditActorFromContext(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)

	// FOR SHARE prevents a concurrent role update/delete from invalidating
	// validation between this check and insertion.
	for _, roleID := range roleIDs {
		var storedID string
		err := tx.QueryRow(ctx, `
			SELECT id
			FROM roles
				WHERE id = $1
				  AND service_resource = $2
			  AND is_deleted = FALSE
			  AND enabled = TRUE
			FOR SHARE`, roleID, serviceResource).Scan(&storedID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("%w: role %q does not exist, is disabled, deleted, or belongs to another service resource", biz.ErrConflict, roleID)
			}
			return mapDBError(err)
		}
	}

	// Do not delete by subject alone: a subject may have independent role
	// assignments in another service resource.
	if _, err := tx.Exec(ctx, `
		UPDATE user_roles ur
		SET is_deleted = TRUE,
			updated_by_id = $3,
			updated_by_name = $4,
			updated_at = NOW()
		FROM roles ro
		WHERE ur.role_id = ro.id
		  AND ur.subject = $1
		  AND ro.service_resource = $2
		  AND ur.is_deleted = FALSE`, subject, serviceResource, actor.ID, actor.Name); err != nil {
		return mapDBError(err)
	}
	for _, roleID := range roleIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (
				subject, role_id, created_by_id, created_by_name,
				updated_by_id, updated_by_name, is_deleted
			)
			VALUES ($1, $2, $3, $4, $3, $4, FALSE)
			ON CONFLICT (subject, role_id) DO UPDATE SET
				is_deleted = FALSE,
				updated_by_id = EXCLUDED.updated_by_id,
				updated_by_name = EXCLUDED.updated_by_name,
				updated_at = NOW()`, subject, roleID, actor.ID, actor.Name); err != nil {
			return mapDBError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}

// ReplaceRoles 提供给业务层使用的角色全量替换入口。
func (r *UserRoleRepository) ReplaceRoles(ctx context.Context, subject, serviceResource string, roleIDs []string) error {
	return r.Replace(ctx, subject, serviceResource, roleIDs)
}

// normalizeUserRoleScope 清理并校验用户及服务资源作用域。
func normalizeUserRoleScope(subject, serviceResource string) (string, string, error) {
	subject, serviceResource = strings.TrimSpace(subject), strings.TrimSpace(serviceResource)
	if subject == "" || len(subject) > 80 {
		return "", "", fmt.Errorf("%w: subject must be 1-80 characters", biz.ErrInvalidArgument)
	}
	if serviceResource == "" || len([]rune(serviceResource)) > 128 {
		return "", "", fmt.Errorf("%w: service_resource must be 1-128 characters", biz.ErrInvalidArgument)
	}
	return subject, serviceResource, nil
}

// normalizeRoleIDs 清理并校验角色 ID 列表，同时拒绝重复值。
func normalizeRoleIDs(roleIDs []string) ([]string, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(roleIDs))
	seen := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" || len(roleID) > 80 {
			return nil, fmt.Errorf("%w: role_id must be 1-80 characters", biz.ErrInvalidArgument)
		}
		if _, ok := seen[roleID]; ok {
			return nil, fmt.Errorf("%w: duplicate role_id %q", biz.ErrInvalidArgument, roleID)
		}
		seen[roleID] = struct{}{}
		normalized = append(normalized, roleID)
	}
	return normalized, nil
}
