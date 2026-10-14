package repo

import (
	"fmt"
	"strings"

	"github.com/luck/nexus-authorization/internal/biz"
)

// These normalization helpers are retained for existing regression tests.
// normalizeUserRoleScope 清理并校验用户及服务资源作用域。
func normalizeUserRoleScope(subject, serviceResource string) (string, string, error) {
	subject, serviceResource = strings.TrimSpace(subject), strings.TrimSpace(serviceResource)
	if subject == "" || len(subject) > 80 {
		return "", "", fmt.Errorf("%w: subject must be 1-80 characters", biz.ErrInvalidArgument)
	}
	if serviceResource == "" || len([]rune(serviceResource)) > 128 {
		return "", "", fmt.Errorf("%w: service_resource must be 1-128 characters", biz.ErrInvalidArgument)
	}
	return subject, serviceResource, nil
}

// normalizeRoleIDs 清理并校验角色 ID 列表，同时拒绝重复值。
func normalizeRoleIDs(roleIDs []string) ([]string, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(roleIDs))
	seen := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" || len(roleID) > 80 {
			return nil, fmt.Errorf("%w: role_id must be 1-80 characters", biz.ErrInvalidArgument)
		}
		if _, ok := seen[roleID]; ok {
			return nil, fmt.Errorf("%w: duplicate role_id %q", biz.ErrInvalidArgument, roleID)
		}
		seen[roleID] = struct{}{}
		normalized = append(normalized, roleID)
	}
	return normalized, nil
}
