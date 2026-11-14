-- PostgreSQL is the authoritative store for RBAC, OIDC users, external auth
-- tokens, and workspace share links when a database is configured. Kubernetes
-- ConfigMaps and Secrets remain as compatibility mirrors or migration sources.
--
-- Share tokens are looked up by SHA-256 hash and stored encrypted at rest. Set
-- PODDY_EXTERNAL_AUTH_KEY (or PODDY_OIDC_SESSION_KEY/PODDY_DESKTOP_SECRET) to a
-- stable value so share links survive restarts.

SET LOCAL search_path TO poddy, public;

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

INSERT INTO schema_migrations (version) VALUES ('0005_pg_first_state');
