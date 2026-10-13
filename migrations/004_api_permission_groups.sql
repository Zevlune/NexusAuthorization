-- Service-scoped API permission group catalog. A group bundles a bounded,
-- unique set of local api_endpoints so a token policy can reference maintained
-- groups instead of re-expanding endpoints. This migration is safe to re-run.
BEGIN;

CREATE TABLE IF NOT EXISTS api_permission_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_resource_key VARCHAR(128) NOT NULL,
    code VARCHAR(128) NOT NULL,
    name VARCHAR(200) NOT NULL,
    action VARCHAR(16) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (btrim(service_resource_key) <> ''),
    CHECK (btrim(code) <> ''),
    CHECK (btrim(name) <> ''),
    CHECK (action IN ('read', 'write'))
);

CREATE UNIQUE INDEX IF NOT EXISTS api_permission_groups_service_code_uq
    ON api_permission_groups(service_resource_key, code);
CREATE INDEX IF NOT EXISTS api_permission_groups_service_idx
    ON api_permission_groups(service_resource_key, code);

CREATE TABLE IF NOT EXISTS api_permission_group_endpoints (
    group_id UUID NOT NULL REFERENCES api_permission_groups(id) ON DELETE CASCADE,
    endpoint_id UUID NOT NULL REFERENCES api_endpoints(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, endpoint_id)
);

CREATE INDEX IF NOT EXISTS api_permission_group_endpoints_endpoint_idx
    ON api_permission_group_endpoints(endpoint_id);

-- Token policies reference permission groups by id. Existing rows get an empty
-- policy list so the runtime never has to treat NULL as a wildcard.
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS policies JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE OR REPLACE FUNCTION enforce_api_permission_group_scope()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    group_service VARCHAR(128);
    group_action VARCHAR(16);
    endpoint_service VARCHAR(128);
    endpoint_method VARCHAR(16);
    endpoint_enabled BOOLEAN;
BEGIN
    SELECT service_resource_key, action INTO group_service, group_action
        FROM api_permission_groups WHERE id = NEW.group_id;
    SELECT service_resource_key, method, enabled INTO endpoint_service, endpoint_method, endpoint_enabled
        FROM api_endpoints WHERE id = NEW.endpoint_id;
    IF group_service IS NULL OR endpoint_service IS NULL THEN
        RAISE EXCEPTION 'API permission group and endpoint must both exist'
            USING ERRCODE = '23514';
    END IF;
    IF endpoint_service <> group_service THEN
        RAISE EXCEPTION 'API permission group endpoint must belong to the group service'
            USING ERRCODE = '23514';
    END IF;
    IF endpoint_enabled IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'API permission group endpoint must be enabled'
            USING ERRCODE = '23514';
    END IF;
    IF endpoint_method NOT IN ('GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE') THEN
        RAISE EXCEPTION 'API permission group endpoint method is not supported'
            USING ERRCODE = '23514';
    END IF;
    IF group_action = 'read' AND endpoint_method NOT IN ('GET', 'HEAD') THEN
        RAISE EXCEPTION 'read API permission groups may only contain GET or HEAD endpoints'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS api_permission_group_scope_guard ON api_permission_group_endpoints;
CREATE TRIGGER api_permission_group_scope_guard
BEFORE INSERT OR UPDATE ON api_permission_group_endpoints
FOR EACH ROW EXECUTE FUNCTION enforce_api_permission_group_scope();

COMMIT;
