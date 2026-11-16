-- Per-workspace backup retention limit, enforced by the portal when a volume
-- backup is created. NULL means unlimited.

SET LOCAL search_path TO poddy, public;

ALTER TABLE user_quotas
    ADD COLUMN IF NOT EXISTS max_backups integer
        CHECK (max_backups IS NULL OR max_backups >= 0);

ALTER TABLE team_quotas
    ADD COLUMN IF NOT EXISTS max_backups integer
        CHECK (max_backups IS NULL OR max_backups >= 0);
