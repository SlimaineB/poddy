package quota

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"poddy/internal/rbac"
)

// Postgres stores quotas in the poddy PostgreSQL schema.
type Postgres struct {
	pool      *pgxpool.Pool
	namespace string
}

// NewPostgres returns a PostgreSQL-backed quota store for one namespace.
func NewPostgres(pool *pgxpool.Pool, namespace string) *Postgres {
	return &Postgres{pool: pool, namespace: namespace}
}

func (p *Postgres) UserQuotas(ctx context.Context) (map[string]rbac.Quota, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT identity, max_workspaces, cpu, memory, storage
		 FROM poddy.user_quotas WHERE namespace = $1`,
		p.namespace)
	if err != nil {
		return nil, fmt.Errorf("load user quotas: %w", err)
	}
	defer rows.Close()
	out := map[string]rbac.Quota{}
	for rows.Next() {
		var identity string
		var max sql.NullInt64
		var quota rbac.Quota
		if err := rows.Scan(&identity, &max, &quota.CPU, &quota.Memory, &quota.Storage); err != nil {
			return nil, fmt.Errorf("scan user quota: %w", err)
		}
		quota.MaxWorkspaces = nullInt(max)
		out[identity] = quota
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load user quotas: %w", err)
	}
	return out, nil
}

func (p *Postgres) TeamQuotas(ctx context.Context) (map[string]rbac.Quota, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT team, max_workspaces, cpu, memory, storage
		 FROM poddy.team_quotas WHERE namespace = $1`,
		p.namespace)
	if err != nil {
		return nil, fmt.Errorf("load team quotas: %w", err)
	}
	defer rows.Close()
	out := map[string]rbac.Quota{}
	for rows.Next() {
		var team string
		var max sql.NullInt64
		var quota rbac.Quota
		if err := rows.Scan(&team, &max, &quota.CPU, &quota.Memory, &quota.Storage); err != nil {
			return nil, fmt.Errorf("scan team quota: %w", err)
		}
		quota.MaxWorkspaces = nullInt(max)
		out[team] = quota
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load team quotas: %w", err)
	}
	return out, nil
}

func (p *Postgres) SetUserQuota(ctx context.Context, identity string, quota rbac.Quota) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := p.ensureTenant(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.user_quotas (
			namespace, identity, max_workspaces, cpu, memory, storage
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (namespace, identity) DO UPDATE SET
			max_workspaces = EXCLUDED.max_workspaces,
			cpu = EXCLUDED.cpu,
			memory = EXCLUDED.memory,
			storage = EXCLUDED.storage,
			updated_at = now()`,
		p.namespace, identity, quota.MaxWorkspaces, quota.CPU, quota.Memory, quota.Storage,
	); err != nil {
		return fmt.Errorf("set user quota %q: %w", identity, err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteUserQuota(ctx context.Context, identity string) error {
	if _, err := p.pool.Exec(ctx,
		"DELETE FROM poddy.user_quotas WHERE namespace = $1 AND identity = $2",
		p.namespace, identity); err != nil {
		return fmt.Errorf("delete user quota %q: %w", identity, err)
	}
	return nil
}

func (p *Postgres) SetTeamQuota(ctx context.Context, team string, quota rbac.Quota) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := p.ensureTenant(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.team_quotas (
			namespace, team, max_workspaces, cpu, memory, storage
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (namespace, team) DO UPDATE SET
			max_workspaces = EXCLUDED.max_workspaces,
			cpu = EXCLUDED.cpu,
			memory = EXCLUDED.memory,
			storage = EXCLUDED.storage,
			updated_at = now()`,
		p.namespace, team, quota.MaxWorkspaces, quota.CPU, quota.Memory, quota.Storage,
	); err != nil {
		return fmt.Errorf("set team quota %q: %w", team, err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteTeamQuota(ctx context.Context, team string) error {
	if _, err := p.pool.Exec(ctx,
		"DELETE FROM poddy.team_quotas WHERE namespace = $1 AND team = $2",
		p.namespace, team); err != nil {
		return fmt.Errorf("delete team quota %q: %w", team, err)
	}
	return nil
}

func (p *Postgres) ensureTenant(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT DO NOTHING",
		p.namespace)
	return err
}

func nullInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	n := int(value.Int64)
	return &n
}
