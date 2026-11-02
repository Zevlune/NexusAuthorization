package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg)
	if err != nil {
		logger.Error("Create application failed.", slog.Any("Error", err))
		return 1
	}
	defer application.Close()
	go application.RunSubjectAPIReconciler(ctx, logger)
	middleware := []func(http.Handler) http.Handler{application.Auth.Middleware, httpserver.ManagementAuditMiddleware}
	if cfg.OIDC.Enabled {
		middleware = append(middleware, httpserver.ManagementAuthorizationMiddleware(
			application.RBACV1, "management", cfg.Runtime.ServiceUK,
		))
	}
	server := &http.Server{
		Addr: cfg.HTTP.Addr,
		// Bound slow clients. WriteTimeout leaves room for Swagger imports and
		// Redis publication after saves.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       2 * time.Minute,
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
			middleware...,
		)),
	}
	logger.Info("Permission center HTTP server listening.", slog.String("Address", cfg.HTTP.Addr))
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped unexpectedly.", slog.Any("Error", err))
			return 1
		}
		return 0
	case <-ctx.Done():
	}
	// Let in-flight saves finish, including their Redis publication.
	logger.Info("Shutting down HTTP server.")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown failed.", slog.Any("Error", err))
		return 1
	}
	return 0
}
