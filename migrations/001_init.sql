-- NexusAuthorization initial schema for the first installation.
-- Connect to the empty target database before running this file.
-- This file is the complete initial schema; it contains no seed data.
BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE service_resource_source AS ENUM ('local', 'nexusauth');
CREATE TYPE endpoint_source AS ENUM ('manual', 'swagger');
CREATE TYPE menu_type AS ENUM ('directory', 'menu', 'button');

CREATE TABLE service_resources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_key VARCHAR(128) NOT NULL UNIQUE,
    display_name VARCHAR(200) NOT NULL,
    audience VARCHAR(200) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source service_resource_source NOT NULL DEFAULT 'local',
    external_id VARCHAR(200),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source = 'local' AND external_id IS NULL),
    CHECK (btrim(resource_key) <> ''),
    CHECK (btrim(display_name) <> '')
);

CREATE TABLE applications (
    policy_token UUID NOT NULL DEFAULT gen_random_uuid(),
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_key VARCHAR(128) NOT NULL UNIQUE,
    display_name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    url VARCHAR(2048) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (btrim(app_key) <> ''),
    CHECK (btrim(display_name) <> '')
);

CREATE TABLE application_service_resources (
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    service_resource_key VARCHAR(128) NOT NULL CHECK (btrim(service_resource_key) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (application_id, service_resource_key)
);

CREATE TABLE api_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_resource_key VARCHAR(128) NOT NULL CHECK (btrim(service_resource_key) <> ''),
    endpoint_key VARCHAR(160) NOT NULL,
    method VARCHAR(16) NOT NULL,
    path_template VARCHAR(500) NOT NULL,
    module VARCHAR(200) NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    source endpoint_source NOT NULL DEFAULT 'manual',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_discovered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (service_resource_key, endpoint_key),
    UNIQUE (service_resource_key, method, path_template),
    CHECK (btrim(endpoint_key) <> ''),
    CHECK (method = upper(method)),
    CHECK (left(path_template, 1) = '/')
);

-- Compatibility table for the still-registered /api/v1/authorization/api-endpoints
-- routes. It is a standalone, service-resource-scoped endpoint registry with
-- audit columns and soft delete; it is separate from api_endpoints and does not
-- participate in application RBAC matching.
CREATE TABLE authorization_api_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_resource VARCHAR(128) NOT NULL,
    controller VARCHAR(200) NOT NULL,
    method VARCHAR(16) NOT NULL,
    path_template VARCHAR(500) NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_id VARCHAR(200) NOT NULL DEFAULT 'system',
    created_by_name VARCHAR(200) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by_id VARCHAR(200) NOT NULL DEFAULT 'system',
    updated_by_name VARCHAR(200) NOT NULL DEFAULT 'system',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK (btrim(service_resource) <> ''),
    CHECK (btrim(controller) <> ''),
    CHECK (method = upper(method)),
    CHECK (left(path_template, 1) = '/')
);

CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    code VARCHAR(160) NOT NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (application_id, code),
    UNIQUE (id, application_id),
    CHECK (btrim(code) <> ''),
    CHECK (btrim(name) <> '')
);

CREATE TABLE menus (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    parent_id UUID,
    menu_type menu_type NOT NULL,
    code VARCHAR(160) NOT NULL,
    name VARCHAR(200) NOT NULL,
    route VARCHAR(500) NOT NULL DEFAULT '',
    component VARCHAR(300) NOT NULL DEFAULT '',
    icon VARCHAR(100) NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (application_id, code),
    UNIQUE (id, application_id),
    FOREIGN KEY (parent_id, application_id) REFERENCES menus(id, application_id) ON DELETE RESTRICT,
    CHECK (btrim(code) <> ''),
    CHECK (btrim(name) <> '')
);

CREATE TABLE role_api_endpoint_bindings (
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    endpoint_id UUID NOT NULL REFERENCES api_endpoints(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, endpoint_id)
);

CREATE TABLE role_menu_bindings (
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    menu_id UUID NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, menu_id)
);

CREATE TABLE subject_role_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    subject VARCHAR(200) NOT NULL,
    domain VARCHAR(200) NOT NULL DEFAULT '',
    role_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (application_id, subject, domain, role_id),
    FOREIGN KEY (role_id, application_id) REFERENCES roles(id, application_id) ON DELETE CASCADE,
    CHECK (btrim(subject) <> '')
);

CREATE OR REPLACE FUNCTION prevent_disabling_linked_service_resource()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM application_service_resources WHERE service_resource_key = OLD.resource_key)
           OR EXISTS (SELECT 1 FROM api_endpoints WHERE service_resource_key = OLD.resource_key) THEN
            RAISE EXCEPTION 'cannot delete a service resource with application links or API endpoints'
                USING ERRCODE = '23514';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.enabled = TRUE AND NEW.enabled = FALSE AND EXISTS (
        SELECT 1 FROM application_service_resources
        WHERE service_resource_key = NEW.resource_key
    ) THEN
        RAISE EXCEPTION 'cannot disable a service resource linked to an application'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER service_resource_enabled_guard
BEFORE UPDATE OF enabled OR DELETE ON service_resources
FOR EACH ROW EXECUTE FUNCTION prevent_disabling_linked_service_resource();

CREATE OR REPLACE FUNCTION prevent_unlinking_granted_service_resource()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM role_api_endpoint_bindings b
        JOIN roles r ON r.id = b.role_id AND r.application_id = OLD.application_id
        JOIN api_endpoints e ON e.id = b.endpoint_id
        WHERE e.service_resource_key = OLD.service_resource_key
    ) THEN
        RAISE EXCEPTION 'cannot remove a service resource with existing API grants'
            USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER application_service_resource_grant_guard
BEFORE DELETE ON application_service_resources
FOR EACH ROW EXECUTE FUNCTION prevent_unlinking_granted_service_resource();

CREATE OR REPLACE FUNCTION enforce_role_api_endpoint_application()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    role_application UUID;
    endpoint_service VARCHAR(128);
BEGIN
    SELECT application_id INTO role_application FROM roles WHERE id = NEW.role_id;
    SELECT service_resource_key INTO endpoint_service FROM api_endpoints WHERE id = NEW.endpoint_id;
    IF role_application IS NULL OR endpoint_service IS NULL OR NOT EXISTS (
        SELECT 1 FROM application_service_resources
        WHERE application_id = role_application AND service_resource_key = endpoint_service
    ) THEN
        RAISE EXCEPTION 'role API endpoint must belong to a service linked to the application'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER role_api_endpoint_application_guard
BEFORE INSERT OR UPDATE ON role_api_endpoint_bindings
FOR EACH ROW EXECUTE FUNCTION enforce_role_api_endpoint_application();

CREATE OR REPLACE FUNCTION enforce_role_menu_application()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    role_application UUID;
    menu_application UUID;
BEGIN
    SELECT application_id INTO role_application FROM roles WHERE id = NEW.role_id;
    SELECT application_id INTO menu_application FROM menus WHERE id = NEW.menu_id;
    IF role_application IS NULL OR menu_application IS NULL OR role_application <> menu_application THEN
        RAISE EXCEPTION 'role and menu must belong to the same application'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER role_menu_application_guard
BEFORE INSERT OR UPDATE ON role_menu_bindings
FOR EACH ROW EXECUTE FUNCTION enforce_role_menu_application();

CREATE INDEX api_endpoints_scope_enabled_idx ON api_endpoints(service_resource_key, enabled);
CREATE UNIQUE INDEX authorization_api_endpoints_active_route_idx
    ON authorization_api_endpoints(service_resource, method, path_template)
    WHERE is_deleted = FALSE;
CREATE INDEX role_api_endpoint_bindings_endpoint_idx ON role_api_endpoint_bindings(endpoint_id, role_id);
CREATE INDEX subject_role_bindings_application_lookup_idx ON subject_role_bindings(application_id, subject, domain);
CREATE INDEX menus_application_enabled_idx ON menus(application_id, enabled, sort_order);
CREATE INDEX application_service_resources_service_idx ON application_service_resources(service_resource_key, application_id);

-- Internal cache invalidation only: no release history or permission snapshots.
CREATE OR REPLACE FUNCTION refresh_application_policy_token()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.policy_token := gen_random_uuid();
    RETURN NEW;
END;
$$;
CREATE TRIGGER application_policy_token BEFORE UPDATE ON applications
FOR EACH ROW EXECUTE FUNCTION refresh_application_policy_token();

-- Transition tables allow one invalidation per affected application per statement,
-- including bulk grants/members, updates that change ownership, and deletes.
CREATE OR REPLACE FUNCTION invalidate_current_policy()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    changed_sql TEXT;
    affected_sql TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        changed_sql := 'SELECT to_jsonb(r) AS row FROM new_rows r';
    ELSIF TG_OP = 'DELETE' THEN
        changed_sql := 'SELECT to_jsonb(r) AS row FROM old_rows r';
    ELSE
        changed_sql := 'SELECT to_jsonb(r) AS row FROM new_rows r UNION ALL SELECT to_jsonb(r) AS row FROM old_rows r';
    END IF;
    IF TG_TABLE_NAME IN ('roles','menus','subject_role_bindings','application_service_resources') THEN
        affected_sql := 'SELECT (row->>''application_id'')::uuid FROM changed';
    ELSIF TG_TABLE_NAME IN ('role_api_endpoint_bindings','role_menu_bindings') THEN
        affected_sql := 'SELECT application_id FROM roles WHERE id IN (SELECT (row->>''role_id'')::uuid FROM changed)';
    ELSIF TG_TABLE_NAME = 'api_endpoints' THEN
        affected_sql := 'SELECT application_id FROM application_service_resources WHERE service_resource_key IN (SELECT row->>''service_resource_key'' FROM changed)';
    ELSE
        affected_sql := 'SELECT application_id FROM application_service_resources WHERE service_resource_key IN (SELECT row->>''resource_key'' FROM changed)';
    END IF;
    EXECUTE 'WITH changed AS (' || changed_sql || ') UPDATE applications SET policy_token=gen_random_uuid() WHERE id IN (' || affected_sql || ')';
    RETURN NULL;
END;
$$;
CREATE TRIGGER roles_policy_insert AFTER INSERT ON roles
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER roles_policy_update AFTER UPDATE ON roles
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER roles_policy_delete AFTER DELETE ON roles
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER menus_policy_insert AFTER INSERT ON menus
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER menus_policy_update AFTER UPDATE ON menus
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER menus_policy_delete AFTER DELETE ON menus
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER subject_role_bindings_policy_insert AFTER INSERT ON subject_role_bindings
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER subject_role_bindings_policy_update AFTER UPDATE ON subject_role_bindings
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER subject_role_bindings_policy_delete AFTER DELETE ON subject_role_bindings
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER application_service_resources_policy_insert AFTER INSERT ON application_service_resources
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER application_service_resources_policy_update AFTER UPDATE ON application_service_resources
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER application_service_resources_policy_delete AFTER DELETE ON application_service_resources
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_api_endpoint_bindings_policy_insert AFTER INSERT ON role_api_endpoint_bindings
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_api_endpoint_bindings_policy_update AFTER UPDATE ON role_api_endpoint_bindings
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_api_endpoint_bindings_policy_delete AFTER DELETE ON role_api_endpoint_bindings
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_menu_bindings_policy_insert AFTER INSERT ON role_menu_bindings
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_menu_bindings_policy_update AFTER UPDATE ON role_menu_bindings
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER role_menu_bindings_policy_delete AFTER DELETE ON role_menu_bindings
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER api_endpoints_policy_insert AFTER INSERT ON api_endpoints
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER api_endpoints_policy_update AFTER UPDATE ON api_endpoints
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER api_endpoints_policy_delete AFTER DELETE ON api_endpoints
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER service_resources_policy_insert AFTER INSERT ON service_resources
REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER service_resources_policy_update AFTER UPDATE ON service_resources
REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();
CREATE TRIGGER service_resources_policy_delete AFTER DELETE ON service_resources
REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION invalidate_current_policy();

COMMIT;
