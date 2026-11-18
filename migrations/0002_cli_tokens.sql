-- Revoked CLI tokens and consumed one-time CLI login codes.
SET LOCAL search_path TO poddy, public;

CREATE TABLE IF NOT EXISTS cli_revocations (
    token_id text PRIMARY KEY CHECK (btrim(token_id) <> ''),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cli_revocations_expires_idx
    ON cli_revocations (expires_at);

CREATE TABLE IF NOT EXISTS cli_used_codes (
    code_id text PRIMARY KEY CHECK (btrim(code_id) <> ''),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cli_used_codes_expires_idx
    ON cli_used_codes (expires_at);
