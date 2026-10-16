package app

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/db"
	"github.com/luck/nexus-authorization/internal/data/db/repo"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

type Application struct {
	Pool             *pgxpool.Pool
	ServiceResources *biz.ServiceResourceService
	APIEndpoints     *biz.APIEndpointService
	RBACV1           *biz.RBACV1Service
	Auth             *auth.Service
	Users            *biz.NexusAuthSCIMClient
	PolicyCache      *rediscache.PolicyCache
}

// New 创建权限中心应用，并完成数据库仓储、业务服务和认证服务的装配。
func New(ctx context.Context, cfg *config.Config) (*Application, error) {
	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
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
	var users *biz.NexusAuthSCIMClient
	if strings.TrimSpace(cfg.SCIM.NexusAuthBaseURL) != "" {
		timeout, err := cfg.SCIM.TimeoutDuration()
		if err != nil {
			pool.Close()
			return nil, err
		}
		users, err = biz.NewNexusAuthSCIMClient(cfg.SCIM.NexusAuthBaseURL, cfg.SCIM.APIKey, timeout)
		if err != nil {
			pool.Close()
			return nil, err
		}
	}
	apiEndpoints := biz.NewAPIEndpointService(repo.NewAPIEndpointRepository(pool)).WithServiceResourceCatalog(serviceResources)
	rbacRepository := repo.NewRBACV1Repository(pool, serviceResources)
	var policyCache *rediscache.PolicyCache
	if strings.TrimSpace(cfg.Redis.URL) != "" {
		policyCache, err = rediscache.New(cfg.Redis)
		if err != nil {
			pool.Close()
			return nil, err
		}
		rbacRepository.WithPolicyCache(policyCache)
	}
	return &Application{
		Pool:             pool,
		ServiceResources: serviceResources,
		APIEndpoints:     apiEndpoints,
		RBACV1:           biz.NewRBACV1Service(rbacRepository),
		PolicyCache:      policyCache,
		Auth:             authenticator,
		Users:            users,
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
	if a != nil && a.PolicyCache != nil {
		_ = a.PolicyCache.Close()
	}
	if a != nil && a.Pool != nil {
		a.Pool.Close()
	}
}
