-- Workspace access state: owner approvals, pending requests, and presence.
--
-- This is mutable, frequently written state (presence heartbeats), so it lives
-- in PostgreSQL instead of a Kubernetes Secret. The namespace column is the
-- workspace namespace and isolates tenants that share one database.
--
-- Grants expire (expires_at) and presence entries are stale after a short TTL
-- (last_seen). The portal prunes expired rows opportunistically.

SET LOCAL search_path TO poddy, public;

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
