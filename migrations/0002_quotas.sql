-- Workspace quotas mirrored from the RBAC ConfigMap.
--
-- Kubernetes remains the source of truth. These columns and the user_quotas
-- table exist for reporting and administration from SQL; the portal still
-- enforces quotas from the RBAC document.
--
-- Resource values are stored as text because they are Kubernetes quantities
-- such as "500m", "4Gi", and "10Gi". An empty value or NULL max_workspaces
-- means unlimited.

SET LOCAL search_path TO poddy, public;

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS quota_max_workspaces integer
        CHECK (quota_max_workspaces IS NULL OR quota_max_workspaces >= 0),
    ADD COLUMN IF NOT EXISTS quota_cpu text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quota_memory text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quota_storage text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS user_quotas (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    identity text NOT NULL CHECK (btrim(identity) <> ''),
    max_workspaces integer
        CHECK (max_workspaces IS NULL OR max_workspaces >= 0),
    cpu text NOT NULL DEFAULT '',
    memory text NOT NULL DEFAULT '',
    storage text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, identity)
);

CREATE INDEX IF NOT EXISTS user_quotas_identity_idx
    ON user_quotas (namespace, lower(identity));
