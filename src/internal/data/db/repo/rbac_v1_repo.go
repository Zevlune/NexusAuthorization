package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

// RBACV1Repository persists the normalized RBAC v1 control plane. It does
// not read or write the retired JSON policy document.
type RBACV1Repository struct {
	pool    *pgxpool.Pool
	catalog biz.ServiceResourceCatalog
	cache   RBACPolicyCache
}

// RBACPolicyCache stores only current permission records, validated against database tokens.
type RBACPolicyCache interface {
	Load(context.Context, biz.CurrentPolicy) (map[string]any, error)
	Store(context.Context, biz.CurrentPolicy) error
}

func (r *RBACV1Repository) WithPolicyCache(cache RBACPolicyCache) *RBACV1Repository {
	r.cache = cache
	return r
}

var _ biz.RBACV1Repository = (*RBACV1Repository)(nil)

func NewRBACV1Repository(pool *pgxpool.Pool, catalogs ...biz.ServiceResourceCatalog) *RBACV1Repository {
	localCatalog, _ := biz.NewServiceResourceCatalog(NewServiceResourceRepository(pool), nil, biz.ServiceResourceSourceLocal)
	var catalog biz.ServiceResourceCatalog = localCatalog
	if len(catalogs) > 0 && catalogs[0] != nil {
		catalog = catalogs[0]
	}
	return &RBACV1Repository{pool: pool, catalog: catalog}
}

func (r *RBACV1Repository) resourceKey(ctx context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("%w: service resource key is required", biz.ErrInvalidArgument)
	}
	resource, err := r.catalog.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if resource == nil || !resource.IsActive {
		return "", fmt.Errorf("%w: service resource must exist and be enabled", biz.ErrConflict)
	}
	return key, nil
}

func (r *RBACV1Repository) activeServiceKeys(ctx context.Context) ([]string, error) {
	resources, err := r.catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource != nil && resource.IsActive {
			keys = append(keys, resource.Key)
		}
	}
	return keys, nil
}

func (r *RBACV1Repository) ListEndpoints(ctx context.Context, key string) ([]biz.RBACEndpoint, error) {
	scope, err := r.resourceKey(ctx, key)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled,last_discovered_at,created_at,updated_at FROM api_endpoints WHERE service_resource_key=$1 ORDER BY module,method,path_template`, scope)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := make([]biz.RBACEndpoint, 0)
	for rows.Next() {
		var v biz.RBACEndpoint
		if err := rows.Scan(&v.ID, &v.ServiceResource, &v.EndpointKey, &v.Method, &v.PathTemplate, &v.Module, &v.Summary, &v.Source, &v.Enabled, &v.LastDiscoveredAt, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, mapDBError(err)
		}
		items = append(items, v)
	}
	return items, mapDBError(rows.Err())
}
func (r *RBACV1Repository) SaveEndpoint(ctx context.Context, key string, value biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	scope, err := r.resourceKey(ctx, key)
	if err != nil {
		return value, err
	}
	value.Method = strings.ToUpper(strings.TrimSpace(value.Method))
	value.PathTemplate = strings.TrimSpace(value.PathTemplate)
	if value.EndpointKey == "" {
		value.EndpointKey = "endpoint:" + strings.ToLower(value.Method) + ":" + strings.ReplaceAll(strings.Trim(strings.ReplaceAll(value.PathTemplate, "/", "-"), "-"), "{", "")
	}
	if value.Source == "" {
		value.Source = "manual"
	}
	if value.ID == uuid.Nil {
		err = r.pool.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled,last_discovered_at,created_at,updated_at`, scope, value.EndpointKey, value.Method, value.PathTemplate, value.Module, value.Summary, value.Source, value.Enabled).Scan(&value.ID, &value.ServiceResource, &value.EndpointKey, &value.Method, &value.PathTemplate, &value.Module, &value.Summary, &value.Source, &value.Enabled, &value.LastDiscoveredAt, &value.CreatedAt, &value.UpdatedAt)
	} else {
		err = r.pool.QueryRow(ctx, `UPDATE api_endpoints SET method=$3,path_template=$4,module=$5,summary=$6,enabled=$7,updated_at=NOW() WHERE id=$1 AND service_resource_key=$2 RETURNING id,service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled,last_discovered_at,created_at,updated_at`, value.ID, scope, value.Method, value.PathTemplate, value.Module, value.Summary, value.Enabled).Scan(&value.ID, &value.ServiceResource, &value.EndpointKey, &value.Method, &value.PathTemplate, &value.Module, &value.Summary, &value.Source, &value.Enabled, &value.LastDiscoveredAt, &value.CreatedAt, &value.UpdatedAt)
	}
	return value, mapDBError(err)
}
func (r *RBACV1Repository) DeleteEndpoint(ctx context.Context, key string, id uuid.UUID) error {
	scope, err := r.resourceKey(ctx, key)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM api_endpoints WHERE id=$1 AND service_resource_key=$2`, id, scope)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	return nil
}

func (r *RBACV1Repository) ListRoles(ctx context.Context, key string) ([]biz.RBACRole, error) {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT r.id,r.application_id,r.code,r.name,r.description,r.enabled,COALESCE(array_agg(b.endpoint_id) FILTER (WHERE b.endpoint_id IS NOT NULL),'{}'),COALESCE((SELECT array_agg(menu_id) FROM role_menu_bindings WHERE role_id=r.id),'{}') FROM roles r LEFT JOIN role_api_endpoint_bindings b ON b.role_id=r.id WHERE r.application_id=$1 GROUP BY r.id ORDER BY r.code`, scope)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := []biz.RBACRole{}
	for rows.Next() {
		var v biz.RBACRole
		if err := rows.Scan(&v.ID, &v.ApplicationID, &v.Code, &v.Name, &v.Description, &v.Enabled, &v.EndpointIDs, &v.MenuIDs); err != nil {
			return nil, mapDBError(err)
		}
		items = append(items, v)
	}
	return items, mapDBError(rows.Err())
}
func (r *RBACV1Repository) SaveRole(ctx context.Context, key string, value biz.RBACRole) (biz.RBACRole, error) {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return value, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return value, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, scope); err != nil {
		return value, err
	}
	if value.ID == uuid.Nil {
		err = tx.QueryRow(ctx, `INSERT INTO roles(application_id,code,name,description,enabled)VALUES($1,$2,$3,$4,$5)RETURNING id,application_id,code,name,description,enabled`, scope, strings.TrimSpace(value.Code), strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), value.Enabled).Scan(&value.ID, &value.ApplicationID, &value.Code, &value.Name, &value.Description, &value.Enabled)
	} else {
		err = tx.QueryRow(ctx, `UPDATE roles SET name=$3,description=$4,enabled=$5,updated_at=NOW() WHERE id=$1 AND application_id=$2 RETURNING id,application_id,code,name,description,enabled`, value.ID, scope, strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), value.Enabled).Scan(&value.ID, &value.ApplicationID, &value.Code, &value.Name, &value.Description, &value.Enabled)
	}
	if err != nil {
		return value, mapDBError(err)
	}
	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return value, err
	}
	var endpointCount int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM api_endpoints e JOIN application_service_resources a ON a.service_resource_key=e.service_resource_key WHERE a.application_id=$1 AND e.id=ANY($2::uuid[]) AND e.service_resource_key=ANY($3::text[])`, scope, value.EndpointIDs, activeKeys).Scan(&endpointCount); err != nil {
		return value, mapDBError(err)
	}
	if endpointCount != len(value.EndpointIDs) {
		return value, fmt.Errorf("%w: an endpoint belongs to an unlinked or disabled service", biz.ErrConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM role_api_endpoint_bindings WHERE role_id=$1`, value.ID); err != nil {
		return value, mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_api_endpoint_bindings(role_id,endpoint_id) SELECT $1,unnest($2::uuid[])`, value.ID, value.EndpointIDs); err != nil {
		return value, mapDBError(err)
	}
	var menuCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM menus WHERE application_id=$1 AND id=ANY($2::uuid[])`, scope, value.MenuIDs).Scan(&menuCount); err != nil {
		return value, err
	}
	if menuCount != len(value.MenuIDs) {
		return value, fmt.Errorf("%w: menu outside application", biz.ErrInvalidArgument)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM role_menu_bindings WHERE role_id=$1`, value.ID); err != nil {
		return value, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_menu_bindings(role_id,menu_id) SELECT $1,unnest($2::uuid[])`, value.ID, value.MenuIDs); err != nil {
		return value, mapDBError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return value, mapDBError(err)
	}
	return value, nil
}
func (r *RBACV1Repository) DeleteRole(ctx context.Context, key string, id uuid.UUID) error {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM roles WHERE id=$1 AND application_id=$2`, id, scope)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	return nil
}
func (r *RBACV1Repository) ListSubjectRoleBindings(ctx context.Context, key string) ([]biz.SubjectRoleBinding, error) {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,application_id,subject,domain,role_id FROM subject_role_bindings WHERE application_id=$1 ORDER BY subject,domain`, scope)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := []biz.SubjectRoleBinding{}
	// After the legacy alias folds onto the empty default domain, an old
	// "global" row and its empty-string twin describe the same grant. Collapse
	// them on read so callers never see duplicate bindings before 003 runs.
	seen := map[subjectRoleKey]struct{}{}
	for rows.Next() {
		var v biz.SubjectRoleBinding
		if err := rows.Scan(&v.ID, &v.ApplicationID, &v.Subject, &v.Domain, &v.RoleID); err != nil {
			return nil, mapDBError(err)
		}
		// Legacy "global"/blank rows fold onto the empty default domain so
		// callers never observe the retired literal.
		v.Domain = biz.NormalizeRBACDomain(v.Domain)
		key := subjectRoleKey{subject: v.Subject, domain: v.Domain, roleID: v.RoleID}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, v)
	}
	return items, mapDBError(rows.Err())
}

type subjectRoleKey struct {
	subject string
	domain  string
	roleID  uuid.UUID
}

func (r *RBACV1Repository) ReplaceSubjectRoles(ctx context.Context, key, subject, domain string, roleIDs []uuid.UUID) error {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return err
	}
	// Defensive normalization: persisted default-domain bindings use the empty
	// string, never the retired "global" alias.
	domain = biz.NormalizeRBACDomain(domain)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, scope); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM roles WHERE application_id=$1 AND id=ANY($2)`, scope, roleIDs).Scan(&count); err != nil {
		return mapDBError(err)
	}
	if count != len(roleIDs) {
		return fmt.Errorf("%w: a role belongs to another application", biz.ErrConflict)
	}
	// Replacing the default domain must also clear the retired legacy aliases
	// ("global", " global ", whitespace-only). Reads and the compiled policy
	// already fold those onto the empty domain, so leaving them behind would
	// keep authorizing a grant the caller just removed. Named domains stay
	// scoped to exactly that name.
	if domain == "" {
		_, err = tx.Exec(ctx, `DELETE FROM subject_role_bindings WHERE application_id=$1 AND subject=$2 AND btrim(domain) IN ('global', '')`, scope, subject)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM subject_role_bindings WHERE application_id=$1 AND subject=$2 AND domain=$3`, scope, subject, domain)
	}
	if err != nil {
		return mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) SELECT $1,$2,$3,unnest($4::uuid[])`, scope, subject, domain, roleIDs); err != nil {
		return mapDBError(err)
	}
	return mapDBError(tx.Commit(ctx))
}

func (r *RBACV1Repository) applicationID(ctx context.Context, key string) (uuid.UUID, error) {
	var id uuid.UUID
	if strings.TrimSpace(key) == "" {
		return id, fmt.Errorf("%w: app_key is required", biz.ErrInvalidArgument)
	}
	err := r.pool.QueryRow(ctx, `SELECT id FROM applications WHERE app_key=$1 AND enabled`, key).Scan(&id)
	return id, mapDBError(err)
}

func (r *RBACV1Repository) ListApplicationEndpoints(ctx context.Context, app, service string, page, size int) ([]biz.RBACEndpoint, int, error) {
	aid, err := r.applicationID(ctx, app)
	if err != nil {
		return nil, 0, err
	}
	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	where := ` FROM api_endpoints e JOIN application_service_resources a ON a.service_resource_key=e.service_resource_key WHERE a.application_id=$1 AND e.service_resource_key=ANY($3::text[]) AND ($2='' OR e.service_resource_key=$2)`
	if err = r.pool.QueryRow(ctx, `SELECT count(*)`+where, aid, service, activeKeys).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT e.id,e.service_resource_key,e.endpoint_key,e.method,e.path_template,e.module,e.summary,e.source,e.enabled,e.last_discovered_at,e.created_at,e.updated_at`+where+` ORDER BY e.service_resource_key,e.module,e.path_template,e.method LIMIT $4 OFFSET $5`, aid, service, activeKeys, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []biz.RBACEndpoint{}
	for rows.Next() {
		var v biz.RBACEndpoint
		if err = rows.Scan(&v.ID, &v.ServiceResource, &v.EndpointKey, &v.Method, &v.PathTemplate, &v.Module, &v.Summary, &v.Source, &v.Enabled, &v.LastDiscoveredAt, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// Read current normalized tables in one MVCC statement; nothing is persisted as JSON.
const applicationCurrentPolicySQL = `SELECT a.policy_token::text,jsonb_build_object(
 'app_key',a.app_key,'application_id',a.id,
 'service_resources',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',x.service_resource_key) ORDER BY x.service_resource_key) FROM application_service_resources x WHERE x.application_id=a.id AND x.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'endpoints',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'service_resource',e.service_resource_key,'method',e.method,'path_template',e.path_template,'enabled',e.enabled) ORDER BY e.id) FROM api_endpoints e JOIN application_service_resources x ON x.service_resource_key=e.service_resource_key WHERE x.application_id=a.id AND e.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'code',r.code,'enabled',r.enabled,'endpoint_ids',COALESCE((SELECT jsonb_agg(b.endpoint_id ORDER BY b.endpoint_id) FROM role_api_endpoint_bindings b WHERE b.role_id=r.id),'[]'::jsonb),'menu_ids',COALESCE((SELECT jsonb_agg(b.menu_id ORDER BY b.menu_id) FROM role_menu_bindings b WHERE b.role_id=r.id),'[]'::jsonb)) ORDER BY r.id) FROM roles r WHERE r.application_id=a.id),'[]'::jsonb),
 'subject_roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',b.subject,'domain',b.domain,'role_id',b.role_id) ORDER BY b.subject,b.domain,b.role_id) FROM subject_role_bindings b WHERE b.application_id=a.id),'[]'::jsonb),
 'menus',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'parent_id',m.parent_id,'code',m.code,'name',m.name,'menu_type',m.menu_type,'route',m.route,'component',m.component,'icon',m.icon,'sort_order',m.sort_order,'enabled',m.enabled) ORDER BY m.sort_order,m.id) FROM menus m WHERE m.application_id=a.id),'[]'::jsonb)
 ) FROM applications a WHERE a.id=$1 AND a.enabled AND a.policy_token::text=split_part($3,':',1)`

func (r *RBACV1Repository) GetPolicyMetadata(ctx context.Context, scope string) (biz.CurrentPolicy, error) {
	var p biz.CurrentPolicy
	if strings.TrimSpace(scope) == "" {
		return p, biz.ErrInvalidArgument
	}
	if err := r.pool.QueryRow(ctx, `SELECT id,policy_token::text FROM applications WHERE app_key=$1 AND enabled`, scope).Scan(&p.ApplicationID, &p.Token); err != nil {
		return p, mapDBError(err)
	}
	keys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return p, err
	}
	sort.Strings(keys)
	raw, _ := json.Marshal(keys)
	digest := sha256.Sum256(raw)
	p.Token += ":" + hex.EncodeToString(digest[:])
	p.ServiceKeys = keys
	return p, nil
}

func (r *RBACV1Repository) GetCurrentPolicy(ctx context.Context, p biz.CurrentPolicy) (biz.CurrentPolicy, error) {
	if r.cache != nil {
		data, err := r.cache.Load(ctx, p)
		if err == nil {
			p.Data = data
			return p, nil
		}
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
	}
	var raw []byte
	var token string
	err := r.pool.QueryRow(ctx, applicationCurrentPolicySQL, p.ApplicationID, p.ServiceKeys, p.Token).Scan(&token, &raw)
	// A save won the race after metadata was read. Retry with fresh metadata.
	if err == pgx.ErrNoRows {
		return p, biz.ErrConflict
	}
	if err != nil {
		return p, mapDBError(err)
	}
	if err = json.Unmarshal(raw, &p.Data); err != nil {
		return p, err
	}
	if r.cache != nil {
		if err := r.cache.Store(ctx, p); err != nil {
			slog.WarnContext(ctx, "current policy cache write failed; using database", "application_id", p.ApplicationID, "error", err)
		}
	}
	return p, nil
}
