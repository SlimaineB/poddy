-- Environment variables are control-plane metadata resolved at workspace
-- creation and start. Plain values are stored as text; secret values are
-- encrypted with the portal key and never returned by the API.
--
-- Precedence is template < team < user < workspace. When several teams define
-- the same variable, teams are applied in alphabetical order.

SET LOCAL search_path TO poddy, public;

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

INSERT INTO schema_migrations (version) VALUES ('0006_env_vars');
