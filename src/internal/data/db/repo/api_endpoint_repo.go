package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/data/db/model"
)

const apiEndpointColumns = `id, service_resource, controller, method, path_template, summary,
	enabled, created_by_id, created_by_name, created_at, updated_by_id, updated_by_name,
	updated_at, is_deleted`

// APIEndpointRepository 保存服务资源作用域内的 API 端点。
type APIEndpointRepository struct {
	pool *pgxpool.Pool
}

var _ biz.APIEndpointRepository = (*APIEndpointRepository)(nil)

// NewAPIEndpointRepository 创建 API 端点 PostgreSQL 仓储。
func NewAPIEndpointRepository(pool *pgxpool.Pool) *APIEndpointRepository {
	return &APIEndpointRepository{pool: pool}
}

// Create 持久化一条 API 端点记录并返回数据库中的完整记录。
func (r *APIEndpointRepository) Create(ctx context.Context, value *biz.APIEndpoint) (*biz.APIEndpoint, error) {
	if value == nil || strings.TrimSpace(value.ServiceResource) == "" {
		return nil, fmt.Errorf("%w: api endpoint and service_resource are required", biz.ErrInvalidArgument)
	}
	id := value.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	actor := biz.AuditActorFromContext(ctx)
	const query = `
		INSERT INTO authorization_api_endpoints (
			id, service_resource, controller, method, path_template, summary, enabled,
			created_by_id, created_by_name, updated_by_id, updated_by_name, is_deleted
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $8, $9, FALSE)
		RETURNING ` + apiEndpointColumns
	stored, err := scanAPIEndpoint(r.pool.QueryRow(ctx, query,
		id, strings.TrimSpace(value.ServiceResource), value.Controller, value.Method,
		value.PathTemplate, value.Summary, value.Enabled, actor.ID, actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizAPIEndpoint(stored), nil
}

// Get 按 UUID 查询一条未删除的 API 端点记录。
func (r *APIEndpointRepository) Get(ctx context.Context, id uuid.UUID) (*biz.APIEndpoint, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("%w: api endpoint id is required", biz.ErrInvalidArgument)
	}
	value, err := scanAPIEndpoint(r.pool.QueryRow(ctx,
		`SELECT `+apiEndpointColumns+` FROM authorization_api_endpoints WHERE id = $1 AND is_deleted = FALSE`, id))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizAPIEndpoint(value), nil
}

// ListByServiceResource 查询服务资源下全部未删除的 API 端点。
func (r *APIEndpointRepository) ListByServiceResource(ctx context.Context, serviceResource string) ([]*biz.APIEndpoint, error) {
	serviceResource = strings.TrimSpace(serviceResource)
	if serviceResource == "" {
		return nil, fmt.Errorf("%w: service_resource is required", biz.ErrInvalidArgument)
	}
	rows, err := r.pool.Query(ctx, `SELECT `+apiEndpointColumns+` FROM authorization_api_endpoints WHERE service_resource = $1 AND is_deleted = FALSE ORDER BY controller, method, path_template, id`, serviceResource)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	values := make([]*biz.APIEndpoint, 0)
	for rows.Next() {
		value, scanErr := scanAPIEndpoint(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		values = append(values, toBizAPIEndpoint(value))
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return values, nil
}

// Update 更新 API 端点的可变字段，并保留服务资源和创建审计字段。
func (r *APIEndpointRepository) Update(ctx context.Context, value *biz.APIEndpoint) (*biz.APIEndpoint, error) {
	if value == nil || value.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: api endpoint id is required", biz.ErrInvalidArgument)
	}
	actor := biz.AuditActorFromContext(ctx)
	const query = `
		UPDATE authorization_api_endpoints
		SET controller = $2, method = $3, path_template = $4, summary = $5, enabled = $6,
			updated_by_id = $7, updated_by_name = $8, updated_at = NOW()
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING ` + apiEndpointColumns
	stored, err := scanAPIEndpoint(r.pool.QueryRow(ctx, query,
		value.ID, value.Controller, value.Method, value.PathTemplate, value.Summary,
		value.Enabled, actor.ID, actor.Name,
	))
	if err != nil {
		return nil, mapDBError(err)
	}
	return toBizAPIEndpoint(stored), nil
}

// SoftDelete 软删除 API 端点并同步禁用状态。
func (r *APIEndpointRepository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: api endpoint id is required", biz.ErrInvalidArgument)
	}
	actor := biz.AuditActorFromContext(ctx)
	result, err := r.pool.Exec(ctx, `UPDATE authorization_api_endpoints SET is_deleted = TRUE, enabled = FALSE, updated_by_id = $2, updated_by_name = $3, updated_at = NOW() WHERE id = $1 AND is_deleted = FALSE`, id, actor.ID, actor.Name)
	if err != nil {
		return mapDBError(err)
	}
	if result.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	return nil
}

func scanAPIEndpoint(row rowScanner) (*model.APIEndpoint, error) {
	value := &model.APIEndpoint{}
	err := row.Scan(
		&value.ID,
		&value.ServiceResource,
		&value.Controller,
		&value.Method,
		&value.PathTemplate,
		&value.Summary,
		&value.Enabled,
		&value.CreatedByID,
		&value.CreatedByName,
		&value.CreatedAt,
		&value.UpdatedByID,
		&value.UpdatedByName,
		&value.UpdatedAt,
		&value.IsDeleted,
	)
	return value, err
}

func toBizAPIEndpoint(value *model.APIEndpoint) *biz.APIEndpoint {
	if value == nil {
		return nil
	}
	return &biz.APIEndpoint{
		BaseFields: biz.BaseFields{
			CreatedByID:   value.CreatedByID,
			CreatedByName: value.CreatedByName,
			CreatedAt:     value.CreatedAt,
			UpdatedByID:   value.UpdatedByID,
			UpdatedByName: value.UpdatedByName,
			UpdatedAt:     value.UpdatedAt,
			IsDeleted:     value.IsDeleted,
		},
		ID:              value.ID,
		ServiceResource: value.ServiceResource,
		Controller:      value.Controller,
		Method:          value.Method,
		PathTemplate:    value.PathTemplate,
		Summary:         value.Summary,
		Enabled:         value.Enabled,
	}
}
