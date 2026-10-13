-- Application-scoped API tokens and immutable endpoint grant snapshots.
-- This migration is safe to run more than once.
BEGIN;

CREATE TABLE IF NOT EXISTS api_tokens (
    id UUID PRIMARY KEY,
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    token_prefix VARCHAR(32) NOT NULL,
    token_hash VARCHAR(64) NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    owner_id VARCHAR(200) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_by VARCHAR(200) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    CHECK (btrim(name) <> ''),
    CHECK (btrim(token_prefix) <> ''),
    CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CHECK (owner_type IN ('user', 'service')),
    CHECK (btrim(owner_id) <> ''),
    CHECK (status IN ('active', 'disabled', 'revoked')),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_token_hash_uq ON api_tokens(token_hash);
CREATE INDEX IF NOT EXISTS api_tokens_application_idx ON api_tokens(application_id, created_at DESC);
CREATE INDEX IF NOT EXISTS api_tokens_prefix_idx ON api_tokens(token_prefix);

CREATE TABLE IF NOT EXISTS api_token_permissions (
    token_id UUID NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    endpoint_id UUID NOT NULL REFERENCES api_endpoints(id) ON DELETE RESTRICT,
    service_resource_key VARCHAR(128) NOT NULL,
    action VARCHAR(16) NOT NULL,
    tenant_id VARCHAR(200) NOT NULL DEFAULT '',
    project_id VARCHAR(200) NOT NULL DEFAULT '',
    method VARCHAR(16) NOT NULL,
    path_template VARCHAR(500) NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (token_id, endpoint_id, tenant_id, project_id),
    CHECK (btrim(service_resource_key) <> ''),
    CHECK (action IN ('read', 'write')),
    CHECK (method = upper(method)),
    CHECK (left(path_template, 1) = '/')
);

CREATE INDEX IF NOT EXISTS api_token_permissions_endpoint_idx ON api_token_permissions(endpoint_id);

CREATE TABLE IF NOT EXISTS api_token_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id UUID NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    actor_id VARCHAR(200) NOT NULL DEFAULT 'system',
    action VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (btrim(actor_id) <> ''),
    CHECK (btrim(action) <> '')
);

CREATE INDEX IF NOT EXISTS api_token_audit_logs_token_idx ON api_token_audit_logs(token_id, created_at DESC);

CREATE OR REPLACE FUNCTION enforce_api_token_permission_scope()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    token_application UUID;
    endpoint_service VARCHAR(128);
BEGIN
    SELECT application_id INTO token_application FROM api_tokens WHERE id = NEW.token_id;
    SELECT service_resource_key INTO endpoint_service FROM api_endpoints WHERE id = NEW.endpoint_id;
    IF token_application IS NULL OR endpoint_service IS NULL
       OR endpoint_service <> NEW.service_resource_key
       OR NOT EXISTS (
           SELECT 1 FROM application_service_resources
           WHERE application_id = token_application AND service_resource_key = endpoint_service
       ) THEN
        RAISE EXCEPTION 'API token endpoint must belong to a service linked to its application'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS api_token_permission_scope_guard ON api_token_permissions;
CREATE TRIGGER api_token_permission_scope_guard
BEFORE INSERT OR UPDATE ON api_token_permissions
FOR EACH ROW EXECUTE FUNCTION enforce_api_token_permission_scope();

COMMIT;
