package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type memoryPermissionGroupRepository struct {
	groups map[uuid.UUID]APIPermissionGroup
	saved  []APIPermissionGroup
	err    error
}

func newMemoryPermissionGroupRepository() *memoryPermissionGroupRepository {
	return &memoryPermissionGroupRepository{groups: map[uuid.UUID]APIPermissionGroup{}}
}

func (r *memoryPermissionGroupRepository) List(_ context.Context, service string) ([]APIPermissionGroup, error) {
	if r.err != nil {
		return nil, r.err
	}
	items := make([]APIPermissionGroup, 0)
	for _, group := range r.groups {
		if group.ServiceResource == service {
			items = append(items, group)
		}
	}
	return items, nil
}

func (r *memoryPermissionGroupRepository) Save(_ context.Context, group APIPermissionGroup) (APIPermissionGroup, error) {
	if r.err != nil {
		return APIPermissionGroup{}, r.err
	}
	if group.ID == uuid.Nil {
		group.ID = uuid.New()
	}
	r.groups[group.ID] = group
	r.saved = append(r.saved, group)
	return group, nil
}

func (r *memoryPermissionGroupRepository) Delete(_ context.Context, service string, id uuid.UUID) error {
	if r.err != nil {
		return r.err
	}
	group, ok := r.groups[id]
	if !ok || group.ServiceResource != service {
		return ErrNotFound
	}
	delete(r.groups, id)
	return nil
}

func permissionGroupTestInput() APIPermissionGroup {
	return APIPermissionGroup{
		ServiceResource: "nexusauth",
		Code:            "scim-users-read",
		Name:            "SCIM users read",
		Action:          "read",
		Enabled:         true,
		EndpointIDs:     []uuid.UUID{uuid.New()},
	}
}

func TestAPIPermissionGroupServiceSaveNormalizes(t *testing.T) {
	r := newMemoryPermissionGroupRepository()
	s := NewAPIPermissionGroupService(r)
	in := permissionGroupTestInput()
	in.ServiceResource, in.Code, in.Name, in.Action = "  nexusauth  ", "  scim-users-read ", "  SCIM users read  ", " read "
	saved, err := s.Save(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ServiceResource != "nexusauth" || saved.Code != "scim-users-read" || saved.Name != "SCIM users read" || saved.Action != "read" {
		t.Fatalf("fields were not trimmed: %+v", saved)
	}
	if saved.ID == uuid.Nil || len(saved.EndpointIDs) != 1 {
		t.Fatalf("save did not assign identity or endpoints: %+v", saved)
	}
}

func TestAPIPermissionGroupServiceSaveRejectsInvalidInput(t *testing.T) {
	tooMany := make([]uuid.UUID, 0, APIPermissionGroupMaxEndpoints+1)
	for i := 0; i <= APIPermissionGroupMaxEndpoints; i++ {
		tooMany = append(tooMany, uuid.New())
	}
	base := permissionGroupTestInput()
	r := newMemoryPermissionGroupRepository()
	s := NewAPIPermissionGroupService(r)
	for _, tc := range []struct {
		name   string
		modify func(*APIPermissionGroup)
	}{
		{"service", func(v *APIPermissionGroup) { v.ServiceResource = "   " }},
		{"service too long", func(v *APIPermissionGroup) { v.ServiceResource = string(make([]byte, 129)) }},
		{"code", func(v *APIPermissionGroup) { v.Code = "" }},
		{"name", func(v *APIPermissionGroup) { v.Name = "  " }},
		{"name too long", func(v *APIPermissionGroup) { v.Name = string(make([]rune, 201)) }},
		{"action", func(v *APIPermissionGroup) { v.Action = "delete" }},
		{"empty endpoints", func(v *APIPermissionGroup) { v.EndpointIDs = nil }},
		{"too many endpoints", func(v *APIPermissionGroup) { v.EndpointIDs = tooMany }},
		{"nil endpoint", func(v *APIPermissionGroup) { v.EndpointIDs = []uuid.UUID{uuid.Nil} }},
		{"duplicate endpoint", func(v *APIPermissionGroup) {
			id := uuid.New()
			v.EndpointIDs = []uuid.UUID{id, id}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.EndpointIDs = append([]uuid.UUID(nil), base.EndpointIDs...)
			tc.modify(&in)
			out, err := s.Save(context.Background(), in)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid input accepted: %+v %v", out, err)
			}
		})
	}
}

func TestAPIPermissionGroupServiceListAndDeleteValidate(t *testing.T) {
	r := newMemoryPermissionGroupRepository()
	s := NewAPIPermissionGroupService(r)
	if _, err := s.List(context.Background(), "  "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank service accepted on list: %v", err)
	}
	created, err := s.Save(context.Background(), permissionGroupTestInput())
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(context.Background(), "nexusauth")
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("list did not return the saved group: %+v %v", items, err)
	}
	if err = s.Delete(context.Background(), "  ", created.ID); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank service accepted on delete: %v", err)
	}
	if err = s.Delete(context.Background(), "nexusauth", uuid.Nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil id accepted on delete: %v", err)
	}
	if err = s.Delete(context.Background(), "other", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-service delete accepted: %v", err)
	}
	if err = s.Delete(context.Background(), "nexusauth", created.ID); err != nil {
		t.Fatal(err)
	}
	if items, err = s.List(context.Background(), "nexusauth"); err != nil || len(items) != 0 {
		t.Fatalf("delete left group behind: %+v %v", items, err)
	}
}

func TestAPIPermissionGroupServicePropagatesRepositoryError(t *testing.T) {
	r := newMemoryPermissionGroupRepository()
	r.err = errors.New("database unavailable")
	s := NewAPIPermissionGroupService(r)
	ctx := context.Background()
	if _, err := s.List(ctx, "nexusauth"); !errors.Is(err, r.err) {
		t.Fatalf("list error not propagated: %v", err)
	}
	if _, err := s.Save(ctx, permissionGroupTestInput()); !errors.Is(err, r.err) {
		t.Fatalf("save error not propagated: %v", err)
	}
	if err := s.Delete(ctx, "nexusauth", uuid.New()); !errors.Is(err, r.err) {
		t.Fatalf("delete error not propagated: %v", err)
	}
}

func TestAPIPermissionGroupMethodAllowed(t *testing.T) {
	for _, tc := range []struct {
		action, method string
		want           bool
	}{
		{"read", "GET", true},
		{"read", "HEAD", true},
		{"read", "POST", false},
		{"read", "DELETE", false},
		// Write groups may include read-shaped POST endpoints.
		{"write", "POST", true},
		{"write", "GET", true},
		{"write", "DELETE", true},
		{"write", "TRACE", false},
		{"write", "CONNECT", false},
		{"read", "TRACE", false},
		{"", "GET", false},
	} {
		if got := APIPermissionGroupMethodAllowed(tc.action, tc.method); got != tc.want {
			t.Fatalf("APIPermissionGroupMethodAllowed(%q,%q)=%v want %v", tc.action, tc.method, got, tc.want)
		}
	}
}
