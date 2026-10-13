package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

// APIPermissionGroupRepository persists the service-scoped API permission group
// catalog. Endpoint membership, enabled state and method/action agreement are
// validated against the local api_endpoints table inside the write transaction.
type APIPermissionGroupRepository struct {
	pool *pgxpool.Pool
}

var _ biz.APIPermissionGroupRepository = (*APIPermissionGroupRepository)(nil)

func NewAPIPermissionGroupRepository(pool *pgxpool.Pool) *APIPermissionGroupRepository {
	return &APIPermissionGroupRepository{pool: pool}
}

const apiPermissionGroupColumns = `id,service_resource_key,code,name,action,enabled,created_at,updated_at`

func scanAPIPermissionGroup(row rowScanner) (biz.APIPermissionGroup, error) {
	var group biz.APIPermissionGroup
	err := row.Scan(&group.ID, &group.ServiceResource, &group.Code, &group.Name, &group.Action,
		&group.Enabled, &group.CreatedAt, &group.UpdatedAt)
	return group, err
}

func loadAPIPermissionGroupEndpoints(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, groupID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT endpoint_id FROM api_permission_group_endpoints WHERE group_id=$1 ORDER BY endpoint_id`, groupID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapDBError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapDBError(rows.Err())
}

// validateEndpointGrantTx rechecks every requested endpoint against the local
// catalog: same service, enabled, supported method and read/write agreement.
// The group action is the caller's target action so an update that flips the
// action is validated before the membership rows are replaced.
func validateEndpointGrantTx(ctx context.Context, tx pgx.Tx, serviceResource, action string, endpointIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(endpointIDs) == 0 || len(endpointIDs) > biz.APIPermissionGroupMaxEndpoints {
		return nil, fmt.Errorf("%w: select between 1 and %d endpoints", biz.ErrInvalidArgument, biz.APIPermissionGroupMaxEndpoints)
	}
	validated := make([]uuid.UUID, 0, len(endpointIDs))
	seen := make(map[uuid.UUID]struct{}, len(endpointIDs))
	for _, id := range endpointIDs {
		if id == uuid.Nil {
			return nil, fmt.Errorf("%w: endpoint ids must be non-empty UUIDs", biz.ErrInvalidArgument)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: duplicate endpoint id", biz.ErrInvalidArgument)
		}
		seen[id] = struct{}{}
		var endpointService, method string
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT service_resource_key,method,enabled FROM api_endpoints WHERE id=$1 FOR SHARE`, id).
			Scan(&endpointService, &method, &enabled)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, fmt.Errorf("%w: API endpoint does not exist", biz.ErrConflict)
			}
			return nil, mapDBError(err)
		}
		if endpointService != serviceResource {
			return nil, fmt.Errorf("%w: API endpoint must belong to the group service", biz.ErrConflict)
		}
		if !enabled {
			return nil, fmt.Errorf("%w: API endpoint must be enabled", biz.ErrConflict)
		}
		if !biz.APIPermissionGroupMethodAllowed(action, method) {
			return nil, fmt.Errorf("%w: API method is not allowed for this group action", biz.ErrInvalidArgument)
		}
		validated = append(validated, id)
	}
	return validated, nil
}

func writeAPIPermissionGroupEndpoints(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, endpointIDs []uuid.UUID) error {
	for _, id := range endpointIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO api_permission_group_endpoints(group_id,endpoint_id) VALUES($1,$2)`, groupID, id); err != nil {
			return mapDBError(err)
		}
	}
	return nil
}

func (r *APIPermissionGroupRepository) List(ctx context.Context, serviceResource string) ([]biz.APIPermissionGroup, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+apiPermissionGroupColumns+` FROM api_permission_groups WHERE service_resource_key=$1 ORDER BY code,id`, strings.TrimSpace(serviceResource))
	if err != nil {
		return nil, mapDBError(err)
	}
	groups := make([]biz.APIPermissionGroup, 0)
	for rows.Next() {
		group, scanErr := scanAPIPermissionGroup(rows)
		if scanErr != nil {
			rows.Close()
			return nil, mapDBError(scanErr)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, mapDBError(err)
	}
	rows.Close()
	for i := range groups {
		groups[i].EndpointIDs, err = loadAPIPermissionGroupEndpoints(ctx, tx, groups[i].ID)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapDBError(err)
	}
	return groups, nil
}

// Save inserts a new group or updates an existing one. Updates lock the row,
// reject identity changes, replace endpoint membership and revalidate scope so
// a concurrent endpoint change fails closed.
func (r *APIPermissionGroupRepository) Save(ctx context.Context, group biz.APIPermissionGroup) (biz.APIPermissionGroup, error) {
	serviceResource := strings.TrimSpace(group.ServiceResource)
	action := biz.APIPermissionGroupAction(group.Action)
	if action == "" {
		return biz.APIPermissionGroup{}, fmt.Errorf("%w: action must be read or write", biz.ErrInvalidArgument)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.APIPermissionGroup{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)

	if group.ID == uuid.Nil {
		group.ID = uuid.New()
		err = tx.QueryRow(ctx, `INSERT INTO api_permission_groups(id,service_resource_key,code,name,action,enabled,created_at,updated_at)
            VALUES($1,$2,$3,$4,$5,$6,NOW(),NOW()) RETURNING created_at,updated_at`,
			group.ID, serviceResource, strings.TrimSpace(group.Code), group.Name, action, group.Enabled).
			Scan(&group.CreatedAt, &group.UpdatedAt)
		if err != nil {
			return biz.APIPermissionGroup{}, mapDBError(err)
		}
	} else {
		var current biz.APIPermissionGroup
		current, err = scanAPIPermissionGroup(tx.QueryRow(ctx, `SELECT `+apiPermissionGroupColumns+` FROM api_permission_groups WHERE id=$1 FOR UPDATE`, group.ID))
		if err != nil {
			return biz.APIPermissionGroup{}, mapDBError(err)
		}
		if current.ServiceResource != serviceResource || current.Code != strings.TrimSpace(group.Code) {
			return biz.APIPermissionGroup{}, fmt.Errorf("%w: permission group service_resource and code are immutable", biz.ErrInvalidArgument)
		}
		if err = tx.QueryRow(ctx, `UPDATE api_permission_groups SET name=$2,action=$3,enabled=$4,updated_at=NOW() WHERE id=$1 RETURNING created_at,updated_at`,
			group.ID, group.Name, action, group.Enabled).Scan(&group.CreatedAt, &group.UpdatedAt); err != nil {
			return biz.APIPermissionGroup{}, mapDBError(err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM api_permission_group_endpoints WHERE group_id=$1`, group.ID); err != nil {
			return biz.APIPermissionGroup{}, mapDBError(err)
		}
	}

	endpointIDs, err := validateEndpointGrantTx(ctx, tx, serviceResource, action, group.EndpointIDs)
	if err != nil {
		return biz.APIPermissionGroup{}, err
	}
	if err = writeAPIPermissionGroupEndpoints(ctx, tx, group.ID, endpointIDs); err != nil {
		return biz.APIPermissionGroup{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.APIPermissionGroup{}, mapDBError(err)
	}
	group.ServiceResource, group.Action = serviceResource, action
	group.Code = strings.TrimSpace(group.Code)
	group.EndpointIDs = endpointIDs
	return group, nil
}

// Delete removes a group scoped to its service. A group still referenced by a
// token policy is rejected so existing grants never silently lose their source.
func (r *APIPermissionGroupRepository) Delete(ctx context.Context, serviceResource string, id uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)
	var groupID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM api_permission_groups WHERE id=$1 AND service_resource_key=$2 FOR UPDATE`, id, strings.TrimSpace(serviceResource)).Scan(&groupID); err != nil {
		return mapDBError(err)
	}
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
            SELECT 1 FROM api_tokens t
            CROSS JOIN LATERAL jsonb_array_elements(
                CASE WHEN jsonb_typeof(t.policies) = 'array' THEN t.policies ELSE '[]'::jsonb END) AS policy
            WHERE jsonb_typeof(policy->'permission_group_ids') = 'array'
              AND jsonb_exists(policy->'permission_group_ids', $1::text))`, id.String()).Scan(&referenced); err != nil {
		return mapDBError(err)
	}
	if referenced {
		return fmt.Errorf("%w: permission group is referenced by token policies", biz.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM api_permission_groups WHERE id=$1`, id); err != nil {
		return mapDBError(err)
	}
	return mapDBError(tx.Commit(ctx))
}
