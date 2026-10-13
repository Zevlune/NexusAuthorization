package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// APIPermissionGroup is a service-scoped, maintained bundle of API endpoints.
// Token policies reference groups by id and resolve their endpoints from the
// local catalog, so deleting a referenced group must be blocked.
type APIPermissionGroup struct {
	ID              uuid.UUID   `json:"id"`
	ServiceResource string      `json:"service_resource"`
	Code            string      `json:"code"`
	Name            string      `json:"name"`
	Action          string      `json:"action"`
	Enabled         bool        `json:"enabled"`
	EndpointIDs     []uuid.UUID `json:"endpoint_ids"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

const (
	APIPermissionGroupActionRead  = "read"
	APIPermissionGroupActionWrite = "write"

	// APIPermissionGroupMaxEndpoints bounds a single group so a token policy
	// cannot expand into an unbounded grant set.
	APIPermissionGroupMaxEndpoints = 1000
)

// apiPermissionGroupMethods is the set of HTTP methods the local catalog may
// store. Grants always come from explicit group mappings; a group action never
// widens this set on its own.
var apiPermissionGroupMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

// APIPermissionGroupAction normalizes a group action, returning "" when it is
// not one of the supported read/write values.
func APIPermissionGroupAction(value string) string {
	switch strings.TrimSpace(value) {
	case APIPermissionGroupActionRead:
		return APIPermissionGroupActionRead
	case APIPermissionGroupActionWrite:
		return APIPermissionGroupActionWrite
	default:
		return ""
	}
}

// APIPermissionGroupMethodAllowed reports whether an endpoint method may belong
// to a group with the given action. Read groups stay on GET/HEAD; write groups
// may include read-shaped POST endpoints, so only unsupported methods fail and
// the action never grants a method that the mapping did not select.
func APIPermissionGroupMethodAllowed(action, method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	if _, ok := apiPermissionGroupMethods[method]; !ok {
		return false
	}
	switch APIPermissionGroupAction(action) {
	case APIPermissionGroupActionRead:
		return method == "GET" || method == "HEAD"
	case APIPermissionGroupActionWrite:
		return true
	default:
		return false
	}
}

// APIPermissionGroupRepository persists the service-scoped group catalog. All
// endpoint scope and method validation is resolved against the local
// api_endpoints table inside the write transaction; no remote catalog is used.
type APIPermissionGroupRepository interface {
	List(context.Context, string) ([]APIPermissionGroup, error)
	Save(context.Context, APIPermissionGroup) (APIPermissionGroup, error)
	Delete(context.Context, string, uuid.UUID) error
}

type APIPermissionGroupService struct{ repository APIPermissionGroupRepository }

func NewAPIPermissionGroupService(repository APIPermissionGroupRepository) *APIPermissionGroupService {
	return &APIPermissionGroupService{repository: repository}
}

func (s *APIPermissionGroupService) List(ctx context.Context, service string) ([]APIPermissionGroup, error) {
	service = strings.TrimSpace(service)
	if service == "" || len(service) > 128 {
		return nil, fmt.Errorf("%w: service_resource is required and at most 128 characters", ErrInvalidArgument)
	}
	return s.repository.List(ctx, service)
}

// normalizeAPIPermissionGroup trims and bounds the caller supplied fields. The
// repository revalidates endpoint scope, enabled state and method/action
// agreement, and the unique code index protects concurrent creates.
func normalizeAPIPermissionGroup(group APIPermissionGroup) (APIPermissionGroup, error) {
	group.ServiceResource = strings.TrimSpace(group.ServiceResource)
	group.Code = strings.TrimSpace(group.Code)
	group.Name = strings.TrimSpace(group.Name)
	group.Action = strings.TrimSpace(group.Action)
	if group.ServiceResource == "" || len(group.ServiceResource) > 128 {
		return group, fmt.Errorf("%w: service_resource is required and at most 128 characters", ErrInvalidArgument)
	}
	if group.Code == "" || len(group.Code) > 128 {
		return group, fmt.Errorf("%w: code is required and at most 128 characters", ErrInvalidArgument)
	}
	if group.Name == "" || len([]rune(group.Name)) > 200 {
		return group, fmt.Errorf("%w: name is required and at most 200 characters", ErrInvalidArgument)
	}
	if APIPermissionGroupAction(group.Action) == "" {
		return group, fmt.Errorf("%w: action must be read or write", ErrInvalidArgument)
	}
	if len(group.EndpointIDs) == 0 || len(group.EndpointIDs) > APIPermissionGroupMaxEndpoints {
		return group, fmt.Errorf("%w: select between 1 and %d endpoints", ErrInvalidArgument, APIPermissionGroupMaxEndpoints)
	}
	seen := make(map[uuid.UUID]struct{}, len(group.EndpointIDs))
	endpoints := make([]uuid.UUID, 0, len(group.EndpointIDs))
	for _, id := range group.EndpointIDs {
		if id == uuid.Nil {
			return group, fmt.Errorf("%w: endpoint ids must be non-empty UUIDs", ErrInvalidArgument)
		}
		if _, ok := seen[id]; ok {
			return group, fmt.Errorf("%w: duplicate endpoint id", ErrInvalidArgument)
		}
		seen[id] = struct{}{}
		endpoints = append(endpoints, id)
	}
	group.EndpointIDs = endpoints
	return group, nil
}

// Save keeps service_resource and code immutable for an existing group; the
// repository locks the row and rejects any change to those identity fields.
func (s *APIPermissionGroupService) Save(ctx context.Context, group APIPermissionGroup) (APIPermissionGroup, error) {
	group, err := normalizeAPIPermissionGroup(group)
	if err != nil {
		return APIPermissionGroup{}, err
	}
	return s.repository.Save(ctx, group)
}

func (s *APIPermissionGroupService) Delete(ctx context.Context, service string, id uuid.UUID) error {
	service = strings.TrimSpace(service)
	if service == "" || len(service) > 128 {
		return fmt.Errorf("%w: service_resource is required and at most 128 characters", ErrInvalidArgument)
	}
	if id == uuid.Nil {
		return fmt.Errorf("%w: permission group id is required", ErrInvalidArgument)
	}
	return s.repository.Delete(ctx, service, id)
}
