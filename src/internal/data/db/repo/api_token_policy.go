package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luck/permission-center-go/internal/biz"
)

type apiTokenQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadEffectiveTokenPermissions(ctx context.Context, q apiTokenQuerier, token biz.APIToken) ([]biz.APITokenPermission, error) {
	if len(token.Policies) == 0 {
		return loadTokenPermissions(ctx, q, token.ID)
	}
	return resolveTokenPolicies(ctx, q, token.AppKey, token.Policies, false)
}

func validateTokenGrantsTx(ctx context.Context, tx pgx.Tx, applicationID uuid.UUID, token biz.APIToken) ([]biz.APITokenPermission, error) {
	if len(token.Policies) > 0 {
		return resolveTokenPolicies(ctx, tx, token.AppKey, token.Policies, true)
	}
	return validatePermissionsTx(ctx, tx, applicationID, token.Permissions)
}

func (r *APITokenRepository) ResolvePolicies(ctx context.Context, appKey string, policies []biz.APITokenPolicy) ([]biz.APITokenPermission, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	permissions, err := resolveTokenPolicies(ctx, tx, appKey, policies, true)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, mapDBError(err)
	}
	return permissions, nil
}

// Resolve maintained group mappings on every local token read. Disabled groups,
// endpoints and services grant nothing. There is no fallback to an old snapshot.
func resolveTokenPolicies(ctx context.Context, q apiTokenQuerier, appKey string, policies []biz.APITokenPolicy, strict bool) ([]biz.APITokenPermission, error) {
	permissions := make([]biz.APITokenPermission, 0)
	seen := make(map[string]bool)
	for _, policy := range policies {
		if policy.Effect != "allow" {
			return nil, fmt.Errorf("%w: only allow policies are supported", biz.ErrInvalidArgument)
		}
		for _, groupID := range policy.PermissionGroupIDs {
			if strict {
				var service string
				var enabled bool
				err := q.QueryRow(ctx, `SELECT service_resource_key,enabled FROM api_permission_groups WHERE id=$1 FOR SHARE`, groupID).Scan(&service, &enabled)
				if err == pgx.ErrNoRows || (err == nil && (!enabled || service != policy.ServiceResource)) {
					return nil, fmt.Errorf("%w: permission group must be enabled and belong to the policy service", biz.ErrConflict)
				}
				if err != nil {
					return nil, mapDBError(err)
				}
			}
			rows, err := q.Query(ctx, `SELECT e.id,e.service_resource_key,e.method,e.path_template,e.summary
    FROM api_permission_groups g
    JOIN api_permission_group_endpoints ge ON ge.group_id=g.id
    JOIN api_endpoints e ON e.id=ge.endpoint_id AND e.service_resource_key=g.service_resource_key
    JOIN application_service_resources l ON l.service_resource_key=e.service_resource_key
    JOIN applications a ON a.id=l.application_id
    WHERE g.id=$1 AND g.service_resource_key=$2 AND g.enabled AND e.enabled
      AND a.app_key=$3 AND a.enabled
      AND e.method IN ('GET','HEAD','POST','PUT','PATCH','DELETE')
      AND (g.action='write' OR e.method IN ('GET','HEAD'))
      AND NOT EXISTS(SELECT 1 FROM service_resources s WHERE s.resource_key=e.service_resource_key AND NOT s.enabled)
    ORDER BY e.id`, groupID, policy.ServiceResource, appKey)
			if err != nil {
				return nil, mapDBError(err)
			}
			count := 0
			for rows.Next() {
				var p biz.APITokenPermission
				if err = rows.Scan(&p.EndpointID, &p.ServiceResource, &p.Method, &p.PathTemplate, &p.Summary); err != nil {
					rows.Close()
					return nil, mapDBError(err)
				}
				count++
				p.Action, p.TenantID, p.ProjectID = biz.APITokenAction(p.Method), policy.TenantID, policy.ProjectID
				key := p.EndpointID.String() + "\x00" + p.TenantID + "\x00" + p.ProjectID
				if !seen[key] {
					seen[key] = true
					permissions = append(permissions, p)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, mapDBError(err)
			}
			if strict && count == 0 {
				return nil, fmt.Errorf("%w: permission group has no enabled APIs in an application-linked service", biz.ErrConflict)
			}
		}
	}
	if strict && (len(permissions) == 0 || len(permissions) > 1000) {
		return nil, fmt.Errorf("%w: select between 1 and 1000 API permissions", biz.ErrInvalidArgument)
	}
	return permissions, nil
}
