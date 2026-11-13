-- Team quotas are now owned by the portal quota store instead of being
-- mirrored from the RBAC ConfigMap. user_quotas already exists from 0002.
--
-- An empty resource value or NULL max_workspaces means unlimited.

SET LOCAL search_path TO poddy, public;

CREATE TABLE IF NOT EXISTS team_quotas (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    team text NOT NULL CHECK (btrim(team) <> ''),
    max_workspaces integer
        CHECK (max_workspaces IS NULL OR max_workspaces >= 0),
    cpu text NOT NULL DEFAULT '',
    memory text NOT NULL DEFAULT '',
    storage text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, team)
);

CREATE INDEX IF NOT EXISTS team_quotas_team_idx
    ON team_quotas (namespace, lower(team));
