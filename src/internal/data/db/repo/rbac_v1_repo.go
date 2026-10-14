package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	for _, endpointID := range value.EndpointIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO role_api_endpoint_bindings(role_id,endpoint_id) VALUES($1,$2)`, value.ID, endpointID); err != nil {
			return value, mapDBError(err)
		}
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
	for _, id := range value.MenuIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO role_menu_bindings(role_id,menu_id) VALUES($1,$2)`, value.ID, id); err != nil {
			return value, mapDBError(err)
		}
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
	for rows.Next() {
		var v biz.SubjectRoleBinding
		if err := rows.Scan(&v.ID, &v.ApplicationID, &v.Subject, &v.Domain, &v.RoleID); err != nil {
			return nil, mapDBError(err)
		}
		items = append(items, v)
	}
	return items, mapDBError(rows.Err())
}
func (r *RBACV1Repository) ReplaceSubjectRoles(ctx context.Context, key, subject, domain string, roleIDs []uuid.UUID) error {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return err
	}
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
	if _, err = tx.Exec(ctx, `DELETE FROM subject_role_bindings WHERE application_id=$1 AND subject=$2 AND domain=$3`, scope, subject, domain); err != nil {
		return mapDBError(err)
	}
	for _, roleID := range roleIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id)VALUES($1,$2,$3,$4)`, scope, subject, domain, roleID); err != nil {
			return mapDBError(err)
		}
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

// A single SQL statement captures a consistent view of the entire application.
const applicationSnapshotSQL = `SELECT jsonb_build_object(
 'app_key',a.app_key,'application_id',a.id,
 'service_resources',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',x.service_resource_key) ORDER BY x.service_resource_key) FROM application_service_resources x WHERE x.application_id=a.id AND x.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'endpoints',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'service_resource',e.service_resource_key,'method',e.method,'path_template',e.path_template,'enabled',e.enabled) ORDER BY e.id) FROM api_endpoints e JOIN application_service_resources x ON x.service_resource_key=e.service_resource_key WHERE x.application_id=a.id AND e.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'code',r.code,'enabled',r.enabled,'endpoint_ids',COALESCE((SELECT jsonb_agg(b.endpoint_id ORDER BY b.endpoint_id) FROM role_api_endpoint_bindings b WHERE b.role_id=r.id),'[]'::jsonb),'menu_ids',COALESCE((SELECT jsonb_agg(b.menu_id ORDER BY b.menu_id) FROM role_menu_bindings b WHERE b.role_id=r.id),'[]'::jsonb)) ORDER BY r.id) FROM roles r WHERE r.application_id=a.id),'[]'::jsonb),
 'subject_roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',b.subject,'domain',b.domain,'role_id',b.role_id) ORDER BY b.subject,b.domain,b.role_id) FROM subject_role_bindings b WHERE b.application_id=a.id),'[]'::jsonb),
 'menus',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'parent_id',m.parent_id,'code',m.code,'name',m.name,'menu_type',m.menu_type,'route',m.route,'component',m.component,'icon',m.icon,'sort_order',m.sort_order,'enabled',m.enabled) ORDER BY m.sort_order,m.id) FROM menus m WHERE m.application_id=a.id),'[]'::jsonb)
 ) FROM applications a WHERE a.id=$1 AND a.enabled`

func (r *RBACV1Repository) BuildRBACSnapshot(ctx context.Context, scope string) (map[string]any, error) {
	aid, err := r.applicationID(ctx, scope)
	if err != nil {
		return nil, err
	}
	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = r.pool.QueryRow(ctx, applicationSnapshotSQL, aid, activeKeys).Scan(&raw); err != nil {
		return nil, mapDBError(err)
	}
	var snapshot map[string]any
	err = json.Unmarshal(raw, &snapshot)
	return snapshot, err
}

func createApplicationRelease(ctx context.Context, tx pgx.Tx, aid uuid.UUID, description, actor string, snapshot map[string]any) (biz.RBACRelease, error) {
	var v biz.RBACRelease
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return v, err
	}
	hash := sha256.Sum256(raw)
	v = biz.RBACRelease{ID: uuid.New(), ApplicationID: aid, Description: description, PublishedBy: actor, Checksum: hex.EncodeToString(hash[:]), Status: "published"}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM rbac_releases WHERE application_id=$1`, aid).Scan(&v.Version); err != nil {
		return v, err
	}
	if _, err = tx.Exec(ctx, `UPDATE rbac_releases SET status='superseded' WHERE application_id=$1 AND status='published'`, aid); err != nil {
		return v, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO rbac_releases(id,application_id,version,status,description,checksum,published_by) VALUES($1,$2,$3,'published',$4,$5,$6) RETURNING published_at`, v.ID, aid, v.Version, description, v.Checksum, actor).Scan(&v.PublishedAt); err != nil {
		return v, mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rbac_release_snapshots(release_id,snapshot) VALUES($1,$2::jsonb)`, v.ID, raw); err != nil {
		return v, mapDBError(err)
	}
	return v, nil
}
func (r *RBACV1Repository) CreateRBACRelease(ctx context.Context, scope, description, actor string, snapshot map[string]any) (biz.RBACRelease, error) {
	aid, err := r.applicationID(ctx, scope)
	if err != nil {
		return biz.RBACRelease{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return biz.RBACRelease{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, aid); err != nil {
		return biz.RBACRelease{}, err
	}
	v, err := createApplicationRelease(ctx, tx, aid, description, actor, snapshot)
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (r *RBACV1Repository) PublishApplication(ctx context.Context, scope, description, actor string) (biz.RBACRelease, error) {
	aid, err := r.applicationID(ctx, scope)
	if err != nil {
		return biz.RBACRelease{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return biz.RBACRelease{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, aid); err != nil {
		return biz.RBACRelease{}, err
	}
	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return biz.RBACRelease{}, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, applicationSnapshotSQL, aid, activeKeys).Scan(&raw); err != nil {
		return biz.RBACRelease{}, err
	}
	var snapshot map[string]any
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return biz.RBACRelease{}, err
	}
	v, err := createApplicationRelease(ctx, tx, aid, description, actor, snapshot)
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (r *RBACV1Repository) ListRBACReleases(ctx context.Context, scope string) ([]biz.RBACRelease, error) {
	aid, err := r.applicationID(ctx, scope)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,application_id,version,status,description,checksum,published_by,published_at FROM rbac_releases WHERE application_id=$1 ORDER BY version DESC`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []biz.RBACRelease{}
	for rows.Next() {
		var v biz.RBACRelease
		if err = rows.Scan(&v.ID, &v.ApplicationID, &v.Version, &v.Status, &v.Description, &v.Checksum, &v.PublishedBy, &v.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *RBACV1Repository) GetActiveRBACRelease(ctx context.Context, scope string) (biz.RBACRelease, error) {
	var v biz.RBACRelease
	aid, err := r.applicationID(ctx, scope)
	if err != nil {
		return v, err
	}
	var raw []byte
	err = r.pool.QueryRow(ctx, `SELECT r.id,r.application_id,r.version,r.status,r.description,r.checksum,r.published_by,r.published_at,s.snapshot FROM rbac_releases r JOIN rbac_release_snapshots s ON s.release_id=r.id WHERE r.application_id=$1 AND r.status='published' ORDER BY r.version DESC LIMIT 1`, aid).Scan(&v.ID, &v.ApplicationID, &v.Version, &v.Status, &v.Description, &v.Checksum, &v.PublishedBy, &v.PublishedAt, &raw)
	if err != nil {
		return v, mapDBError(err)
	}
	err = json.Unmarshal(raw, &v.Snapshot)
	return v, err
}
