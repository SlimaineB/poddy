package dotfiles

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres stores per-user dotfiles settings in the poddy schema.
type Postgres struct {
	pool      *pgxpool.Pool
	namespace string
}

// NewPostgres returns a PostgreSQL-backed dotfiles store for one namespace.
func NewPostgres(pool *pgxpool.Pool, namespace string) *Postgres {
	return &Postgres{pool: pool, namespace: namespace}
}

func (p *Postgres) Get(ctx context.Context, owner string) (Config, error) {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return Config{}, errors.New("owner is required")
	}
	var cfg Config
	err := p.pool.QueryRow(ctx, `
		SELECT url, branch, updated_at
		FROM poddy.dotfiles
		WHERE namespace = $1 AND identity = $2`,
		p.namespace, owner,
	).Scan(&cfg.URL, &cfg.Branch, &cfg.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("load dotfiles for %q: %w", owner, err)
	}
	return cfg, nil
}

func (p *Postgres) Set(ctx context.Context, owner string, cfg Config) error {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return errors.New("owner is required")
	}
	normalized, err := Normalize(cfg)
	if err != nil {
		return err
	}
	if normalized.Empty() {
		return errors.New("dotfiles repository URL is required")
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin dotfiles update: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT DO NOTHING",
		p.namespace,
	); err != nil {
		return fmt.Errorf("ensure tenant %q: %w", p.namespace, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.dotfiles (namespace, identity, url, branch)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (namespace, identity) DO UPDATE SET
			url = EXCLUDED.url,
			branch = EXCLUDED.branch,
			updated_at = now()`,
		p.namespace, owner, normalized.URL, normalized.Branch,
	); err != nil {
		return fmt.Errorf("save dotfiles for %q: %w", owner, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit dotfiles update: %w", err)
	}
	return nil
}

func (p *Postgres) Delete(ctx context.Context, owner string) error {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return errors.New("owner is required")
	}
	if _, err := p.pool.Exec(ctx, `
		DELETE FROM poddy.dotfiles
		WHERE namespace = $1 AND identity = $2`,
		p.namespace, owner,
	); err != nil {
		return fmt.Errorf("delete dotfiles for %q: %w", owner, err)
	}
	return nil
}
