package repo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luck/nexus-authorization/internal/biz"
)

// subjectAPIRefreshTimeout bounds one publication after a committed save,
// including the wait for another instance's PostgreSQL publication lock.
const subjectAPIRefreshTimeout = 90 * time.Second

// SubjectAPICache is the direct Redis authority for runtime service API
// authorization. PostgreSQL is used to build records after a control-plane
// save; authorization never falls back to it.
type SubjectAPICache interface {
	LoadSubjectAPIs(context.Context, biz.SubjectAPIIdentity) (biz.SubjectAPIPolicy, error)
	ReplaceSubjectAPIs(context.Context, []biz.SubjectAPIPolicy) error
}

var _ biz.SubjectAPIRepository = (*RBACV1Repository)(nil)

func (r *RBACV1Repository) GetSubjectAPIs(ctx context.Context, app, subject, domain, service string) (biz.SubjectAPIPolicy, error) {
	if r.cache == nil {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("subject API Redis cache is not configured")
	}
	return r.cache.LoadSubjectAPIs(ctx, biz.SubjectAPIIdentity{
		ApplicationKey:  strings.TrimSpace(app),
		Subject:         strings.TrimSpace(subject),
		Domain:          biz.NormalizeRBACDomain(domain),
		ServiceResource: strings.TrimSpace(service),
	})
}

type subjectAPIRole struct {
	id       uuid.UUID
	code     string
	enabled  bool
	endpoint map[uuid.UUID]struct{}
}

type subjectAPIBuildInput struct {
	appID       uuid.UUID
	appKey      string
	appEnabled  bool
	services    []string
	serviceLive map[string]bool
	endpoints   map[string][]biz.SubjectAPIEndpoint
	roles       map[uuid.UUID]subjectAPIRole
	bindings    map[string][]uuid.UUID
}

func subjectAPIEndpointFromRow(row interface{ Scan(...any) error }) (biz.SubjectAPIEndpoint, error) {
	var endpoint biz.SubjectAPIEndpoint
	if err := row.Scan(&endpoint.ID, &endpoint.Method, &endpoint.PathTemplate, &endpoint.Enabled); err != nil {
		return biz.SubjectAPIEndpoint{}, mapDBError(err)
	}
	return endpoint, nil
}

// serviceCatalogSnapshot lists the configured service catalog once per
// rebuild. A failure aborts the rebuild so a transient catalog error can never
// be published as "every service is disabled".
func (r *RBACV1Repository) serviceCatalogSnapshot(ctx context.Context) (map[string]bool, error) {
	resources, err := r.catalog.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read service catalog: %w", err)
	}
	active := make(map[string]bool, len(resources))
	for _, resource := range resources {
		if resource != nil {
			active[resource.Key] = resource.IsActive
		}
	}
	return active, nil
}

// buildSubjectAPIPolicies reads one application's records in a single
// repeatable-read snapshot and aggregates them per subject, domain and service.
func (r *RBACV1Repository) buildSubjectAPIPolicies(ctx context.Context, tx pgx.Tx, appKey string, catalog map[string]bool) ([]biz.SubjectAPIPolicy, error) {
	var in subjectAPIBuildInput
	if err := tx.QueryRow(ctx, `SELECT id,app_key,enabled FROM applications WHERE app_key=$1`, appKey).Scan(&in.appID, &in.appKey, &in.appEnabled); err != nil {
		return nil, mapDBError(err)
	}

	rows, err := tx.Query(ctx, `SELECT service_resource_key FROM application_service_resources WHERE application_id=$1 ORDER BY service_resource_key`, in.appID)
	if err != nil {
		return nil, mapDBError(err)
	}
	for rows.Next() {
		var service string
		if err := rows.Scan(&service); err != nil {
			rows.Close()
			return nil, mapDBError(err)
		}
		in.services = append(in.services, service)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, mapDBError(err)
	}
	rows.Close()

	in.serviceLive = make(map[string]bool, len(in.services))
	in.endpoints = make(map[string][]biz.SubjectAPIEndpoint, len(in.services))
	for _, service := range in.services {
		in.serviceLive[service] = catalog[service]
		endpointRows, queryErr := tx.Query(ctx, `SELECT id,method,path_template,enabled FROM api_endpoints WHERE service_resource_key=$1 ORDER BY method,path_template,id`, service)
		if queryErr != nil {
			return nil, mapDBError(queryErr)
		}
		for endpointRows.Next() {
			endpoint, scanErr := subjectAPIEndpointFromRow(endpointRows)
			if scanErr != nil {
				endpointRows.Close()
				return nil, scanErr
			}
			in.endpoints[service] = append(in.endpoints[service], endpoint)
		}
		if err := endpointRows.Err(); err != nil {
			endpointRows.Close()
			return nil, mapDBError(err)
		}
		endpointRows.Close()
	}

	in.roles = make(map[uuid.UUID]subjectAPIRole)
	roleRows, err := tx.Query(ctx, `SELECT id,code,enabled FROM roles WHERE application_id=$1`, in.appID)
	if err != nil {
		return nil, mapDBError(err)
	}
	for roleRows.Next() {
		var role subjectAPIRole
		if err := roleRows.Scan(&role.id, &role.code, &role.enabled); err != nil {
			roleRows.Close()
			return nil, mapDBError(err)
		}
		role.endpoint = make(map[uuid.UUID]struct{})
		in.roles[role.id] = role
	}
	if err := roleRows.Err(); err != nil {
		roleRows.Close()
		return nil, mapDBError(err)
	}
	roleRows.Close()

	grantRows, err := tx.Query(ctx, `SELECT b.role_id,b.endpoint_id FROM role_api_endpoint_bindings b JOIN roles r ON r.id=b.role_id WHERE r.application_id=$1`, in.appID)
	if err != nil {
		return nil, mapDBError(err)
	}
	for grantRows.Next() {
		var roleID, endpointID uuid.UUID
		if err := grantRows.Scan(&roleID, &endpointID); err != nil {
			grantRows.Close()
			return nil, mapDBError(err)
		}
		role := in.roles[roleID]
		role.endpoint[endpointID] = struct{}{}
		in.roles[roleID] = role
	}
	if err := grantRows.Err(); err != nil {
		grantRows.Close()
		return nil, mapDBError(err)
	}
	grantRows.Close()

	in.bindings = make(map[string][]uuid.UUID)
	bindingRows, err := tx.Query(ctx, `SELECT subject,domain,role_id FROM subject_role_bindings WHERE application_id=$1 ORDER BY subject,domain,role_id`, in.appID)
	if err != nil {
		return nil, mapDBError(err)
	}
	for bindingRows.Next() {
		var subject, domain string
		var roleID uuid.UUID
		if err := bindingRows.Scan(&subject, &domain, &roleID); err != nil {
			bindingRows.Close()
			return nil, mapDBError(err)
		}
		key := strings.TrimSpace(subject) + "\x00" + biz.NormalizeRBACDomain(domain)
		in.bindings[key] = append(in.bindings[key], roleID)
	}
	if err := bindingRows.Err(); err != nil {
		bindingRows.Close()
		return nil, mapDBError(err)
	}
	bindingRows.Close()

	keys := make([]string, 0, len(in.bindings))
	for key := range in.bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	policies := make([]biz.SubjectAPIPolicy, 0, len(keys)*len(in.services))
	for _, bindingKey := range keys {
		parts := strings.SplitN(bindingKey, "\x00", 2)
		subject, domain := parts[0], parts[1]
		roleIDs := in.bindings[bindingKey]
		roleCodes := make([]string, 0, len(roleIDs))
		granted := make(map[uuid.UUID]struct{})
		seenRoles := make(map[uuid.UUID]struct{})
		for _, roleID := range roleIDs {
			if _, seen := seenRoles[roleID]; seen {
				continue
			}
			seenRoles[roleID] = struct{}{}
			role, exists := in.roles[roleID]
			if !exists || !role.enabled {
				continue
			}
			roleCodes = append(roleCodes, role.code)
			for endpointID := range role.endpoint {
				granted[endpointID] = struct{}{}
			}
		}
		sort.Strings(roleCodes)
		for _, service := range in.services {
			catalog := append([]biz.SubjectAPIEndpoint(nil), in.endpoints[service]...)
			apis := make([]biz.SubjectAPIEndpoint, 0, len(catalog))
			if in.appEnabled && in.serviceLive[service] {
				for _, endpoint := range catalog {
					if endpoint.Enabled {
						if _, ok := granted[endpoint.ID]; ok {
							apis = append(apis, endpoint)
						}
					}
				}
			}
			policies = append(policies, biz.SubjectAPIPolicy{
				SubjectAPIIdentity: biz.SubjectAPIIdentity{ApplicationKey: in.appKey, ApplicationID: in.appID, Subject: subject, Domain: domain, ServiceResource: service, ServiceActive: in.appEnabled && in.serviceLive[service]},
				APIs:               apis, Catalog: catalog, RoleCodes: append([]string(nil), roleCodes...),
			})
		}
	}
	return policies, nil
}

// refreshSubjectAPIApplication rebuilds service-level permissions after a
// committed control-plane save. Contributions from every application must be
// recomputed together, otherwise one application's revoke could erase another.
func (r *RBACV1Repository) refreshSubjectAPIApplication(ctx context.Context, _ string) error {
	return r.refreshAllSubjectAPIs(ctx)
}

func (r *RBACV1Repository) refreshAllSubjectAPIs(ctx context.Context) error {
	if r.cache == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subjectAPIRefreshTimeout)
	defer cancel()
	if _, err := r.publishSubjectAPIs(ctx); err != nil {
		// Periodic full reconciliation and the startup pass retry from current
		// database state, including saves that failed before reaching Redis.
		return fmt.Errorf("%w: %w", biz.ErrPublishPending, err)
	}
	return nil
}

// RefreshSubjectAPIForService also refreshes removed service associations and
// shared-service contributions; the final snapshot owns all business keys.
func (r *RBACV1Repository) RefreshSubjectAPIForService(ctx context.Context, _ string) error {
	return r.refreshAllSubjectAPIs(ctx)
}

// The lock is held on a dedicated PostgreSQL session across the snapshot and
// Redis publication. Taking it before BeginTx avoids a waiting repeatable-read
// transaction retaining a snapshot older than a completed publisher's revoke.
// No coordination keys are written to Redis.
const subjectAPIPublicationLock int64 = 0x4e58534150495055

// acquireSubjectAPIPublicationConn does not hold pooled connections while
// waiting. Once locked, Hijack gives the publisher a dedicated session, leaving
// the pool available for control-plane commits and catalog reads even at size 1.
func (r *RBACV1Repository) acquireSubjectAPIPublicationConn(ctx context.Context) (*pgx.Conn, error) {
	for {
		pooled, err := r.pool.Acquire(ctx)
		if err != nil {
			return nil, mapDBError(err)
		}
		var locked bool
		err = pooled.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, subjectAPIPublicationLock).Scan(&locked)
		if err != nil {
			// The reply may be lost after acquiring the session lock.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = pooled.Hijack().Close(cleanupCtx)
			cancel()
			return nil, mapDBError(err)
		}
		if locked {
			return pooled.Hijack(), nil
		}
		pooled.Release()
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *RBACV1Repository) publishSubjectAPIs(ctx context.Context) (int, error) {
	conn, err := r.acquireSubjectAPIPublicationConn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Closing the dedicated session releases its advisory lock, including
		// error paths, without returning a locked connection to the pool.
		_ = conn.Close(cleanupCtx)
	}()

	catalog, err := r.serviceCatalogSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, mapDBError(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT app_key FROM applications ORDER BY app_key`)
	if err != nil {
		return 0, mapDBError(err)
	}
	apps, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, mapDBError(err)
	}
	var contributions []biz.SubjectAPIPolicy
	for _, appKey := range apps {
		policies, err := r.buildSubjectAPIPolicies(ctx, tx, appKey, catalog)
		if err != nil {
			return 0, err
		}
		contributions = append(contributions, policies...)
	}
	policies, err := mergeServiceSubjectAPIPolicies(contributions)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, mapDBError(err)
	}
	// The session lock remains held after Commit until the complete publish
	// finishes. Each Redis value is replaced atomically, not the whole set.
	if err := r.cache.ReplaceSubjectAPIs(ctx, policies); err != nil {
		return 0, err
	}
	return len(apps), nil
}

// ReconcileSubjectAPIs always recomputes the full service/user snapshot. It
// repairs missing values, failed saves, deleted bindings and remote catalog
// changes without storing readiness, indexes or counters in Redis. The full
// argument remains for the startup/background caller contract.
func (r *RBACV1Repository) ReconcileSubjectAPIs(ctx context.Context, _ bool) (int, error) {
	if r.cache == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, subjectAPIRefreshTimeout)
	defer cancel()
	return r.publishSubjectAPIs(ctx)
}

// mergeServiceSubjectAPIPolicies gives each service/user/domain exactly one
// value. Only active application contributions grant APIs or roles; catalog
// routes remain complete so route specificity cannot widen a user's grants.
func mergeServiceSubjectAPIPolicies(contributions []biz.SubjectAPIPolicy) ([]biz.SubjectAPIPolicy, error) {
	type scope struct{ service, subject, domain string }
	type aggregate struct {
		policy  biz.SubjectAPIPolicy
		apis    map[uuid.UUID]biz.SubjectAPIEndpoint
		catalog map[uuid.UUID]biz.SubjectAPIEndpoint
		roles   map[string]struct{}
	}
	groups := make(map[scope]*aggregate)
	for _, source := range contributions {
		key := scope{strings.TrimSpace(source.ServiceResource), strings.TrimSpace(source.Subject), biz.NormalizeRBACDomain(source.Domain)}
		if key.service == "" || key.subject == "" {
			return nil, fmt.Errorf("invalid service subject policy identity")
		}
		group := groups[key]
		if group == nil {
			group = &aggregate{
				policy: biz.SubjectAPIPolicy{SubjectAPIIdentity: biz.SubjectAPIIdentity{ServiceResource: key.service, Subject: key.subject, Domain: key.domain}},
				apis:   make(map[uuid.UUID]biz.SubjectAPIEndpoint), catalog: make(map[uuid.UUID]biz.SubjectAPIEndpoint), roles: make(map[string]struct{}),
			}
			groups[key] = group
		}
		for _, endpoint := range source.Catalog {
			if existing, ok := group.catalog[endpoint.ID]; ok && existing != endpoint {
				return nil, fmt.Errorf("conflicting service API catalog entry %s", endpoint.ID)
			}
			group.catalog[endpoint.ID] = endpoint
		}
		if !source.ServiceActive {
			continue
		}
		group.policy.ServiceActive = true
		for _, endpoint := range source.APIs {
			group.apis[endpoint.ID] = endpoint
		}
		for _, role := range source.RoleCodes {
			if role = strings.TrimSpace(role); role != "" {
				group.roles[role] = struct{}{}
			}
		}
	}
	keys := make([]scope, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.service != b.service {
			return a.service < b.service
		}
		if a.subject != b.subject {
			return a.subject < b.subject
		}
		return a.domain < b.domain
	})
	result := make([]biz.SubjectAPIPolicy, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		for _, endpoint := range group.apis {
			group.policy.APIs = append(group.policy.APIs, endpoint)
		}
		for _, endpoint := range group.catalog {
			group.policy.Catalog = append(group.policy.Catalog, endpoint)
		}
		less := func(a, b biz.SubjectAPIEndpoint) bool {
			if a.Method != b.Method {
				return a.Method < b.Method
			}
			if a.PathTemplate != b.PathTemplate {
				return a.PathTemplate < b.PathTemplate
			}
			return a.ID.String() < b.ID.String()
		}
		sort.Slice(group.policy.APIs, func(i, j int) bool { return less(group.policy.APIs[i], group.policy.APIs[j]) })
		sort.Slice(group.policy.Catalog, func(i, j int) bool { return less(group.policy.Catalog[i], group.policy.Catalog[j]) })
		for role := range group.roles {
			group.policy.RoleCodes = append(group.policy.RoleCodes, role)
		}
		sort.Strings(group.policy.RoleCodes)
		result = append(result, group.policy)
	}
	return result, nil
}
