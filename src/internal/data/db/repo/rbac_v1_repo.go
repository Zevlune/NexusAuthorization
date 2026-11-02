package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	cache   SubjectAPICache
}

// WithPolicyCache enables Redis publication of service API records.
func (r *RBACV1Repository) WithPolicyCache(cache SubjectAPICache) *RBACV1Repository {
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

func serviceResourceKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("%w: service resource key is required", biz.ErrInvalidArgument)
	}
	return key, nil
}

func (r *RBACV1Repository) resourceKey(ctx context.Context, key string) (string, error) {
	key, err := serviceResourceKey(key)
	if err != nil {
		return "", err
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
	scope, err := serviceResourceKey(key)
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

// generatedEndpointKey derives a unique endpoint key from the method and path.
// The readable part is truncated to fit the column; the hash suffix keeps keys
// distinct when different paths flatten to the same text (/a-b and /a/b).
func generatedEndpointKey(method, path string) string {
	sum := sha256.Sum256([]byte(method + " " + path))
	prefix := "endpoint:" + strings.ToLower(method) + ":"
	suffix := ":" + hex.EncodeToString(sum[:4])
	readable := strings.Trim(strings.NewReplacer("/", "-", "{", "", "}", "").Replace(path), "-")
	if limit := biz.MaxEndpointKeyLength - len(prefix) - len(suffix); len([]rune(readable)) > limit {
		readable = string([]rune(readable)[:limit])
	}
	return prefix + readable + suffix
}

func (r *RBACV1Repository) SaveEndpoint(ctx context.Context, key string, value biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	scope, err := r.resourceKey(ctx, key)
	if err != nil {
		return value, err
	}
	value, err = r.saveEndpoint(ctx, scope, value)
	if err != nil {
		return value, err
	}
	if err := r.RefreshSubjectAPIForService(ctx, scope); err != nil {
		return value, err
	}
	return value, nil
}

// SaveEndpoints saves endpoints one by one, like SaveEndpoint, but publishes
// the service's applications once at the end instead of after every endpoint.
// It stops at the first failure and returns the endpoints saved before it.
func (r *RBACV1Repository) SaveEndpoints(ctx context.Context, key string, values []biz.RBACEndpoint) ([]biz.RBACEndpoint, error) {
	scope, err := r.resourceKey(ctx, key)
	if err != nil {
		return nil, err
	}
	saved := make([]biz.RBACEndpoint, 0, len(values))
	var saveErr error
	for _, value := range values {
		item, err := r.saveEndpoint(ctx, scope, value)
		if err != nil {
			saveErr = err
			break
		}
		saved = append(saved, item)
	}
	if len(saved) > 0 {
		if err := r.RefreshSubjectAPIForService(ctx, scope); err != nil && saveErr == nil {
			saveErr = err
		}
	}
	return saved, saveErr
}

// saveEndpoint writes one endpoint of an already validated service without
// publishing.
func (r *RBACV1Repository) saveEndpoint(ctx context.Context, scope string, value biz.RBACEndpoint) (biz.RBACEndpoint, error) {
	value.Method = strings.ToUpper(strings.TrimSpace(value.Method))
	value.PathTemplate = strings.TrimSpace(value.PathTemplate)
	value.EndpointKey = strings.TrimSpace(value.EndpointKey)
	if value.ID == uuid.Nil && value.EndpointKey == "" {
		value.EndpointKey = generatedEndpointKey(value.Method, value.PathTemplate)
	}
	if value.Source == "" {
		value.Source = "manual"
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return value, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	// Serialize saves of one service and method so two concurrent saves cannot
	// both pass the route-shape check below.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "api_endpoints|"+scope+"|"+value.Method); err != nil {
		return value, mapDBError(err)
	}
	// Templates that differ only in parameter names or slashes match the same
	// requests, and runtime authorization denies such ambiguous routes. Reject
	// them here instead of silently breaking the existing route.
	rows, err := tx.Query(ctx, `SELECT id,path_template FROM api_endpoints WHERE service_resource_key=$1 AND method=$2`, scope, value.Method)
	if err != nil {
		return value, mapDBError(err)
	}
	type existingRoute struct {
		id   uuid.UUID
		path string
	}
	existing, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (existingRoute, error) {
		var route existingRoute
		return route, row.Scan(&route.id, &route.path)
	})
	if err != nil {
		return value, mapDBError(err)
	}
	shape := biz.RouteShape(value.PathTemplate)
	for _, route := range existing {
		if route.id != value.ID && biz.RouteShape(route.path) == shape {
			return value, fmt.Errorf("%w: %s %s already matches the same requests", biz.ErrAlreadyExists, value.Method, route.path)
		}
	}
	if value.ID == uuid.Nil {
		err = tx.QueryRow(ctx, `INSERT INTO api_endpoints(service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled,last_discovered_at,created_at,updated_at`, scope, value.EndpointKey, value.Method, value.PathTemplate, value.Module, value.Summary, value.Source, value.Enabled).Scan(&value.ID, &value.ServiceResource, &value.EndpointKey, &value.Method, &value.PathTemplate, &value.Module, &value.Summary, &value.Source, &value.Enabled, &value.LastDiscoveredAt, &value.CreatedAt, &value.UpdatedAt)
	} else {
		// An empty endpoint_key keeps the stored key.
		err = tx.QueryRow(ctx, `UPDATE api_endpoints SET method=$3,path_template=$4,module=$5,summary=$6,enabled=$7,endpoint_key=COALESCE(NULLIF($8,''),endpoint_key),updated_at=NOW() WHERE id=$1 AND service_resource_key=$2 RETURNING id,service_resource_key,endpoint_key,method,path_template,module,summary,source,enabled,last_discovered_at,created_at,updated_at`, value.ID, scope, value.Method, value.PathTemplate, value.Module, value.Summary, value.Enabled, value.EndpointKey).Scan(&value.ID, &value.ServiceResource, &value.EndpointKey, &value.Method, &value.PathTemplate, &value.Module, &value.Summary, &value.Source, &value.Enabled, &value.LastDiscoveredAt, &value.CreatedAt, &value.UpdatedAt)
	}
	if err != nil {
		return value, mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return value, mapDBError(err)
	}
	return value, nil
}
func (r *RBACV1Repository) DeleteEndpoint(ctx context.Context, key string, id uuid.UUID) error {
	scope, err := serviceResourceKey(key)
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
	return r.RefreshSubjectAPIForService(ctx, scope)
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
	value.EndpointIDs = uniqueUUIDs(value.EndpointIDs)
	value.MenuIDs = uniqueUUIDs(value.MenuIDs)
	// Read the (possibly remote) catalog before taking the row lock, so the
	// lock is never held across a network call.
	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return value, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return value, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, scope); err != nil {
		return value, mapDBError(err)
	}
	if value.ID == uuid.Nil {
		err = tx.QueryRow(ctx, `INSERT INTO roles(application_id,code,name,description,enabled)VALUES($1,$2,$3,$4,$5)RETURNING id,application_id,code,name,description,enabled`, scope, strings.TrimSpace(value.Code), strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), value.Enabled).Scan(&value.ID, &value.ApplicationID, &value.Code, &value.Name, &value.Description, &value.Enabled)
	} else {
		err = tx.QueryRow(ctx, `UPDATE roles SET name=$3,description=$4,enabled=$5,updated_at=NOW() WHERE id=$1 AND application_id=$2 RETURNING id,application_id,code,name,description,enabled`, value.ID, scope, strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), value.Enabled).Scan(&value.ID, &value.ApplicationID, &value.Code, &value.Name, &value.Description, &value.Enabled)
	}
	if err != nil {
		return value, mapDBError(err)
	}
	var endpointCount int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM api_endpoints e JOIN application_service_resources a ON a.service_resource_key=e.service_resource_key WHERE a.application_id=$1 AND e.id=ANY($2::uuid[]) AND e.service_resource_key=ANY($3::text[])`, scope, value.EndpointIDs, activeKeys).Scan(&endpointCount); err != nil {
		return value, mapDBError(err)
	}
	if endpointCount != len(value.EndpointIDs) {
		return value, fmt.Errorf("%w: an endpoint belongs to an unlinked or disabled service", biz.ErrConflict)
	}
	// Only add and remove the API grants that changed; unchanged rows stay.
	if _, err = tx.Exec(ctx, `DELETE FROM role_api_endpoint_bindings WHERE role_id=$1 AND NOT (endpoint_id=ANY(COALESCE($2::uuid[],'{}'::uuid[])))`, value.ID, value.EndpointIDs); err != nil {
		return value, mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_api_endpoint_bindings(role_id,endpoint_id) SELECT $1,unnest($2::uuid[]) ON CONFLICT DO NOTHING`, value.ID, value.EndpointIDs); err != nil {
		return value, mapDBError(err)
	}
	var menuCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM menus WHERE application_id=$1 AND id=ANY($2::uuid[])`, scope, value.MenuIDs).Scan(&menuCount); err != nil {
		return value, mapDBError(err)
	}
	if menuCount != len(value.MenuIDs) {
		return value, fmt.Errorf("%w: menu outside application", biz.ErrInvalidArgument)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM role_menu_bindings WHERE role_id=$1`, value.ID); err != nil {
		return value, mapDBError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_menu_bindings(role_id,menu_id) SELECT $1,unnest($2::uuid[])`, value.ID, value.MenuIDs); err != nil {
		return value, mapDBError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return value, mapDBError(err)
	}
	if err := r.refreshSubjectAPIApplication(ctx, key); err != nil {
		return value, err
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
	return r.refreshSubjectAPIApplication(ctx, key)
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

// uniqueUUIDs drops duplicates while keeping order, so repeated IDs in a
// request are not mistaken for IDs outside the application.
func uniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	return unique
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
	roleIDs = uniqueUUIDs(roleIDs)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, scope); err != nil {
		return mapDBError(err)
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
	if err := mapDBError(tx.Commit(ctx)); err != nil {
		return err
	}
	return r.refreshSubjectAPIApplication(ctx, key)
}

// UpdateRoleSubjects grants one role to the subjects in add and revokes it from
// the subjects in remove, in one domain and one transaction, then publishes the
// application once. Other roles of the same subjects are never touched.
func (r *RBACV1Repository) UpdateRoleSubjects(ctx context.Context, key string, roleID uuid.UUID, domain string, add, remove []string) error {
	scope, err := r.applicationID(ctx, key)
	if err != nil {
		return err
	}
	domain = biz.NormalizeRBACDomain(domain)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, scope); err != nil {
		return mapDBError(err)
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM roles WHERE id=$1 AND application_id=$2`, roleID, scope).Scan(&enabled); err != nil {
		return mapDBError(err)
	}
	if len(add) > 0 && !enabled {
		return fmt.Errorf("%w: a disabled role cannot be assigned", biz.ErrConflict)
	}
	if len(remove) > 0 {
		// The default domain also clears retired "global"/blank aliases, like
		// ReplaceSubjectRoles.
		if domain == "" {
			_, err = tx.Exec(ctx, `DELETE FROM subject_role_bindings WHERE application_id=$1 AND role_id=$2 AND subject=ANY($3::text[]) AND btrim(domain) IN ('global', '')`, scope, roleID, remove)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM subject_role_bindings WHERE application_id=$1 AND role_id=$2 AND subject=ANY($3::text[]) AND domain=$4`, scope, roleID, remove, domain)
		}
		if err != nil {
			return mapDBError(err)
		}
	}
	if len(add) > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO subject_role_bindings(application_id,subject,domain,role_id) SELECT $1,unnest($2::text[]),$3,$4 ON CONFLICT DO NOTHING`, scope, add, domain, roleID); err != nil {
			return mapDBError(err)
		}
	}
	if err := mapDBError(tx.Commit(ctx)); err != nil {
		return err
	}
	return r.refreshSubjectAPIApplication(ctx, key)
}

func (r *RBACV1Repository) applicationID(ctx context.Context, key string) (uuid.UUID, error) {
	var id uuid.UUID
	if strings.TrimSpace(key) == "" {
		return id, fmt.Errorf("%w: app_key is required", biz.ErrInvalidArgument)
	}
	err := r.pool.QueryRow(ctx, `SELECT id FROM applications WHERE app_key=$1 AND enabled`, key).Scan(&id)
	return id, mapDBError(err)
}

func (r *RBACV1Repository) RequireLinkedApplicationService(ctx context.Context, app, service string) error {
	var appErr error
	if strings.TrimSpace(app) == "" {
		appErr = fmt.Errorf("%w: app_key is required", biz.ErrInvalidArgument)
	}
	service, serviceErr := serviceResourceKey(service)
	if appErr != nil {
		return appErr
	}
	if serviceErr != nil {
		return serviceErr
	}
	var linked bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM applications a
		JOIN application_service_resources l ON l.application_id=a.id
		WHERE a.app_key=$1 AND a.enabled AND l.service_resource_key=$2
	)`, strings.TrimSpace(app), service).Scan(&linked)
	if err != nil {
		return mapDBError(err)
	}
	if !linked {
		return fmt.Errorf("%w: service is not linked to application", biz.ErrInvalidArgument)
	}
	return nil
}

func (r *RBACV1Repository) ListApplicationEndpoints(ctx context.Context, app, service string, page, size int) ([]biz.RBACEndpoint, int, error) {
	app = strings.TrimSpace(app)
	service = strings.TrimSpace(service)
	aid, err := r.applicationID(ctx, app)
	if err != nil {
		return nil, 0, err
	}
	var total int
	where := ` FROM api_endpoints e JOIN application_service_resources a ON a.service_resource_key=e.service_resource_key WHERE a.application_id=$1 AND ($2='' OR e.service_resource_key=$2)`
	if err = r.pool.QueryRow(ctx, `SELECT count(*)`+where, aid, service).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT e.id,e.service_resource_key,e.endpoint_key,e.method,e.path_template,e.module,e.summary,e.source,e.enabled,e.last_discovered_at,e.created_at,e.updated_at`+where+` ORDER BY e.service_resource_key,e.module,e.path_template,e.method LIMIT $3 OFFSET $4`, aid, service, size, (page-1)*size)
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
