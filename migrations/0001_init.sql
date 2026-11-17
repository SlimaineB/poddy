-- poddy PostgreSQL schema baseline.
--
-- This is the schema for the first official release. It replaces the
-- pre-release incremental migrations, which had no external users.
--
-- Kubernetes remains the runtime source of truth. The portal mirrors metadata
-- and audit events into this schema. It stores control-plane metadata only:
-- never PVC contents, plaintext OAuth tokens, or Kubernetes credentials.

CREATE SCHEMA IF NOT EXISTS poddy;
SET LOCAL search_path TO poddy, public;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

-- A tenant is one workspace namespace. Deploying one release per namespace is
-- the hard isolation boundary.
CREATE TABLE tenants (
    namespace text PRIMARY KEY
        CHECK (namespace ~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' AND char_length(namespace) <= 63),
    display_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- OIDC identities seen by the portal. identity is the stable key used by RBAC
-- (for example alice@example.com, user:alice, or token).
CREATE TABLE users (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    identity text NOT NULL CHECK (btrim(identity) <> ''),
    subject text,
    username text,
    email text,
    display_name text NOT NULL DEFAULT '',
    groups text[] NOT NULL DEFAULT '{}',
    first_seen timestamptz,
    last_seen timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, identity)
);

CREATE UNIQUE INDEX users_subject_uniq
    ON users (namespace, subject)
    WHERE subject IS NOT NULL AND subject <> '';

CREATE UNIQUE INDEX users_email_uniq
    ON users (namespace, lower(email))
    WHERE email IS NOT NULL AND email <> '';

CREATE INDEX users_groups_gin ON users USING gin (groups);

CREATE TABLE global_admins (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    identity text NOT NULL CHECK (btrim(identity) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, identity)
);

CREATE TABLE teams (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    name text NOT NULL
        CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$'),
    display_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    quota_max_workspaces integer
        CHECK (quota_max_workspaces IS NULL OR quota_max_workspaces >= 0),
    quota_cpu text NOT NULL DEFAULT '',
    quota_memory text NOT NULL DEFAULT '',
    quota_storage text NOT NULL DEFAULT '',
    PRIMARY KEY (namespace, name)
);

CREATE TABLE team_members (
    namespace text NOT NULL,
    team_name text NOT NULL,
    member text NOT NULL CHECK (btrim(member) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, team_name, member),
    FOREIGN KEY (namespace, team_name)
        REFERENCES teams(namespace, name) ON DELETE CASCADE
);

CREATE INDEX team_members_member_idx ON team_members (namespace, lower(member));

CREATE TABLE templates (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    name text NOT NULL
        CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$'),
    display_name text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    -- A template with no access rows is public. Set false when owners, users,
    -- or teams are assigned; the application keeps this field in sync.
    is_public boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE TABLE template_versions (
    namespace text NOT NULL,
    template_name text NOT NULL,
    -- "0" represents an unversioned template.
    version text NOT NULL DEFAULT '0',
    image text NOT NULL CHECK (btrim(image) <> ''),
    parameters jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(parameters) = 'array'),
    links jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(links) = 'array'),
    external_auth jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(external_auth) = 'array'),
    template_yaml text NOT NULL,
    pod_template text NOT NULL,
    checksum text,
    is_active boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, template_name, version),
    FOREIGN KEY (namespace, template_name)
        REFERENCES templates(namespace, name) ON DELETE CASCADE
);

CREATE UNIQUE INDEX template_versions_one_active_idx
    ON template_versions (namespace, template_name)
    WHERE is_active;

CREATE TABLE template_owners (
    namespace text NOT NULL,
    template_name text NOT NULL,
    principal text NOT NULL CHECK (btrim(principal) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, template_name, principal),
    FOREIGN KEY (namespace, template_name)
        REFERENCES templates(namespace, name) ON DELETE CASCADE
);

CREATE INDEX template_owners_principal_idx
    ON template_owners (namespace, lower(principal));

CREATE TABLE template_users (
    namespace text NOT NULL,
    template_name text NOT NULL,
    principal text NOT NULL CHECK (btrim(principal) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, template_name, principal),
    FOREIGN KEY (namespace, template_name)
        REFERENCES templates(namespace, name) ON DELETE CASCADE
);

CREATE INDEX template_users_principal_idx
    ON template_users (namespace, lower(principal));

CREATE TABLE template_teams (
    namespace text NOT NULL,
    template_name text NOT NULL,
    team_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, template_name, team_name),
    FOREIGN KEY (namespace, template_name)
        REFERENCES templates(namespace, name) ON DELETE CASCADE,
    FOREIGN KEY (namespace, team_name)
        REFERENCES teams(namespace, name) ON DELETE CASCADE
);

-- Workspace metadata mirror. rendered_pod is the rendered Kubernetes PodSpec
-- and must not contain external auth tokens or other secrets.
CREATE TABLE workspaces (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    name text NOT NULL
        CHECK (name ~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' AND char_length(name) <= 63),
    owner text NOT NULL CHECK (btrim(owner) <> ''),
    template_name text,
    template_version text,
    image text NOT NULL CHECK (btrim(image) <> ''),
    persistent boolean NOT NULL DEFAULT false,
    storage text NOT NULL DEFAULT '',
    storage_class text NOT NULL DEFAULT '',
    parameters jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(parameters) = 'object'),
    labels jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(labels) = 'object'),
    annotations jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(annotations) = 'object'),
    rendered_pod jsonb NOT NULL
        CHECK (jsonb_typeof(rendered_pod) = 'object'),
    external_auth_env jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(external_auth_env) = 'array'),
    portal_url text NOT NULL DEFAULT '',
    phase text NOT NULL DEFAULT '',
    desired_state text NOT NULL DEFAULT 'running'
        CHECK (desired_state IN ('running', 'stopped')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    stopped_at timestamptz,
    last_seen_at timestamptz,
    PRIMARY KEY (namespace, name)
);

CREATE INDEX workspaces_owner_idx
    ON workspaces (namespace, lower(owner));

CREATE INDEX workspaces_template_idx
    ON workspaces (namespace, template_name);

CREATE INDEX workspaces_phase_idx
    ON workspaces (namespace, phase);

CREATE INDEX workspaces_labels_gin ON workspaces USING gin (labels);

CREATE INDEX workspaces_created_idx
    ON workspaces (namespace, created_at DESC);

-- Reserved for a future encrypted token mirror. The portal currently keeps
-- external auth tokens in a Kubernetes Secret. If token mirroring is added,
-- encrypt values before writing; encryption_key_version identifies the key.
CREATE TABLE external_auth_tokens (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    owner text NOT NULL CHECK (btrim(owner) <> ''),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    access_token_ciphertext bytea NOT NULL,
    refresh_token_ciphertext bytea,
    token_type text NOT NULL DEFAULT '',
    scopes text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz,
    encryption_key_version integer NOT NULL DEFAULT 1
        CHECK (encryption_key_version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, owner, provider)
);

CREATE INDEX external_auth_tokens_expiry_idx
    ON external_auth_tokens (expires_at)
    WHERE expires_at IS NOT NULL;

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    namespace text REFERENCES tenants(namespace) ON DELETE SET NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor text NOT NULL DEFAULT '',
    actor_type text NOT NULL DEFAULT 'user'
        CHECK (actor_type IN ('user', 'token', 'system')),
    action text NOT NULL CHECK (btrim(action) <> ''),
    outcome text NOT NULL DEFAULT 'success'
        CHECK (outcome IN ('success', 'denied', 'error')),
    target_type text NOT NULL DEFAULT '',
    target_name text NOT NULL DEFAULT '',
    target_namespace text,
    workspace_name text NOT NULL DEFAULT '',
    source_ip inet,
    user_agent text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    duration_ms integer CHECK (duration_ms IS NULL OR duration_ms >= 0),
    error text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(details) = 'object')
);

CREATE INDEX audit_events_namespace_time_idx
    ON audit_events (namespace, occurred_at DESC);

CREATE INDEX audit_events_actor_time_idx
    ON audit_events (actor, occurred_at DESC);

CREATE INDEX audit_events_action_time_idx
    ON audit_events (action, occurred_at DESC);

CREATE INDEX audit_events_workspace_time_idx
    ON audit_events (namespace, workspace_name, occurred_at DESC);

CREATE INDEX audit_events_outcome_time_idx
    ON audit_events (outcome, occurred_at DESC);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_set_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER teams_set_updated_at
    BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER templates_set_updated_at
    BEFORE UPDATE ON templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER template_versions_set_updated_at
    BEFORE UPDATE ON template_versions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER workspaces_set_updated_at
    BEFORE UPDATE ON workspaces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER external_auth_tokens_set_updated_at
    BEFORE UPDATE ON external_auth_tokens
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE tenants IS
    'One workspace namespace per tenant. Use one portal release per tenant for hard isolation.';
COMMENT ON TABLE workspaces IS
    'Metadata mirror of Kubernetes workspace pods. Does not contain PVC data or secrets.';
COMMENT ON COLUMN workspaces.template_name IS
    'Template name at creation time. Kept as history if the template is later deleted.';
COMMENT ON COLUMN workspaces.rendered_pod IS
    'Rendered PodSpec. Must not contain external auth tokens or Kubernetes credentials.';
COMMENT ON TABLE external_auth_tokens IS
    'Reserved for a future encrypted OAuth token mirror. Tokens currently stay in Kubernetes Secrets.';
COMMENT ON TABLE audit_events IS
    'Append-only control-plane audit trail for workspace, template, RBAC, SSH, and auth actions.';
COMMENT ON COLUMN audit_events.action IS
    'Dotted action name, for example workspace.create, workspace.terminal.open, ssh.connect, or team.update.';

-- Quotas are owned by the portal quota store. Kubernetes ResourceQuotas and
-- LimitRanges are applied from this data when the portal reconciles a
-- namespace.
--
-- Resource values are stored as text because they are Kubernetes quantities
-- such as "500m", "4Gi", and "10Gi". An empty value or NULL max_workspaces
-- means unlimited.


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
    max_backups integer
        CHECK (max_backups IS NULL OR max_backups >= 0),
    PRIMARY KEY (namespace, identity)
);

CREATE INDEX IF NOT EXISTS user_quotas_identity_idx
    ON user_quotas (namespace, lower(identity));

-- Workspace access state: owner approvals, pending requests, and presence.
--
-- This is mutable, frequently written state (presence heartbeats), so it lives
-- in PostgreSQL instead of a Kubernetes Secret. The namespace column is the
-- workspace namespace and isolates tenants that share one database.
--
-- Grants expire (expires_at) and presence entries are stale after a short TTL
-- (last_seen). The portal prunes expired rows opportunistically.


CREATE TABLE IF NOT EXISTS access_requests (
    namespace text NOT NULL,
    workspace text NOT NULL,
    actor text NOT NULL CHECK (btrim(actor) <> ''),
    requested_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, workspace, actor)
);

CREATE TABLE IF NOT EXISTS access_grants (
    namespace text NOT NULL,
    workspace text NOT NULL,
    actor text NOT NULL CHECK (btrim(actor) <> ''),
    approved_by text NOT NULL DEFAULT '',
    approved_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (namespace, workspace, actor)
);

CREATE INDEX IF NOT EXISTS access_grants_expires_idx
    ON access_grants (namespace, expires_at);

CREATE TABLE IF NOT EXISTS access_presence (
    namespace text NOT NULL,
    workspace text NOT NULL,
    session text NOT NULL CHECK (btrim(session) <> ''),
    actor text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT '',
    since timestamptz NOT NULL DEFAULT now(),
    last_seen timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, workspace, session)
);

CREATE INDEX IF NOT EXISTS access_presence_last_seen_idx
    ON access_presence (namespace, last_seen);

-- Team quotas are owned by the portal quota store. They apply to workspaces
-- created by team members unless a user quota overrides them.
--
-- An empty resource value or NULL max_workspaces means unlimited.


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
    max_backups integer
        CHECK (max_backups IS NULL OR max_backups >= 0),
    PRIMARY KEY (namespace, team)
);

CREATE INDEX IF NOT EXISTS team_quotas_team_idx
    ON team_quotas (namespace, lower(team));

-- PostgreSQL is the authoritative store for RBAC, OIDC users, external auth
-- tokens, and workspace share links when a database is configured. Kubernetes
-- ConfigMaps and Secrets remain as compatibility mirrors or migration sources.
--
-- Share tokens are looked up by SHA-256 hash and stored encrypted at rest. Set
-- PODDY_EXTERNAL_AUTH_KEY (or PODDY_OIDC_SESSION_KEY/PODDY_DESKTOP_SECRET) to a
-- stable value so share links survive restarts.


CREATE TABLE IF NOT EXISTS rbac_documents (
    namespace text PRIMARY KEY REFERENCES tenants(namespace) ON DELETE CASCADE,
    config jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(config) = 'object'),
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER rbac_documents_set_updated_at
    BEFORE UPDATE ON rbac_documents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE rbac_documents IS
    'Authoritative RBAC document when PostgreSQL is configured. The poddy-rbac ConfigMap is a materialized mirror.';

CREATE TABLE IF NOT EXISTS share_links (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    token_ciphertext bytea NOT NULL,
    workspace text NOT NULL CHECK (btrim(workspace) <> ''),
    port integer NOT NULL DEFAULT 0
        CHECK (port >= 0 AND port <= 65535),
    path text NOT NULL DEFAULT '',
    access text NOT NULL CHECK (access IN ('internal', 'public')),
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    encryption_key_version integer NOT NULL DEFAULT 1
        CHECK (encryption_key_version > 0),
    PRIMARY KEY (namespace, token_hash),
    UNIQUE (namespace, workspace, path)
);

CREATE INDEX IF NOT EXISTS share_links_workspace_idx
    ON share_links (namespace, workspace);

CREATE TRIGGER share_links_set_updated_at
    BEFORE UPDATE ON share_links
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE share_links IS
    'Workspace share links. Tokens are hashed for lookup and encrypted for URL reconstruction.';

COMMENT ON COLUMN share_links.token_ciphertext IS
    'AES-GCM encrypted raw share token. Never log or expose this value.';

COMMENT ON COLUMN share_links.encryption_key_version IS
    'Version of the application encryption key used for token_ciphertext.';

CREATE TABLE IF NOT EXISTS metadata_imports (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('external_auth_tokens', 'share_links')),
    imported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, kind)
);

COMMENT ON TABLE metadata_imports IS
    'Tracks one-time imports from Kubernetes Secrets. Prevents deleted tokens or shares from being re-imported after a restart.';

COMMENT ON TABLE external_auth_tokens IS
    'AES-GCM encrypted OAuth2 tokens. Authoritative when PostgreSQL is configured; the Kubernetes Secret is only an import source.';

COMMENT ON TABLE users IS
    'Authoritative OIDC user directory when PostgreSQL is configured; the poddy-users ConfigMap is a materialized mirror.';

-- Environment variables are control-plane metadata resolved at workspace
-- creation and start. Plain values are stored as text; secret values are
-- encrypted with the portal key and never returned by the API.
--
-- Precedence is template < team < user < workspace. When several teams define
-- the same variable, teams are applied in alphabetical order.


CREATE TABLE IF NOT EXISTS env_vars (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('template', 'team', 'user', 'workspace')),
    target text NOT NULL CHECK (btrim(target) <> '' AND char_length(target) <= 253),
    name text NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND char_length(name) <= 255),
    value text,
    value_ciphertext bytea,
    is_secret boolean NOT NULL DEFAULT false,
    encryption_key_version integer NOT NULL DEFAULT 1
        CHECK (encryption_key_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, scope, target, name),
    CHECK (
        (is_secret AND value IS NULL AND value_ciphertext IS NOT NULL)
        OR
        (NOT is_secret AND value IS NOT NULL AND value_ciphertext IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS env_vars_target_idx
    ON env_vars (namespace, scope, target);

CREATE TRIGGER env_vars_set_updated_at
    BEFORE UPDATE ON env_vars
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE env_vars IS
    'Template, team, user, and workspace environment variables. Resolved with workspace > user > team > template precedence.';

COMMENT ON COLUMN env_vars.value IS
    'Plain environment value. NULL for secrets.';

COMMENT ON COLUMN env_vars.value_ciphertext IS
    'AES-GCM encrypted secret environment value. Never expose this column.';

COMMENT ON COLUMN env_vars.encryption_key_version IS
    'Version of the application encryption key used for value_ciphertext.';

-- Per-user dotfiles repositories are control-plane metadata. Workspaces clone
-- the configured repository at startup and copy its top-level entries into
-- /home/dev. A portal-wide fallback is configured with PODDY_DOTFILES_URL.


CREATE TABLE IF NOT EXISTS dotfiles (
    namespace text NOT NULL REFERENCES tenants(namespace) ON DELETE CASCADE,
    identity text NOT NULL
        CHECK (btrim(identity) <> '' AND identity = lower(identity) AND char_length(identity) <= 253),
    url text NOT NULL
        CHECK (btrim(url) <> '' AND char_length(url) <= 2048),
    branch text NOT NULL DEFAULT 'main'
        CHECK (btrim(branch) <> '' AND char_length(branch) <= 255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, identity)
);

CREATE TRIGGER dotfiles_set_updated_at
    BEFORE UPDATE ON dotfiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE dotfiles IS
    'Per-user dotfiles repository URL and branch, applied to workspace homes at startup.';

COMMENT ON COLUMN dotfiles.identity IS
    'Canonical portal owner identity (lowercase email, username, or subject).';
