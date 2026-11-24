-- Durable workspace shares between owners and other users.
SET LOCAL search_path TO poddy, public;

CREATE TABLE IF NOT EXISTS access_shares (
    namespace text NOT NULL,
    workspace text NOT NULL,
    actor text NOT NULL CHECK (btrim(actor) <> ''),
    shared_by text NOT NULL DEFAULT '',
    shared_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, workspace, actor)
);

CREATE INDEX IF NOT EXISTS access_shares_actor_idx
    ON access_shares (namespace, lower(actor));
