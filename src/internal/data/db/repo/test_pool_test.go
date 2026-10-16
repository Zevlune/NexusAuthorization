package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const repositoryTestSchema = "nexusauthorization"

func newRepositoryTestPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = repositoryTestSchema
	return pgxpool.NewWithConfig(ctx, config)
}
