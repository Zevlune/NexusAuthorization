package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
)

const currentPolicyBodySQL = `jsonb_build_object(
 'app_key',a.app_key,'application_id',a.id,
 'service_resources',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',x.service_resource_key) ORDER BY x.service_resource_key) FROM application_service_resources x WHERE x.application_id=a.id AND x.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'endpoints',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'service_resource',e.service_resource_key,'method',e.method,'path_template',e.path_template,'enabled',e.enabled) ORDER BY e.id) FROM api_endpoints e JOIN application_service_resources x ON x.service_resource_key=e.service_resource_key WHERE x.application_id=a.id AND e.service_resource_key=ANY($2::text[])),'[]'::jsonb),
 'roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'code',r.code,'enabled',r.enabled,'endpoint_ids',COALESCE((SELECT jsonb_agg(b.endpoint_id ORDER BY b.endpoint_id) FROM role_api_endpoint_bindings b WHERE b.role_id=r.id),'[]'::jsonb),'menu_ids',COALESCE((SELECT jsonb_agg(b.menu_id ORDER BY b.menu_id) FROM role_menu_bindings b WHERE b.role_id=r.id),'[]'::jsonb)) ORDER BY r.id) FROM roles r WHERE r.application_id=a.id),'[]'::jsonb),
 'subject_roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',b.subject,'domain',b.domain,'role_id',b.role_id) ORDER BY b.subject,b.domain,b.role_id) FROM subject_role_bindings b WHERE b.application_id=a.id),'[]'::jsonb),
 'menus',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'parent_id',m.parent_id,'code',m.code,'name',m.name,'menu_type',m.menu_type,'route',m.route,'component',m.component,'icon',m.icon,'sort_order',m.sort_order,'enabled',m.enabled) ORDER BY m.sort_order,m.id) FROM menus m WHERE m.application_id=a.id),'[]'::jsonb)
 )`

// Read the current normalized records and their content digest in one MVCC
// statement. The token is a SHA256 digest of the canonical JSON encoding of
// those records, so callers can detect a save that landed after metadata was
// read without any stored revision marker. Nothing here is persisted to the
// database or Redis.
const applicationCurrentPolicyByKeySQL = `SELECT ` + currentPolicyBodySQL + ` FROM applications a WHERE a.app_key=$1 AND a.enabled`
const applicationCurrentPolicyByIDSQL = `SELECT ` + currentPolicyBodySQL + ` FROM applications a WHERE a.id=$1 AND a.enabled`

// currentPolicyFingerprint derives the in-memory content token from one decoded
// snapshot. Go's json.Marshal emits map keys in sorted order and a fixed escape
// set, so the digest is stable across reads of equal content.
func currentPolicyFingerprint(data map[string]any) (string, error) {
	canonical, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("encode current policy digest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// parsePolicyApplicationID reads the application id embedded in the policy JSON.
func parsePolicyApplicationID(value any) (uuid.UUID, error) {
	text, _ := value.(string)
	id, err := uuid.Parse(text)
	if err != nil {
		return uuid.Nil, fmt.Errorf("decode current policy application id: %w", err)
	}
	return id, nil
}

// GetPolicyMetadata resolves the application and reads the current normalized
// records plus their content digest in one SQL statement. The digest is the
// current-policy token used only as an in-memory cache identity, and the
// application id is taken from the same statement's JSON.
func (r *RBACV1Repository) GetPolicyMetadata(ctx context.Context, scope string) (biz.CurrentPolicy, error) {
	var p biz.CurrentPolicy
	if strings.TrimSpace(scope) == "" {
		return p, biz.ErrInvalidArgument
	}
	keys, err := r.activeServiceKeys(ctx)
	if err != nil {
		return p, err
	}
	var raw []byte
	if err = r.pool.QueryRow(ctx, applicationCurrentPolicyByKeySQL, scope, keys).Scan(&raw); err != nil {
		return p, mapDBError(err)
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return p, fmt.Errorf("decode current policy: %w", err)
	}
	id, err := parsePolicyApplicationID(data["application_id"])
	if err != nil {
		return p, err
	}
	p.ApplicationID = id
	p.ServiceKeys = keys
	token, err := currentPolicyFingerprint(data)
	if err != nil {
		return p, err
	}
	p.Token = token
	p.Data = data
	return p, nil
}

// GetCurrentPolicy returns the records already read with the metadata. It only
// re-reads from the database when the caller supplied metadata without data,
// validating the requested token against the freshly read digest so a racing
// save surfaces as ErrConflict instead of a stale policy.
func (r *RBACV1Repository) GetCurrentPolicy(ctx context.Context, p biz.CurrentPolicy) (biz.CurrentPolicy, error) {
	if p.Data != nil {
		return p, nil
	}
	// One statement reads the JSON and its digest from the same snapshot.
	var raw []byte
	if err := r.pool.QueryRow(ctx, applicationCurrentPolicyByIDSQL, p.ApplicationID, p.ServiceKeys).Scan(&raw); err != nil {
		return p, mapDBError(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return p, fmt.Errorf("decode current policy: %w", err)
	}
	token, err := currentPolicyFingerprint(data)
	if err != nil {
		return p, err
	}
	// A save landed after metadata was read. Signal the service to re-read
	// metadata instead of serving a stale policy.
	if p.Token != "" && p.Token != token {
		return p, biz.ErrConflict
	}
	p.Token = token
	p.Data = data
	return p, nil
}
