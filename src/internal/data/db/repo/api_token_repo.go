package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
)

// APITokenRepository persists application-scoped token metadata and endpoint
// grant snapshots. All grant validation is local to the transaction.
type APITokenRepository struct {
	pool *pgxpool.Pool
}

var _ biz.APITokenRepository = (*APITokenRepository)(nil)

func NewAPITokenRepository(pool *pgxpool.Pool) *APITokenRepository {
	return &APITokenRepository{pool: pool}
}

const apiTokenColumns = `t.id,t.application_id,a.app_key,t.name,t.token_prefix,t.token_hash,
 t.owner_type,t.owner_id,t.status,t.expires_at,t.last_used_at,t.created_by,t.created_at,t.updated_at,t.revoked_at,t.policies`

func scanAPIToken(row rowScanner) (biz.APIToken, uuid.UUID, error) {
	var token biz.APIToken
	var applicationID uuid.UUID
	var policies []byte
	err := row.Scan(&token.ID, &applicationID, &token.AppKey, &token.Name, &token.TokenPrefix,
		&token.TokenHash, &token.OwnerType, &token.OwnerID, &token.Status, &token.ExpiresAt,
		&token.LastUsedAt, &token.CreatedBy, &token.CreatedAt, &token.UpdatedAt, &token.RevokedAt, &policies)
	if err == nil {
		err = json.Unmarshal(policies, &token.Policies)
	}
	return token, applicationID, err
}

func scanAPITokenPermission(row rowScanner, p *biz.APITokenPermission) error {
	return row.Scan(&p.EndpointID, &p.ServiceResource, &p.Action, &p.TenantID, &p.ProjectID,
		&p.Method, &p.PathTemplate, &p.Summary)
}

func loadTokenPermissions(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, tokenID uuid.UUID) ([]biz.APITokenPermission, error) {
	rows, err := q.Query(ctx, `SELECT endpoint_id,service_resource_key,action,tenant_id,project_id,method,path_template,summary
        FROM api_token_permissions WHERE token_id=$1 ORDER BY endpoint_id,tenant_id,project_id`, tokenID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	permissions := make([]biz.APITokenPermission, 0)
	for rows.Next() {
		var p biz.APITokenPermission
		if err := scanAPITokenPermission(rows, &p); err != nil {
			return nil, mapDBError(err)
		}
		permissions = append(permissions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return permissions, nil
}

func loadToken(ctx context.Context, q apiTokenQuerier, query string, args ...any) (biz.APIToken, error) {
	token, _, err := scanAPIToken(q.QueryRow(ctx, query, args...))
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	token.Permissions, err = loadEffectiveTokenPermissions(ctx, q, token)
	if err != nil {
		return biz.APIToken{}, err
	}
	return token, nil
}

func (r *APITokenRepository) tokenSnapshot(ctx context.Context, query string, args ...any) (biz.APIToken, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	token, err := loadToken(ctx, tx, query, args...)
	if err != nil {
		return biz.APIToken{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	return token, nil
}

func tokenApplicationID(ctx context.Context, tx pgx.Tx, appKey string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM applications WHERE app_key=$1 AND enabled FOR SHARE`, strings.TrimSpace(appKey)).Scan(&id)
	return id, mapDBError(err)
}

// validatePermissionsTx rechecks the live endpoint and writes current catalog
// metadata into each grant only after confirming the caller's snapshot agrees.
func validatePermissionsTx(ctx context.Context, tx pgx.Tx, applicationID uuid.UUID, permissions []biz.APITokenPermission) ([]biz.APITokenPermission, error) {
	validated := make([]biz.APITokenPermission, 0, len(permissions))
	seen := make(map[string]struct{}, len(permissions))
	for _, requested := range permissions {
		var p biz.APITokenPermission
		err := tx.QueryRow(ctx, `SELECT e.id,e.service_resource_key,e.method,e.path_template,e.summary
            FROM api_endpoints e
            JOIN application_service_resources l ON l.application_id=$1 AND l.service_resource_key=e.service_resource_key
			WHERE e.id=$2 AND e.service_resource_key=$3 AND e.enabled FOR SHARE`, applicationID, requested.EndpointID, requested.ServiceResource).
			Scan(&p.EndpointID, &p.ServiceResource, &p.Method, &p.PathTemplate, &p.Summary)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, fmt.Errorf("%w: API endpoint is not enabled or linked to the application", biz.ErrConflict)
			}
			return nil, mapDBError(err)
		}
		if requested.Method != p.Method || requested.PathTemplate != p.PathTemplate || requested.Summary != p.Summary || requested.Action != biz.APITokenAction(p.Method) {
			return nil, fmt.Errorf("%w: API endpoint snapshot no longer matches the live directory", biz.ErrConflict)
		}
		p.Action, p.TenantID, p.ProjectID = requested.Action, requested.TenantID, requested.ProjectID
		key := p.EndpointID.String() + "\x00" + p.TenantID + "\x00" + p.ProjectID
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate API token permission", biz.ErrInvalidArgument)
		}
		seen[key] = struct{}{}
		validated = append(validated, p)
	}
	return validated, nil
}

func insertToken(ctx context.Context, tx pgx.Tx, applicationID uuid.UUID, token biz.APIToken) error {
	policies := token.Policies
	if policies == nil {
		policies = []biz.APITokenPolicy{}
	}
	encoded, err := json.Marshal(policies)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO api_tokens(id,application_id,name,token_prefix,token_hash,owner_type,owner_id,status,expires_at,last_used_at,created_by,created_at,updated_at,revoked_at,policies)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, token.ID, applicationID, token.Name,
		token.TokenPrefix, token.TokenHash, token.OwnerType, token.OwnerID, token.Status, token.ExpiresAt,
		token.LastUsedAt, token.CreatedBy, token.CreatedAt, token.UpdatedAt, token.RevokedAt, encoded)
	return mapDBError(err)
}

func insertPermissions(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID, permissions []biz.APITokenPermission) error {
	for _, p := range permissions {
		if _, err := tx.Exec(ctx, `INSERT INTO api_token_permissions(token_id,endpoint_id,service_resource_key,action,tenant_id,project_id,method,path_template,summary)
            VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tokenID, p.EndpointID, p.ServiceResource, p.Action, p.TenantID, p.ProjectID, p.Method, p.PathTemplate, p.Summary); err != nil {
			return mapDBError(err)
		}
	}
	return nil
}

func auditToken(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID, action string) error {
	actor := biz.AuditActorFromContext(ctx)
	_, err := tx.Exec(ctx, `INSERT INTO api_token_audit_logs(token_id,actor_id,action) VALUES($1,$2,$3)`, tokenID, actor.ID, action)
	return mapDBError(err)
}

func (r *APITokenRepository) Create(ctx context.Context, token biz.APIToken) (biz.APIToken, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	applicationID, err := tokenApplicationID(ctx, tx, token.AppKey)
	if err != nil {
		return biz.APIToken{}, err
	}
	permissions, err := validateTokenGrantsTx(ctx, tx, applicationID, token)
	if err != nil {
		return biz.APIToken{}, err
	}
	if err = insertToken(ctx, tx, applicationID, token); err != nil {
		return biz.APIToken{}, err
	}
	if len(token.Policies) == 0 {
		if err = insertPermissions(ctx, tx, token.ID, permissions); err != nil {
			return biz.APIToken{}, err
		}
	}
	if err = auditToken(ctx, tx, token.ID, "created"); err != nil {
		return biz.APIToken{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	token.Permissions = permissions
	return token, nil
}

func (r *APITokenRepository) Get(ctx context.Context, appKey string, id uuid.UUID) (biz.APIToken, error) {
	return r.tokenSnapshot(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id
        WHERE a.app_key=$1 AND t.id=$2`, strings.TrimSpace(appKey), id)
}

func (r *APITokenRepository) Find(ctx context.Context, id uuid.UUID) (biz.APIToken, error) {
	return r.tokenSnapshot(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id
        WHERE a.enabled AND t.id=$1`, id)
}

func (r *APITokenRepository) List(ctx context.Context, filter biz.APITokenFilter) (biz.RBACPage[biz.APIToken], error) {
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return biz.RBACPage[biz.APIToken]{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	where := `a.app_key=$1`
	args := []any{strings.TrimSpace(filter.AppKey)}
	n := 2
	if filter.Search != "" {
		where += fmt.Sprintf(` AND (t.name ILIKE $%d OR t.token_prefix ILIKE $%d OR t.owner_id ILIKE $%d)`, n, n, n)
		args = append(args, "%"+filter.Search+"%")
		n++
	}
	if filter.Status != "" {
		if filter.Status == "expired" {
			where += ` AND t.status <> 'revoked' AND t.expires_at IS NOT NULL AND t.expires_at <= NOW()`
		} else {
			where += fmt.Sprintf(` AND t.status=$%d`, n)
			args = append(args, filter.Status)
			n++
			if filter.Status != "revoked" {
				where += ` AND (t.expires_at IS NULL OR t.expires_at > NOW())`
			}
		}
	}
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM api_tokens t JOIN applications a ON a.id=t.application_id WHERE `+where, args...).Scan(&total); err != nil {
		return biz.RBACPage[biz.APIToken]{}, mapDBError(err)
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := tx.Query(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id WHERE `+where+` ORDER BY t.created_at DESC,t.id LIMIT $`+fmt.Sprint(n)+` OFFSET $`+fmt.Sprint(n+1), args...)
	if err != nil {
		return biz.RBACPage[biz.APIToken]{}, mapDBError(err)
	}
	items := make([]biz.APIToken, 0)
	for rows.Next() {
		token, _, scanErr := scanAPIToken(rows)
		if scanErr != nil {
			rows.Close()
			return biz.RBACPage[biz.APIToken]{}, mapDBError(scanErr)
		}
		items = append(items, token)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return biz.RBACPage[biz.APIToken]{}, mapDBError(err)
	}
	rows.Close()
	for i := range items {
		items[i].Permissions, err = loadEffectiveTokenPermissions(ctx, tx, items[i])
		if err != nil {
			return biz.RBACPage[biz.APIToken]{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.RBACPage[biz.APIToken]{}, mapDBError(err)
	}
	return biz.RBACPage[biz.APIToken]{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (r *APITokenRepository) Update(ctx context.Context, token biz.APIToken) (biz.APIToken, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	var applicationID uuid.UUID
	var current biz.APIToken
	current, applicationID, err = scanAPIToken(tx.QueryRow(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id
        WHERE t.id=$1 AND a.app_key=$2 FOR UPDATE`, token.ID, strings.TrimSpace(token.AppKey)))
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	if current.Status == "revoked" {
		return biz.APIToken{}, fmt.Errorf("%w: revoked tokens cannot be edited", biz.ErrConflict)
	}
	permissions, err := validateTokenGrantsTx(ctx, tx, applicationID, token)
	if err != nil {
		return biz.APIToken{}, err
	}
	if current.OwnerType != token.OwnerType || current.OwnerID != token.OwnerID {
		return biz.APIToken{}, fmt.Errorf("%w: token ownership is immutable", biz.ErrConflict)
	}
	policies := token.Policies
	if policies == nil {
		policies = []biz.APITokenPolicy{}
	}
	encoded, err := json.Marshal(policies)
	if err != nil {
		return biz.APIToken{}, err
	}
	if err = tx.QueryRow(ctx, `UPDATE api_tokens SET name=$2,expires_at=$3,policies=$4,updated_at=NOW() WHERE id=$1 RETURNING updated_at`, token.ID, token.Name, token.ExpiresAt, encoded).Scan(&token.UpdatedAt); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM api_token_permissions WHERE token_id=$1`, token.ID); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	if len(token.Policies) == 0 {
		if err = insertPermissions(ctx, tx, token.ID, permissions); err != nil {
			return biz.APIToken{}, err
		}
	}
	if err = auditToken(ctx, tx, token.ID, "updated"); err != nil {
		return biz.APIToken{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	token.ID, token.AppKey, token.TokenPrefix, token.TokenHash = current.ID, current.AppKey, current.TokenPrefix, current.TokenHash
	token.OwnerType, token.OwnerID, token.Status, token.CreatedBy = current.OwnerType, current.OwnerID, current.Status, current.CreatedBy
	token.CreatedAt, token.LastUsedAt, token.RevokedAt, token.Permissions = current.CreatedAt, current.LastUsedAt, current.RevokedAt, permissions
	return token, nil
}

func (r *APITokenRepository) SetStatus(ctx context.Context, appKey string, id uuid.UUID, status string) (biz.APIToken, error) {
	if status != "active" && status != "disabled" && status != "revoked" {
		return biz.APIToken{}, fmt.Errorf("%w: invalid token status", biz.ErrInvalidArgument)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	var current biz.APIToken
	if current, _, err = scanAPIToken(tx.QueryRow(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id
        WHERE t.id=$1 AND a.app_key=$2 FOR UPDATE`, id, strings.TrimSpace(appKey))); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	if current.Status == "revoked" && status != "revoked" {
		return biz.APIToken{}, fmt.Errorf("%w: revoked tokens cannot be enabled or edited", biz.ErrConflict)
	}
	if current.Status != status {
		if _, err = tx.Exec(ctx, `UPDATE api_tokens SET status=$2::varchar,revoked_at=CASE WHEN $2::varchar='revoked' THEN COALESCE(revoked_at,NOW()) ELSE NULL END,updated_at=NOW() WHERE id=$1`, id, status); err != nil {
			return biz.APIToken{}, mapDBError(err)
		}
	}
	if err = auditToken(ctx, tx, id, map[string]string{"revoked": "revoked", "disabled": "disabled", "active": "enabled"}[status]); err != nil {
		return biz.APIToken{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	return r.Get(ctx, appKey, id)
}

func (r *APITokenRepository) Rotate(ctx context.Context, appKey string, id uuid.UUID, token biz.APIToken, revokeOld bool) (biz.APIToken, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	var old biz.APIToken
	var applicationID uuid.UUID
	old, applicationID, err = scanAPIToken(tx.QueryRow(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens t JOIN applications a ON a.id=t.application_id
        WHERE t.id=$1 AND a.app_key=$2 FOR UPDATE`, id, strings.TrimSpace(appKey)))
	if err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	if old.Status != "active" || (old.ExpiresAt != nil && !old.ExpiresAt.After(time.Now())) {
		return biz.APIToken{}, fmt.Errorf("%w: only an active, unexpired token can rotate", biz.ErrConflict)
	}
	old.Permissions, err = loadEffectiveTokenPermissions(ctx, tx, old)
	if err != nil {
		return biz.APIToken{}, err
	}
	permissions, err := validateTokenGrantsTx(ctx, tx, applicationID, old)
	if err != nil {
		return biz.APIToken{}, err
	}
	token.AppKey, token.OwnerType, token.OwnerID, token.Name, token.ExpiresAt = old.AppKey, old.OwnerType, old.OwnerID, old.Name, old.ExpiresAt
	token.Permissions, token.Status = permissions, "active"
	token.Policies = old.Policies
	if err = insertToken(ctx, tx, applicationID, token); err != nil {
		return biz.APIToken{}, err
	}
	if len(token.Policies) == 0 {
		if err = insertPermissions(ctx, tx, token.ID, permissions); err != nil {
			return biz.APIToken{}, err
		}
	}
	if err = auditToken(ctx, tx, token.ID, "rotated"); err != nil {
		return biz.APIToken{}, err
	}
	if revokeOld {
		if _, err = tx.Exec(ctx, `UPDATE api_tokens SET status='revoked',revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW() WHERE id=$1`, old.ID); err != nil {
			return biz.APIToken{}, mapDBError(err)
		}
		if err = auditToken(ctx, tx, old.ID, "revoked"); err != nil {
			return biz.APIToken{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.APIToken{}, mapDBError(err)
	}
	token.Permissions = permissions
	return token, nil
}

func (r *APITokenRepository) Endpoints(ctx context.Context, appKey, serviceKey string) ([]biz.RBACEndpoint, error) {
	var linked bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM applications a JOIN application_service_resources l ON l.application_id=a.id WHERE a.enabled AND a.app_key=$1 AND l.service_resource_key=$2
        AND NOT EXISTS(SELECT 1 FROM service_resources s WHERE s.resource_key=l.service_resource_key AND NOT s.enabled))`, strings.TrimSpace(appKey), strings.TrimSpace(serviceKey)).Scan(&linked); err != nil {
		return nil, mapDBError(err)
	}
	if !linked {
		return nil, biz.ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT e.id,e.service_resource_key,e.endpoint_key,e.method,e.path_template,e.module,e.summary,e.source,e.enabled,e.last_discovered_at,e.created_at,e.updated_at
        FROM api_endpoints e JOIN application_service_resources l ON l.service_resource_key=e.service_resource_key
        JOIN applications a ON a.id=l.application_id WHERE a.enabled AND a.app_key=$1 AND e.service_resource_key=$2
        AND NOT EXISTS(SELECT 1 FROM service_resources s WHERE s.resource_key=e.service_resource_key AND NOT s.enabled)
        ORDER BY e.module,e.method,e.path_template,e.id`, strings.TrimSpace(appKey), strings.TrimSpace(serviceKey))
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := make([]biz.RBACEndpoint, 0)
	for rows.Next() {
		var e biz.RBACEndpoint
		if err := rows.Scan(&e.ID, &e.ServiceResource, &e.EndpointKey, &e.Method, &e.PathTemplate, &e.Module, &e.Summary, &e.Source, &e.Enabled, &e.LastDiscoveredAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, mapDBError(err)
		}
		items = append(items, e)
	}
	return items, mapDBError(rows.Err())
}

func (r *APITokenRepository) RecordUse(ctx context.Context, id uuid.UUID) error {
	useCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err := r.pool.Exec(useCtx, `UPDATE api_tokens SET last_used_at=NOW(),updated_at=updated_at WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < NOW()-INTERVAL '1 minute')`, id)
	return mapDBError(err)
}
