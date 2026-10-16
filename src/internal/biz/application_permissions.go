package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"strings"
)

type ApplicationMenu struct {
	ID            uuid.UUID  `json:"id"`
	ApplicationID uuid.UUID  `json:"application_id"`
	ParentID      *uuid.UUID `json:"parent_id"`
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	MenuType      string     `json:"menu_type"`
	Route         string     `json:"route"`
	Component     string     `json:"component"`
	Icon          string     `json:"icon"`
	SortOrder     int        `json:"sort_order"`
	Enabled       bool       `json:"enabled"`
}
type applicationPermissionsRepository interface {
	ListApplicationEndpoints(context.Context, string, string, int, int) ([]RBACEndpoint, int, error)
	ListApplicationMenus(context.Context, string) ([]ApplicationMenu, error)
	SaveApplicationMenu(context.Context, string, ApplicationMenu) (ApplicationMenu, error)
	DeleteApplicationMenu(context.Context, string, uuid.UUID) error
}

func (s *RBACV1Service) RequireApplicationService(ctx context.Context, app, service string) error {
	if strings.TrimSpace(app) == "" || strings.TrimSpace(service) == "" {
		return fmt.Errorf("%w: app_key and service_resource required", ErrInvalidArgument)
	}
	repo, ok := s.repository.(ApplicationRepository)
	if !ok {
		return ErrNotFound
	}
	items, err := repo.ListApplicationServices(ctx, app)
	if err != nil {
		return err
	}
	for _, v := range items {
		if v.Key == service && v.IsActive {
			return nil
		}
	}
	return fmt.Errorf("%w: service is not linked to application", ErrInvalidArgument)
}
func (s *RBACV1Service) ApplicationEndpoints(ctx context.Context, app, service string, page, size int) (RBACPage[RBACEndpoint], error) {
	if strings.TrimSpace(app) == "" {
		return RBACPage[RBACEndpoint]{}, ErrInvalidArgument
	}
	if service != "" {
		if err := s.RequireApplicationService(ctx, app, service); err != nil {
			return RBACPage[RBACEndpoint]{}, err
		}
	}
	page, size = normalizeRBACPage(page, size)
	repo, ok := s.repository.(applicationPermissionsRepository)
	if !ok {
		return RBACPage[RBACEndpoint]{}, ErrNotFound
	}
	items, total, err := repo.ListApplicationEndpoints(ctx, app, service, page, size)
	return RBACPage[RBACEndpoint]{Items: items, Total: total, Page: page, PageSize: size}, err
}
func (s *RBACV1Service) ListApplicationMenus(ctx context.Context, app string) ([]ApplicationMenu, error) {
	repo, ok := s.repository.(applicationPermissionsRepository)
	if !ok {
		return nil, ErrNotFound
	}
	return repo.ListApplicationMenus(ctx, app)
}
func (s *RBACV1Service) SaveApplicationMenu(ctx context.Context, app string, v ApplicationMenu) (ApplicationMenu, error) {
	v.Code = strings.TrimSpace(v.Code)
	v.Name = strings.TrimSpace(v.Name)
	if v.Code == "" || v.Name == "" || (v.MenuType != "directory" && v.MenuType != "menu" && v.MenuType != "button") {
		return v, fmt.Errorf("%w: code, name and valid menu_type required", ErrInvalidArgument)
	}
	repo, ok := s.repository.(applicationPermissionsRepository)
	if !ok {
		return v, ErrNotFound
	}
	return repo.SaveApplicationMenu(ctx, app, v)
}
func (s *RBACV1Service) DeleteApplicationMenu(ctx context.Context, app string, id uuid.UUID) error {
	repo, ok := s.repository.(applicationPermissionsRepository)
	if !ok {
		return ErrNotFound
	}
	return repo.DeleteApplicationMenu(ctx, app, id)
}

// SubjectMenus reads currently saved role and member permissions.
func (s *RBACV1Service) SubjectMenus(ctx context.Context, app, subject, domain string) ([]ApplicationMenu, error) {
	if strings.TrimSpace(app) == "" || strings.TrimSpace(subject) == "" {
		return nil, ErrInvalidArgument
	}
	policy, err := s.currentPolicy(ctx, app)
	if err != nil {
		return nil, err
	}
	return subjectMenusFromPolicy(policy.Data, subject, domain), nil
}
func subjectMenusFromPolicy(snap map[string]any, subject, domain string) []ApplicationMenu {
	bindings, _ := snap["subject_roles"].([]any)
	roles, _ := snap["roles"].([]any)
	rawMenus, _ := snap["menus"].([]any)
	roleIDs := map[string]bool{}
	grants := map[string]bool{}
	normalizedDomain := NormalizeRBACDomain(domain)
	for _, raw := range bindings {
		b, _ := raw.(map[string]any)
		if b["subject"] == subject && NormalizeRBACDomain(policyString(b["domain"])) == normalizedDomain {
			roleIDs[fmt.Sprint(b["role_id"])] = true
		}
	}
	for _, raw := range roles {
		r, _ := raw.(map[string]any)
		enabled, _ := r["enabled"].(bool)
		if !enabled || !roleIDs[fmt.Sprint(r["id"])] {
			continue
		}
		ids, _ := r["menu_ids"].([]any)
		for _, id := range ids {
			grants[fmt.Sprint(id)] = true
		}
	}
	menus := []ApplicationMenu{}
	byID := map[uuid.UUID]ApplicationMenu{}
	for _, raw := range rawMenus {
		data, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var m ApplicationMenu
		if json.Unmarshal(data, &m) == nil {
			menus = append(menus, m)
			byID[m.ID] = m
		}
	}
	visible := map[uuid.UUID]bool{}
	for _, m := range menus {
		if !grants[m.ID.String()] || !m.Enabled {
			continue
		}
		chain := []uuid.UUID{}
		seen := map[uuid.UUID]bool{}
		cur := m
		valid := true
		for {
			if !cur.Enabled || seen[cur.ID] {
				valid = false
				break
			}
			seen[cur.ID] = true
			chain = append(chain, cur.ID)
			if cur.ParentID == nil {
				break
			}
			var ok bool
			cur, ok = byID[*cur.ParentID]
			if !ok {
				valid = false
				break
			}
		}
		if valid {
			for _, id := range chain {
				visible[id] = true
			}
		}
	}
	out := []ApplicationMenu{}
	for _, m := range menus {
		if visible[m.ID] {
			out = append(out, m)
		}
	}
	return out
}
