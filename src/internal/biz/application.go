package biz

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Application is the permission boundary for menus, roles, subject bindings,
// and published authorization snapshots.
type Application struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Enabled     bool      `json:"enabled"`
}

// ApplicationRepository is an optional extension of the RBAC v1 repository.
// It stays separate from RBACV1Repository so small legacy test repositories
// do not need to implement application management.
type ApplicationRepository interface {
	ListApplications(context.Context) ([]Application, error)
	SaveApplication(context.Context, Application) (Application, error)
	ListApplicationServices(context.Context, string) ([]*ServiceResource, error)
	ReplaceApplicationServices(context.Context, string, []string) error
}

func (s *RBACV1Service) applicationRepository() (ApplicationRepository, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("rbac v1 repository is not configured")
	}
	repository, ok := s.repository.(ApplicationRepository)
	if !ok {
		return nil, fmt.Errorf("application repository is not configured")
	}
	return repository, nil
}

func (s *RBACV1Service) ListApplications(ctx context.Context) ([]Application, error) {
	repository, err := s.applicationRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListApplications(ctx)
}

func (s *RBACV1Service) SaveApplication(ctx context.Context, value Application) (Application, error) {
	value.Key = strings.TrimSpace(value.Key)
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	value.Description = strings.TrimSpace(value.Description)
	value.URL = strings.TrimSpace(value.URL)
	if value.Key == "" || value.DisplayName == "" {
		return Application{}, fmt.Errorf("%w: key and display_name are required", ErrInvalidArgument)
	}
	if len([]rune(value.Key)) > 128 {
		return Application{}, fmt.Errorf("%w: key must be at most 128 characters", ErrInvalidArgument)
	}
	if len([]rune(value.DisplayName)) > 200 {
		return Application{}, fmt.Errorf("%w: display_name must be at most 200 characters", ErrInvalidArgument)
	}
	if err := validateApplicationURL(value.URL); err != nil {
		return Application{}, err
	}
	repository, err := s.applicationRepository()
	if err != nil {
		return Application{}, err
	}
	return repository.SaveApplication(ctx, value)
}

func validateApplicationURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if len([]rune(value)) > 2048 || err != nil || parsed == nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || strings.ContainsAny(value, "\\ \t\r\n") {
		return fmt.Errorf("%w: url must be an absolute HTTP(S) address without credentials, at most 2048 characters", ErrInvalidArgument)
	}
	return nil
}

func (s *RBACV1Service) ListApplicationServices(ctx context.Context, key string) ([]*ServiceResource, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: app_key is required", ErrInvalidArgument)
	}
	repository, err := s.applicationRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListApplicationServices(ctx, key)
}

func (s *RBACV1Service) ReplaceApplicationServices(ctx context.Context, key string, keys []string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: app_key is required", ErrInvalidArgument)
	}
	normalized := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, item := range keys {
		item = strings.TrimSpace(item)
		if item == "" {
			return fmt.Errorf("%w: service resource keys must not be empty", ErrInvalidArgument)
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	repository, err := s.applicationRepository()
	if err != nil {
		return err
	}
	return repository.ReplaceApplicationServices(ctx, key, normalized)
}
