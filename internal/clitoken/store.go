package clitoken

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store tracks revoked CLI tokens and consumed one-time login codes.
type Store interface {
	Revoke(ctx context.Context, id string, expires time.Time) error
	Revoked(ctx context.Context, id string) (bool, error)
	ConsumeCode(ctx context.Context, id string, expires time.Time) (bool, error)
}

// Memory is a process-local store for deployments without PostgreSQL.
type Memory struct {
	mu       sync.Mutex
	revoked  map[string]time.Time
	consumed map[string]time.Time
}

func NewMemory() *Memory {
	return &Memory{
		revoked:  map[string]time.Time{},
		consumed: map[string]time.Time{},
	}
}

func (m *Memory) Revoke(_ context.Context, id string, expires time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("CLI token id is required")
	}
	if expires.IsZero() {
		return errors.New("CLI token expiry is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purge(time.Now())
	m.revoked[id] = expires
	return nil
}

func (m *Memory) Revoked(_ context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purge(time.Now())
	_, ok := m.revoked[id]
	return ok, nil
}

func (m *Memory) ConsumeCode(_ context.Context, id string, expires time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("CLI code id is required")
	}
	if expires.IsZero() {
		return false, errors.New("CLI code expiry is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.purge(now)
	if existing, ok := m.consumed[id]; ok && existing.After(now) {
		return false, nil
	}
	m.consumed[id] = expires
	return true, nil
}

func (m *Memory) purge(now time.Time) {
	for id, expires := range m.revoked {
		if !expires.After(now) {
			delete(m.revoked, id)
		}
	}
	for id, expires := range m.consumed {
		if !expires.After(now) {
			delete(m.consumed, id)
		}
	}
}

// Postgres shares the portal control-plane connection pool.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) Revoke(ctx context.Context, id string, expires time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("CLI token id is required")
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO poddy.cli_revocations (token_id, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (token_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		id, expires.UTC(),
	)
	return err
}

func (p *Postgres) Revoked(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	var revoked bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM poddy.cli_revocations
			WHERE token_id = $1 AND expires_at > now()
		)`, id,
	).Scan(&revoked)
	return revoked, err
}

func (p *Postgres) ConsumeCode(ctx context.Context, id string, expires time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("CLI code id is required")
	}
	_, _ = p.pool.Exec(ctx, "DELETE FROM poddy.cli_used_codes WHERE expires_at < now()")
	tag, err := p.pool.Exec(ctx, `
		INSERT INTO poddy.cli_used_codes (code_id, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (code_id) DO NOTHING`,
		id, expires.UTC(),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
