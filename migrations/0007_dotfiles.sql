-- Per-user dotfiles repositories are control-plane metadata. Workspaces clone
-- the configured repository at startup and copy its top-level entries into
-- /home/dev. A portal-wide fallback is configured with PODDY_DOTFILES_URL.

SET LOCAL search_path TO poddy, public;

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

INSERT INTO schema_migrations (version) VALUES ('0007_dotfiles');
