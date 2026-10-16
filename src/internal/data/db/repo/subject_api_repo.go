package repo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

// SubjectAPICache is the direct Redis authority for runtime service API
// authorization. PostgreSQL is used to build records after a control-plane
// save; authorization never falls back to it.
type SubjectAPICache interface {
	LoadSubjectAPIs(context.Context, biz.SubjectAPIIdentity) (biz.SubjectAPIPolicy, error)
	BlockSubjectAPIApplications(context.Context, []string) error
	ReplaceSubjectAPIApplication(context.Context, string, []biz.SubjectAPIPolicy) error
}

var _ biz.SubjectAPIRepository = (*RBACV1Repository)(nil)

func (r *RBACV1Repository) GetSubjectAPIs(ctx context.Context, app, subject, domain, service string) (biz.SubjectAPIPolicy, error) {
	cache, ok := r.cache.(SubjectAPICache)
	if !ok || cache == nil {
		return biz.SubjectAPIPolicy{}, fmt.Errorf("subject API Redis cache is not configured")
	}
	return cache.LoadSubjectAPIs(ctx, biz.SubjectAPIIdentity{
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

func (r *RBACV1Repository) buildSubjectAPIPolicies(ctx context.Context, appKey string) ([]biz.SubjectAPIPolicy, error) {
	var in subjectAPIBuildInput
	if err := r.pool.QueryRow(ctx, `SELECT id,app_key,enabled FROM applications WHERE app_key=$1`, appKey).Scan(&in.appID, &in.appKey, &in.appEnabled); err != nil {
		return nil, mapDBError(err)
	}

	rows, err := r.pool.Query(ctx, `SELECT service_resource_key FROM application_service_resources WHERE application_id=$1 ORDER BY service_resource_key`, in.appID)
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
		resource, catalogErr := r.catalog.Get(ctx, service)
		in.serviceLive[service] = catalogErr == nil && resource != nil && resource.IsActive
		endpointRows, queryErr := r.pool.Query(ctx, `SELECT id,method,path_template,enabled FROM api_endpoints WHERE service_resource_key=$1 ORDER BY method,path_template,id`, service)
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
	roleRows, err := r.pool.Query(ctx, `SELECT id,code,enabled FROM roles WHERE application_id=$1`, in.appID)
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

	grantRows, err := r.pool.Query(ctx, `SELECT b.role_id,b.endpoint_id FROM role_api_endpoint_bindings b JOIN roles r ON r.id=b.role_id WHERE r.application_id=$1`, in.appID)
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
	bindingRows, err := r.pool.Query(ctx, `SELECT subject,domain,role_id FROM subject_role_bindings WHERE application_id=$1 ORDER BY subject,domain,role_id`, in.appID)
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

func (r *RBACV1Repository) refreshSubjectAPIApplication(ctx context.Context, appKey string) error {
	cache, ok := r.cache.(SubjectAPICache)
	if !ok || cache == nil {
		return nil
	}
	appKey = strings.TrimSpace(appKey)
	policies, err := r.buildSubjectAPIPolicies(ctx, appKey)
	if err != nil {
		return err
	}
	if err := cache.BlockSubjectAPIApplications(ctx, []string{appKey}); err != nil {
		return err
	}
	return cache.ReplaceSubjectAPIApplication(ctx, appKey, policies)
}

func (r *RBACV1Repository) refreshSubjectAPIApplications(ctx context.Context, appKeys []string) error {
	cache, ok := r.cache.(SubjectAPICache)
	if !ok || cache == nil || len(appKeys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(appKeys))
	unique := make([]string, 0, len(appKeys))
	for _, appKey := range appKeys {
		appKey = strings.TrimSpace(appKey)
		if appKey != "" {
			if _, exists := seen[appKey]; !exists {
				seen[appKey] = struct{}{}
				unique = append(unique, appKey)
			}
		}
	}
	if len(unique) == 0 {
		return nil
	}
	if err := cache.BlockSubjectAPIApplications(ctx, unique); err != nil {
		return err
	}
	for _, appKey := range unique {
		policies, err := r.buildSubjectAPIPolicies(ctx, appKey)
		if err != nil {
			return err
		}
		if err := cache.ReplaceSubjectAPIApplication(ctx, appKey, policies); err != nil {
			return err
		}
	}
	return nil
}

// RefreshSubjectAPIForService rebuilds every application linked to a service.
func (r *RBACV1Repository) RefreshSubjectAPIForService(ctx context.Context, service string) error {
	rows, err := r.pool.Query(ctx, `SELECT a.app_key FROM applications a JOIN application_service_resources l ON l.application_id=a.id WHERE l.service_resource_key=$1 ORDER BY a.app_key`, strings.TrimSpace(service))
	if err != nil {
		return mapDBError(err)
	}
	defer rows.Close()
	var apps []string
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return mapDBError(err)
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return mapDBError(err)
	}
	return r.refreshSubjectAPIApplications(ctx, apps)
}
