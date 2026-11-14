package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore persists the RBAC document in PostgreSQL. The Kubernetes
// ConfigMap is kept as a best-effort materialized mirror.
type PostgresStore struct {
	pool      *pgxpool.Pool
	namespace string
	mirror    *Store
	mu        sync.Mutex
	reconcile sync.Once
}

// NewPostgres returns a PostgreSQL-backed RBAC store. When the database has no
// document yet, the mirror ConfigMap is imported once.
func NewPostgres(pool *pgxpool.Pool, namespace string, mirror *Store) *PostgresStore {
	return &PostgresStore{pool: pool, namespace: namespace, mirror: mirror}
}

// Load returns the authoritative RBAC document.
func (s *PostgresStore) Load(ctx context.Context) (Config, error) {
	if err := s.ensure(ctx); err != nil {
		return Config{}, err
	}
	cfg, _, err := s.load(ctx)
	if err != nil {
		return Config{}, err
	}
	s.reconcileMirror(ctx, cfg)
	return cfg, nil
}

// Update applies mutate under a row lock and writes the result to PostgreSQL.
func (s *PostgresStore) Update(ctx context.Context, mutate func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("begin rbac update: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, s.namespace); err != nil {
		return Config{}, err
	}

	cfg, found, err := s.loadForUpdate(ctx, tx)
	if err != nil {
		return Config{}, err
	}
	if !found {
		if s.mirror != nil {
			cfg, err = s.mirror.Load(ctx)
			if err != nil {
				return Config{}, fmt.Errorf("load rbac seed: %w", err)
			}
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return Config{}, fmt.Errorf("encode rbac seed: %w", err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO poddy.rbac_documents (namespace, config) VALUES ($1, $2) ON CONFLICT (namespace) DO NOTHING",
			s.namespace, raw,
		); err != nil {
			return Config{}, fmt.Errorf("seed rbac document: %w", err)
		}
		cfg, found, err = s.loadForUpdate(ctx, tx)
		if err != nil {
			return Config{}, err
		}
		if !found {
			return Config{}, fmt.Errorf("rbac document was not created")
		}
	}

	if err := mutate(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return Config{}, fmt.Errorf("encode rbac: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"UPDATE poddy.rbac_documents SET config = $2, revision = revision + 1 WHERE namespace = $1",
		s.namespace, raw,
	); err != nil {
		return Config{}, fmt.Errorf("update rbac document: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Config{}, fmt.Errorf("commit rbac update: %w", err)
	}

	if s.mirror != nil {
		if err := s.mirror.Save(ctx, cfg); err != nil {
			log.Printf("rbac: reconcile ConfigMap %s: %v", DefaultConfigMap, err)
		}
	}
	return cfg, nil
}

// Save replaces the authoritative RBAC document.
func (s *PostgresStore) Save(ctx context.Context, cfg Config) error {
	_, err := s.Update(ctx, func(target *Config) error {
		*target = cfg
		return nil
	})
	return err
}

func (s *PostgresStore) ensure(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		s.namespace,
	); err != nil {
		return fmt.Errorf("ensure rbac tenant: %w", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM poddy.rbac_documents WHERE namespace = $1)",
		s.namespace,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check rbac document: %w", err)
	}
	if exists {
		return nil
	}

	cfg := Config{}
	if s.mirror != nil {
		var err error
		cfg, err = s.mirror.Load(ctx)
		if err != nil {
			return fmt.Errorf("load rbac seed: %w", err)
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode rbac seed: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		"INSERT INTO poddy.rbac_documents (namespace, config) VALUES ($1, $2) ON CONFLICT (namespace) DO NOTHING",
		s.namespace, raw,
	); err != nil {
		return fmt.Errorf("seed rbac document: %w", err)
	}
	return nil
}

func (s *PostgresStore) load(ctx context.Context) (Config, bool, error) {
	return loadConfig(ctx, s.pool.QueryRow(ctx,
		"SELECT config FROM poddy.rbac_documents WHERE namespace = $1",
		s.namespace,
	))
}

func (s *PostgresStore) loadForUpdate(ctx context.Context, tx pgx.Tx) (Config, bool, error) {
	return loadConfig(ctx, tx.QueryRow(ctx,
		"SELECT config FROM poddy.rbac_documents WHERE namespace = $1 FOR UPDATE",
		s.namespace,
	))
}

type rowScanner interface {
	Scan(...any) error
}

func loadConfig(_ context.Context, row rowScanner) (Config, bool, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("load rbac document: %w", err)
	}
	var cfg Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, false, fmt.Errorf("decode rbac document: %w", err)
		}
	}
	return cfg, true, nil
}

func (s *PostgresStore) reconcileMirror(ctx context.Context, cfg Config) {
	if s.mirror == nil {
		return
	}
	s.reconcile.Do(func() {
		if err := s.mirror.Save(ctx, cfg); err != nil {
			log.Printf("rbac: reconcile ConfigMap %s: %v", DefaultConfigMap, err)
		}
	})
}

func ensureTenant(ctx context.Context, tx pgx.Tx, namespace string) error {
	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		namespace,
	); err != nil {
		return fmt.Errorf("ensure tenant %q: %w", namespace, err)
	}
	return nil
}
