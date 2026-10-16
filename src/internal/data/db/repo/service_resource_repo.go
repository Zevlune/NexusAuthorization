package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/biz"
)

const serviceResourceColumns = `id, resource_key, source, external_id, display_name, audience, description, enabled, created_at, updated_at`

type ServiceResourceRepository struct {
	pool           *pgxpool.Pool
	subjectAPISync func(context.Context, string) error
}

var _ biz.ServiceResourceRepository = (*ServiceResourceRepository)(nil)
var _ biz.ServiceResourceSourceRepository = (*ServiceResourceRepository)(nil)

func NewServiceResourceRepository(pool *pgxpool.Pool) *ServiceResourceRepository {
	return &ServiceResourceRepository{pool: pool}
}

// WithSubjectAPISync wires direct Redis publication for service-status
// changes. The callback is optional for isolated repository tests.
func (r *ServiceResourceRepository) WithSubjectAPISync(sync func(context.Context, string) error) *ServiceResourceRepository {
	r.subjectAPISync = sync
	return r
}

func (r *ServiceResourceRepository) Create(ctx context.Context, input *biz.ServiceResource) (*biz.ServiceResource, error) {
	if input == nil || strings.TrimSpace(input.Key) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return nil, fmt.Errorf("%w: key and display_name are required", biz.ErrInvalidArgument)
	}
	value, err := r.scan(r.pool.QueryRow(ctx, `INSERT INTO service_resources(resource_key,display_name,audience,description,source,enabled) VALUES($1,$2,$3,$4,'local',$5) RETURNING `+serviceResourceColumns,
		strings.TrimSpace(input.Key), strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Audience), strings.TrimSpace(input.Description), input.IsActive))
	if err != nil {
		return nil, err
	}
	if r.subjectAPISync != nil {
		if err := r.subjectAPISync(ctx, value.Key); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func (r *ServiceResourceRepository) Get(ctx context.Context, key string) (*biz.ServiceResource, error) {
	return r.get(ctx, key, "")
}
func (r *ServiceResourceRepository) GetBySource(ctx context.Context, key, source string) (*biz.ServiceResource, error) {
	return r.get(ctx, key, source)
}
func (r *ServiceResourceRepository) get(ctx context.Context, key, source string) (*biz.ServiceResource, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: service resource key is required", biz.ErrInvalidArgument)
	}
	if source == "" {
		return r.scan(r.pool.QueryRow(ctx, `SELECT `+serviceResourceColumns+` FROM service_resources WHERE resource_key=$1`, key))
	}
	return r.scan(r.pool.QueryRow(ctx, `SELECT `+serviceResourceColumns+` FROM service_resources WHERE resource_key=$1 AND source=$2`, key, source))
}

func (r *ServiceResourceRepository) List(ctx context.Context) ([]*biz.ServiceResource, error) {
	return r.list(ctx, "")
}
func (r *ServiceResourceRepository) ListBySource(ctx context.Context, source string) ([]*biz.ServiceResource, error) {
	return r.list(ctx, source)
}
func (r *ServiceResourceRepository) list(ctx context.Context, source string) ([]*biz.ServiceResource, error) {
	query := `SELECT ` + serviceResourceColumns + ` FROM service_resources`
	args := []any{}
	if source != "" {
		query += ` WHERE source=$1`
		args = append(args, source)
	}
	query += ` ORDER BY display_name,resource_key`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	items := make([]*biz.ServiceResource, 0)
	for rows.Next() {
		value, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, mapDBError(rows.Err())
}

func (r *ServiceResourceRepository) Update(ctx context.Context, input *biz.ServiceResource) (*biz.ServiceResource, error) {
	if input == nil || strings.TrimSpace(input.Key) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return nil, fmt.Errorf("%w: key and display_name are required", biz.ErrInvalidArgument)
	}
	value, err := r.scan(r.pool.QueryRow(ctx, `UPDATE service_resources SET display_name=$2,audience=$3,description=$4,enabled=$5,updated_at=NOW() WHERE resource_key=$1 AND source='local' RETURNING `+serviceResourceColumns,
		strings.TrimSpace(input.Key), strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Audience), strings.TrimSpace(input.Description), input.IsActive))
	if err != nil {
		return nil, err
	}
	if r.subjectAPISync != nil {
		if err := r.subjectAPISync(ctx, value.Key); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func (r *ServiceResourceRepository) SoftDelete(ctx context.Context, key string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM service_resources WHERE resource_key=$1 AND source='local'`, strings.TrimSpace(key))
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	if r.subjectAPISync != nil {
		if err := r.subjectAPISync(ctx, strings.TrimSpace(key)); err != nil {
			return err
		}
	}
	return nil
}

func (r *ServiceResourceRepository) scan(row rowScanner) (*biz.ServiceResource, error) {
	var id uuid.UUID
	var externalID *string
	var updatedAt time.Time
	value := &biz.ServiceResource{}
	if err := row.Scan(&id, &value.Key, &value.Source, &externalID, &value.DisplayName, &value.Audience, &value.Description, &value.IsActive, &value.CreatedAt, &updatedAt); err != nil {
		return nil, mapDBError(err)
	}
	value.ID = id.String()
	if externalID != nil && *externalID != "" {
		value.ID = *externalID
	}
	value.Name = value.DisplayName
	return value, nil
}
