package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/data/db"
	"github.com/luck/nexus-authorization/internal/data/db/repo"
	"github.com/luck/nexus-authorization/internal/data/rediscache"
)

// subjectAPIReconciler repairs Redis publication without waiting for a save.
type subjectAPIReconciler interface {
	ReconcileSubjectAPIs(context.Context, bool) (int, error)
}

type Application struct {
	Pool             *pgxpool.Pool
	ServiceResources *biz.ServiceResourceService
	RBACV1           *biz.RBACV1Service
	Auth             *auth.Service
	Users            *biz.NexusAuthSCIMClient
	PolicyCache      *rediscache.PolicyCache

	reconciler        subjectAPIReconciler
	reconcileInterval time.Duration
}

// New 创建权限中心应用，并完成数据库仓储、业务服务和认证服务的装配。
func New(ctx context.Context, cfg *config.Config) (*Application, error) {
	if cfg.Redis.Enabled && strings.TrimSpace(cfg.Redis.URL) == "" {
		return nil, fmt.Errorf("redis.url is required when redis.enabled is true")
	}
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
	rbacRepository := repo.NewRBACV1Repository(pool, serviceResources)
	rbacService := biz.NewRBACV1Service(rbacRepository).WithSwaggerFetchPolicy(biz.SwaggerFetchPolicy{
		AllowLoopback: cfg.SwaggerImport.AllowLoopback,
		AllowedHosts:  cfg.SwaggerImport.AllowedHosts,
	})
	application := &Application{
		Pool:             pool,
		ServiceResources: serviceResources,
		RBACV1:           rbacService,
		Auth:             authenticator,
		Users:            users,
	}
	if cfg.Redis.Enabled {
		interval, err := cfg.Redis.ReconcileIntervalDuration()
		if err != nil {
			pool.Close()
			return nil, err
		}
		policyCache, err := rediscache.New(cfg.Redis)
		if err != nil {
			pool.Close()
			return nil, err
		}
		rbacRepository.WithPolicyCache(policyCache)
		serviceResourceRepository.WithSubjectAPISync(rbacRepository.RefreshSubjectAPIForService)
		application.PolicyCache = policyCache
		application.reconciler = rbacRepository
		application.reconcileInterval = interval
	} else {
		rbacService.WithDatabaseAuthorization()
	}
	return application, nil
}

// RunSubjectAPIReconciler keeps Redis service API records consistent with the
// database until ctx ends. It rebuilds the complete service/user snapshot at
// startup and on every interval, including catalog changes while stopped.
// It returns immediately when Redis authorization is disabled.
func (a *Application) RunSubjectAPIReconciler(ctx context.Context, logger *slog.Logger) {
	if a == nil || a.reconciler == nil {
		return
	}
	pass := func() {
		passCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		republished, err := a.reconciler.ReconcileSubjectAPIs(passCtx, true)
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("Reconciling Redis authorization records failed; will retry.",
					slog.Int("Republished", republished), slog.Any("Error", err))
			}
			return
		}
		logger.Info("Reconciled Redis authorization records.", slog.Int("Republished", republished))
	}
	pass()
	ticker := time.NewTicker(a.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pass()
		}
	}
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
