package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/luck/nexus-authorization/internal/app"
	"github.com/luck/nexus-authorization/internal/config"
	"github.com/luck/nexus-authorization/internal/logging"
	httpserver "github.com/luck/nexus-authorization/internal/server/http"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load("configs/app.yaml")
	if err != nil {
		logging.NewText(os.Stderr, "nexus-authorization", "info").Error("Load configuration failed.", slog.Any("Error", err))
		return 1
	}
	logger, closer, err := logging.New(logging.Options{
		Module:       cfg.Logging.Module,
		MinimumLevel: cfg.Logging.MinimumLevel,
		FilePath:     cfg.Logging.FilePath,
	})
	if err != nil {
		logging.NewText(os.Stderr, cfg.Logging.Module, "info").Error("Configure logging failed.", slog.Any("Error", err))
		return 1
	}
	defer closer.Close()
	slog.SetDefault(logger)
	application, err := app.New(context.Background(), cfg)
	if err != nil {
		logger.Error("Create application failed.", slog.Any("Error", err))
		return 1
	}
	defer application.Close()
	server := &http.Server{
		Addr: cfg.HTTP.Addr,
		Handler: logging.HTTPMiddleware(logger, func(request *http.Request) string {
			user, ok := application.Auth.Me(request)
			if !ok {
				return ""
			}
			return user.Subject
		})(httpserver.NewServer(
			httpserver.NewHandler(application.Auth).
				WithServiceResourceCatalog(application.ServiceResources).
				WithServiceResourceService(application.ServiceResources).
				WithRBACV1Service(application.RBACV1).
				WithUserDirectory(application.Users).
				WithAuthorizationServiceCredential(cfg.Authorization.ServiceCredential),
			application.Auth.Middleware,
		)),
	}
	logger.Info("Permission center HTTP server listening.", slog.String("Address", cfg.HTTP.Addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped unexpectedly.", slog.Any("Error", err))
		return 1
	}
	return 0
}
