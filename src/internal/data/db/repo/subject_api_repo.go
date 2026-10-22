package repo

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luck/nexus-authorization/internal/biz"
)

// subjectAPIRefreshTimeout bounds one publication after a committed save,
// including the wait for another instance's rebuild lease.
const subjectAPIRefreshTimeout = 90 * time.Second

// SubjectAPICache is the direct Redis authority for runtime service API
// authorization. PostgreSQL is used to build records after a control-plane
// save; authorization never falls back to it.
type SubjectAPICache interface {
	LoadSubjectAPIs(context.Context, biz.SubjectAPIIdentity) (biz.SubjectAPIPolicy, error)
	RequestSubjectAPIRefresh(context.Context, []string) (map[string]int64, error)
	AcquireSubjectAPILease(context.Context, []string) (biz.SubjectAPILease, error)
	SubjectAPIStatuses(context.Context, []string) (map[string]biz.SubjectAPIPublication, error)
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
func (r *RBACV1Repository) buildSubjectAPIPolicies(ctx context.Context, appKey string, catalog map[string]bool) ([]biz.SubjectAPIPolicy, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, mapDBError(err)
	}
	defer tx.Rollback(ctx)

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
	return policies, tx.Commit(ctx)
}

// uniqueApplicationKeys trims, drops empty values and deduplicates.
func uniqueApplicationKeys(appKeys []string) []string {
	seen := make(map[string]struct{}, len(appKeys))
	unique := make([]string, 0, len(appKeys))
	for _, appKey := range appKeys {
		appKey = strings.TrimSpace(appKey)
		if appKey == "" {
			continue
		}
		if _, exists := seen[appKey]; !exists {
			seen[appKey] = struct{}{}
			unique = append(unique, appKey)
		}
	}
	return unique
}

func (r *RBACV1Repository) refreshSubjectAPIApplication(ctx context.Context, appKey string) error {
	return r.refreshSubjectAPIApplications(ctx, []string{appKey}, false)
}

// refreshSubjectAPIApplications publishes the given applications after a
// committed save. It detaches from the caller's cancellation so an aborted
// HTTP request cannot leave a half-finished rebuild behind. A failure is
// reported as biz.ErrPublishPending: the save is already committed and the
// reconciler will publish it later.
func (r *RBACV1Repository) refreshSubjectAPIApplications(ctx context.Context, appKeys []string, blockFirst bool) error {
	if r.cache == nil {
		return nil
	}
	keys := uniqueApplicationKeys(appKeys)
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subjectAPIRefreshTimeout)
	defer cancel()
	tickets, err := r.cache.RequestSubjectAPIRefresh(ctx, keys)
	if err == nil {
		err = r.publishSubjectAPIApplications(ctx, keys, tickets, blockFirst)
	}
	if err != nil {
		r.markUnpublished(keys)
		return fmt.Errorf("%w: %w", biz.ErrPublishPending, err)
	}
	return nil
}

// markUnpublished remembers applications whose publication failed so the next
// reconcile pass republishes them even if Redis never recorded the request.
func (r *RBACV1Repository) markUnpublished(appKeys []string) {
	r.unpublishedMu.Lock()
	defer r.unpublishedMu.Unlock()
	if r.unpublished == nil {
		r.unpublished = make(map[string]struct{})
	}
	for _, appKey := range appKeys {
		r.unpublished[appKey] = struct{}{}
	}
}

func (r *RBACV1Repository) takeUnpublished() map[string]struct{} {
	r.unpublishedMu.Lock()
	defer r.unpublishedMu.Unlock()
	taken := r.unpublished
	r.unpublished = nil
	return taken
}

// publishSubjectAPIApplications rebuilds every leased application whose
// records do not yet cover its ticket. The lease is taken before the database
// snapshot, so the snapshot includes every save that requested a rebuild
// before the lease was granted. With blockFirst every gate closes before any
// rebuild starts (used when a shared service changed); otherwise each
// application keeps serving its previous records until its own replace.
func (r *RBACV1Repository) publishSubjectAPIApplications(ctx context.Context, keys []string, tickets map[string]int64, blockFirst bool) error {
	lease, err := r.cache.AcquireSubjectAPILease(ctx, keys)
	if err != nil {
		return err
	}
	defer lease.Release(ctx)
	pending, err := lease.Pending(ctx, tickets)
	if err != nil || len(pending) == 0 {
		return err
	}
	catalog, err := r.serviceCatalogSnapshot(ctx)
	if err != nil {
		return err
	}
	if blockFirst {
		if err := lease.Block(ctx, pending); err != nil {
			return err
		}
	}
	for _, appKey := range pending {
		policies, err := r.buildSubjectAPIPolicies(ctx, appKey, catalog)
		if err != nil {
			return err
		}
		if !blockFirst {
			if err := lease.Block(ctx, []string{appKey}); err != nil {
				return err
			}
		}
		if err := lease.Replace(ctx, appKey, policies); err != nil {
			return err
		}
	}
	return nil
}

// RefreshSubjectAPIForService rebuilds every application linked to a service.
func (r *RBACV1Repository) RefreshSubjectAPIForService(ctx context.Context, service string) error {
	apps, err := r.applicationKeysForServices(ctx, []string{strings.TrimSpace(service)})
	if err != nil {
		return err
	}
	return r.refreshSubjectAPIApplications(ctx, apps, true)
}

func (r *RBACV1Repository) applicationKeysForServices(ctx context.Context, services []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT a.app_key FROM applications a JOIN application_service_resources l ON l.application_id=a.id WHERE l.service_resource_key=ANY($1::text[]) ORDER BY a.app_key`, services)
	if err != nil {
		return nil, mapDBError(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ReconcileSubjectAPIs repairs Redis publication without waiting for another
// save. It republishes every application when full is true, and otherwise only
// applications whose gate is missing or not ready, whose published request
// number is behind (a save whose rebuild failed), or that link a service whose
// catalog status changed since the previous pass (remote catalogs change
// without a local save). It returns the number of applications republished.
func (r *RBACV1Repository) ReconcileSubjectAPIs(ctx context.Context, full bool) (int, error) {
	if r.cache == nil {
		return 0, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT app_key FROM applications ORDER BY app_key`)
	if err != nil {
		return 0, mapDBError(err)
	}
	apps, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, mapDBError(err)
	}
	catalog, err := r.serviceCatalogSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	// Passes are serialized so catalog change detection compares consecutive
	// snapshots.
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	previous := r.lastCatalog

	forced := make(map[string]bool)
	if full {
		for _, appKey := range apps {
			forced[appKey] = true
		}
	} else if previous != nil {
		changed := make([]string, 0)
		for key, active := range catalog {
			if before, ok := previous[key]; !ok || before != active {
				changed = append(changed, key)
			}
		}
		for key := range previous {
			if _, ok := catalog[key]; !ok {
				changed = append(changed, key)
			}
		}
		if len(changed) > 0 {
			affected, err := r.applicationKeysForServices(ctx, changed)
			if err != nil {
				return 0, err
			}
			for _, appKey := range affected {
				forced[appKey] = true
			}
		}
	}
	if len(apps) == 0 {
		r.lastCatalog = maps.Clone(catalog)
		return 0, nil
	}
	statuses, err := r.cache.SubjectAPIStatuses(ctx, apps)
	if err != nil {
		return 0, err
	}
	// Taken only now, after every read that can fail: each taken application
	// is either published below or marked again.
	for appKey := range r.takeUnpublished() {
		forced[appKey] = true
	}

	republished := 0
	var failures []error
	// A catalog change is only considered handled once every affected
	// application holds a rebuild request; a failed rebuild after that is
	// retried through its unpublished request number.
	requested := true
	for _, appKey := range apps {
		status := statuses[appKey]
		var tickets map[string]int64
		switch {
		case forced[appKey] || status.State != "ready":
			// Request a fresh rebuild that is newer than anything published.
			tickets, err = r.cache.RequestSubjectAPIRefresh(ctx, []string{appKey})
			if err != nil {
				requested = false
				r.markUnpublished([]string{appKey})
				failures = append(failures, fmt.Errorf("%s: %w", appKey, err))
				continue
			}
		case status.Published < status.Requested:
			// A save committed but its rebuild did not finish.
			tickets = map[string]int64{appKey: status.Requested}
		default:
			continue
		}
		if err := r.publishSubjectAPIApplications(ctx, []string{appKey}, tickets, false); err != nil {
			r.markUnpublished([]string{appKey})
			failures = append(failures, fmt.Errorf("%s: %w", appKey, err))
			continue
		}
		republished++
	}
	if requested {
		r.lastCatalog = maps.Clone(catalog)
	}
	return republished, errors.Join(failures...)
}
