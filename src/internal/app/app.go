package app

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/permission-center-go/internal/auth"
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/config"
	"github.com/luck/permission-center-go/internal/data/db"
	"github.com/luck/permission-center-go/internal/data/db/repo"
)

type Application struct {
	Pool             *pgxpool.Pool
	Permissions      *biz.PermissionService
	ServiceResources *biz.ServiceResourceService
	APIEndpoints     *biz.APIEndpointService
	RBACV1           *biz.RBACV1Service
	Auth             *auth.Service
}

// New 创建权限中心应用，并完成数据库仓储、业务服务和认证服务的装配。
func New(ctx context.Context, cfg *config.Config) (*Application, error) {
	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	roles := repo.NewRoleRepository(pool)
	menus := repo.NewMenuRepository(pool)
	userRoles := repo.NewUserRoleRepository(pool)
	serviceResourceRepository := repo.NewServiceResourceRepository(pool)
	serviceResources, err := newServiceResourceCatalog(serviceResourceRepository, cfg.ServiceResource)
	if err != nil {
		pool.Close()
		return nil, err
	}
	authenticator, err := auth.New(ctx, cfg.OIDC)
	if err != nil {
		pool.Close()
		return nil, err
	}
	permissions := biz.NewPermissionService(roles, menus, userRoles).WithServiceResourceCatalog(serviceResources)
	apiEndpoints := biz.NewAPIEndpointService(repo.NewAPIEndpointRepository(pool)).WithServiceResourceCatalog(serviceResources)
	return &Application{
		Pool:             pool,
		Permissions:      permissions,
		ServiceResources: serviceResources,
		APIEndpoints:     apiEndpoints,
		RBACV1:           biz.NewRBACV1Service(repo.NewRBACV1Repository(pool, serviceResources)),
		Auth:             authenticator,
	}, nil
}

// newServiceResourceCatalog 根据后端配置创建本地或 NexusAuth 服务资源来源。
func newServiceResourceCatalog(repository biz.ServiceResourceRepository, cfg config.ServiceResourceConfig) (*biz.ServiceResourceService, error) {
	var remote biz.ServiceResourceCatalog
	if cfg.Source == biz.ServiceResourceSourceNexusAuth {
		timeout, err := cfg.TimeoutDuration()
		if err != nil {
			return nil, err
		}
		remote, err = biz.NewNexusAuthServiceResourceCatalog(cfg.BaseURL(), cfg.Credential(), timeout)
		if err != nil {
			return nil, err
		}
	}
	return biz.NewServiceResourceCatalog(repository, remote, cfg.Source)
}

// Close 释放应用持有的数据库连接池。
func (a *Application) Close() {
	if a != nil && a.Pool != nil {
		a.Pool.Close()
	}
}
