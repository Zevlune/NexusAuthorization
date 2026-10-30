package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luck/nexus-authorization/internal/biz"
)

// Read menu grants, bindings and the full tree in one database snapshot. API
// endpoints and remote service state cannot change application menu visibility.
const applicationMenuPolicySQL = `SELECT a.id,jsonb_build_object(
 'roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'enabled',r.enabled,'menu_ids',COALESCE((SELECT jsonb_agg(b.menu_id ORDER BY b.menu_id) FROM role_menu_bindings b WHERE b.role_id=r.id),'[]'::jsonb)) ORDER BY r.id) FROM roles r WHERE r.application_id=a.id),'[]'::jsonb),
 'subject_roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',b.subject,'domain',b.domain,'role_id',b.role_id) ORDER BY b.subject,b.domain,b.role_id) FROM subject_role_bindings b WHERE b.application_id=a.id),'[]'::jsonb),
 'menus',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'application_id',m.application_id,'parent_id',m.parent_id,'code',m.code,'name',m.name,'menu_type',m.menu_type,'route',m.route,'component',m.component,'icon',m.icon,'sort_order',m.sort_order,'enabled',m.enabled) ORDER BY m.sort_order,m.id) FROM menus m WHERE m.application_id=a.id),'[]'::jsonb)
 ) FROM applications a WHERE a.app_key=$1 AND a.enabled`

func (r *RBACV1Repository) GetMenuPolicy(ctx context.Context, app string) (biz.CurrentPolicy, error) {
	var policy biz.CurrentPolicy
	app = strings.TrimSpace(app)
	if app == "" {
		return policy, biz.ErrInvalidArgument
	}
	var raw []byte
	if err := r.pool.QueryRow(ctx, applicationMenuPolicySQL, app).Scan(&policy.ApplicationID, &raw); err != nil {
		return policy, mapDBError(err)
	}
	if err := json.Unmarshal(raw, &policy.Data); err != nil {
		return policy, fmt.Errorf("decode menu policy: %w", err)
	}
	return policy, nil
}
