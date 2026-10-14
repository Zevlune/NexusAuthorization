package repo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luck/nexus-authorization/internal/biz"
)

const applicationColumns = `id,app_key,display_name,description,enabled,url`

var _ biz.ApplicationRepository = (*RBACV1Repository)(nil)

func (r *RBACV1Repository) applicationIDForKey(ctx context.Context, key string) (uuid.UUID, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return uuid.Nil, fmt.Errorf("%w: app_key is required", biz.ErrInvalidArgument)
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM applications WHERE app_key=$1`, key).Scan(&id)
	return id, mapDBError(err)
}

func (r *RBACV1Repository) ListApplications(ctx context.Context) ([]biz.Application, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+applicationColumns+` FROM applications ORDER BY display_name,app_key`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := make([]biz.Application, 0)
	for rows.Next() {
		var item biz.Application
		if err := rows.Scan(&item.ID, &item.Key, &item.DisplayName, &item.Description, &item.Enabled, &item.URL); err != nil {
			return nil, mapDBError(err)
		}
		items = append(items, item)
	}
	return items, mapDBError(rows.Err())
}

func (r *RBACV1Repository) SaveApplication(ctx context.Context, value biz.Application) (biz.Application, error) {
	value.Key = strings.TrimSpace(value.Key)
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	value.Description = strings.TrimSpace(value.Description)
	value.URL = strings.TrimSpace(value.URL)
	if value.Key == "" || value.DisplayName == "" {
		return value, fmt.Errorf("%w: key and display_name are required", biz.ErrInvalidArgument)
	}
	if value.ID == uuid.Nil {
		err := r.pool.QueryRow(ctx, `INSERT INTO applications(app_key,display_name,description,enabled,url) VALUES($1,$2,$3,$4,$5) RETURNING `+applicationColumns,
			value.Key, value.DisplayName, value.Description, value.Enabled, value.URL).Scan(&value.ID, &value.Key, &value.DisplayName, &value.Description, &value.Enabled, &value.URL)
		return value, mapDBError(err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return value, mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, value.ID).Scan(&value.ID); err != nil {
		return value, mapDBError(err)
	}
	err = tx.QueryRow(ctx, `UPDATE applications SET display_name=$2,description=$3,enabled=$4,url=$5,updated_at=NOW() WHERE id=$1 RETURNING `+applicationColumns,
		value.ID, value.DisplayName, value.Description, value.Enabled, value.URL).Scan(&value.ID, &value.Key, &value.DisplayName, &value.Description, &value.Enabled, &value.URL)
	if err != nil {
		return value, mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return value, mapDBError(err)
	}
	return value, nil
}

// Only the service key is persisted. Display metadata always comes from the configured catalog.
func (r *RBACV1Repository) ListApplicationServices(ctx context.Context, key string) ([]*biz.ServiceResource, error) {
	applicationID, err := r.applicationIDForKey(ctx, key)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT service_resource_key FROM application_service_resources WHERE application_id=$1 ORDER BY service_resource_key`, applicationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	keys := []string{}
	for rows.Next() {
		var serviceKey string
		if err := rows.Scan(&serviceKey); err != nil {
			rows.Close()
			return nil, mapDBError(err)
		}
		keys = append(keys, serviceKey)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, mapDBError(err)
	}
	resources, err := r.catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*biz.ServiceResource, len(resources))
	for _, resource := range resources {
		if resource != nil {
			byKey[resource.Key] = resource
		}
	}
	items := make([]*biz.ServiceResource, 0, len(keys))
	for _, serviceKey := range keys {
		if resource := byKey[serviceKey]; resource != nil {
			items = append(items, resource)
		} else {
			// Keep a missing association visible so the user can remove it. No cached metadata is used.
			items = append(items, &biz.ServiceResource{Key: serviceKey, Name: serviceKey, DisplayName: serviceKey, IsActive: false})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DisplayName != items[j].DisplayName {
			return items[i].DisplayName < items[j].DisplayName
		}
		return items[i].Key < items[j].Key
	})
	return items, nil
}

func (r *RBACV1Repository) ReplaceApplicationServices(ctx context.Context, key string, keys []string) error {
	applicationID, err := r.applicationIDForKey(ctx, key)
	if err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapDBError(err)
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, applicationID).Scan(&applicationID); err != nil {
		return mapDBError(err)
	}

	activeKeys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return err
	}
	active := make(map[string]bool, len(activeKeys))
	for _, serviceKey := range activeKeys {
		active[serviceKey] = true
	}
	// Existing unavailable links may be retained while other links are edited, but cannot be newly added.
	for _, serviceKey := range keys {
		if active[serviceKey] {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM application_service_resources WHERE application_id=$1 AND service_resource_key=$2)`, applicationID, serviceKey).Scan(&exists); err != nil {
			return mapDBError(err)
		}
		if !exists {
			return fmt.Errorf("%w: service resource must exist and be enabled", biz.ErrConflict)
		}
	}

	var hasRemovedGrant bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM role_api_endpoint_bindings b
		JOIN roles r ON r.id=b.role_id AND r.application_id=$1
		JOIN api_endpoints e ON e.id=b.endpoint_id
		JOIN application_service_resources current_link ON current_link.application_id=$1 AND current_link.service_resource_key=e.service_resource_key
		WHERE NOT (e.service_resource_key=ANY($2::text[]))
	)`, applicationID, keys).Scan(&hasRemovedGrant)
	if err != nil {
		return mapDBError(err)
	}
	if hasRemovedGrant {
		return fmt.Errorf("%w: cannot remove a service resource with existing API grants", biz.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM application_service_resources WHERE application_id=$1 AND NOT (service_resource_key=ANY($2::text[]))`, applicationID, keys); err != nil {
		return mapDBError(err)
	}
	for _, serviceKey := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO application_service_resources(application_id,service_resource_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, applicationID, serviceKey); err != nil {
			return mapDBError(err)
		}
	}
	return mapDBError(tx.Commit(ctx))
}
