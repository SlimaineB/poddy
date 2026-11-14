package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"poddy/internal/k8s"
	"poddy/internal/secure"
)

// ShareStore persists workspace share links in PostgreSQL. Tokens are looked
// up by hash and encrypted at rest so the raw link can be reconstructed.
type ShareStore struct {
	pool      *pgxpool.Pool
	namespace string
	key       []byte
	mirror    *k8s.ShareStore
	seedMu    sync.Mutex
	seeded    bool
}

// NewShareStore returns a PostgreSQL-backed share store. The Kubernetes Secret
// is only used to import pre-upgrade links when the table is empty.
func NewShareStore(pool *pgxpool.Pool, namespace, secret string, mirror *k8s.ShareStore) *ShareStore {
	return &ShareStore{
		pool:      pool,
		namespace: namespace,
		key:       secure.Key(secret),
		mirror:    mirror,
	}
}

// Load returns all share links keyed by raw token.
func (s *ShareStore) Load(ctx context.Context) (map[string]k8s.Share, error) {
	if err := s.ensureSeeded(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT token_ciphertext, workspace, port, path, access, created_by,
			created_at, updated_at
		FROM poddy.share_links
		WHERE namespace = $1`,
		s.namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()

	shares := map[string]k8s.Share{}
	for rows.Next() {
		share, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		shares[share.Token] = share
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	return shares, nil
}

// GetByToken returns a share from its raw token.
func (s *ShareStore) GetByToken(ctx context.Context, token string) (k8s.Share, bool, error) {
	if token == "" {
		return k8s.Share{}, false, nil
	}
	if err := s.ensureSeeded(ctx); err != nil {
		return k8s.Share{}, false, err
	}
	share, err := s.scan(s.pool.QueryRow(ctx, `
		SELECT token_ciphertext, workspace, port, path, access, created_by,
			created_at, updated_at
		FROM poddy.share_links
		WHERE namespace = $1 AND token_hash = $2`,
		s.namespace, shareTokenHash(token),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return k8s.Share{}, false, nil
	}
	if err != nil {
		return k8s.Share{}, false, err
	}
	return share, true, nil
}

// GetByTarget returns the share for a workspace path.
func (s *ShareStore) GetByTarget(ctx context.Context, workspace, path string) (k8s.Share, bool, error) {
	if err := s.ensureSeeded(ctx); err != nil {
		return k8s.Share{}, false, err
	}
	share, err := s.scan(s.pool.QueryRow(ctx, `
		SELECT token_ciphertext, workspace, port, path, access, created_by,
			created_at, updated_at
		FROM poddy.share_links
		WHERE namespace = $1 AND workspace = $2 AND path = $3`,
		s.namespace, workspace, path,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return k8s.Share{}, false, nil
	}
	if err != nil {
		return k8s.Share{}, false, err
	}
	return share, true, nil
}

// Set stores or replaces a share link.
func (s *ShareStore) Set(ctx context.Context, share k8s.Share) error {
	if err := s.ensureSeeded(ctx); err != nil {
		return err
	}
	if share.Token == "" {
		return fmt.Errorf("share token is required")
	}
	if share.CreatedAt.IsZero() {
		share.CreatedAt = time.Now().UTC()
	}
	if share.UpdatedAt.IsZero() {
		share.UpdatedAt = time.Now().UTC()
	}

	ciphertext, err := secure.Encrypt(s.key, []byte(share.Token))
	if err != nil {
		return fmt.Errorf("encrypt share token: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin share update: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		s.namespace,
	); err != nil {
		return fmt.Errorf("ensure share tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM poddy.share_links
		WHERE namespace = $1
			AND (token_hash = $2 OR (workspace = $3 AND path = $4))`,
		s.namespace, shareTokenHash(share.Token), share.Workspace, share.Path,
	); err != nil {
		return fmt.Errorf("replace share: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.share_links (
			namespace, token_hash, token_ciphertext, workspace, port, path,
			access, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (namespace, workspace, path) DO UPDATE SET
			token_hash = EXCLUDED.token_hash,
			token_ciphertext = EXCLUDED.token_ciphertext,
			port = EXCLUDED.port,
			access = EXCLUDED.access,
			created_by = EXCLUDED.created_by,
			updated_at = EXCLUDED.updated_at`,
		s.namespace, shareTokenHash(share.Token), ciphertext, share.Workspace,
		share.Port, share.Path, share.Access, share.CreatedBy,
		share.CreatedAt.UTC(), share.UpdatedAt.UTC(),
	); err != nil {
		return fmt.Errorf("save share: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit share update: %w", err)
	}
	return nil
}

// DeleteTarget removes the share for a workspace path.
func (s *ShareStore) DeleteTarget(ctx context.Context, workspace, path string) error {
	if err := s.ensureSeeded(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx,
		"DELETE FROM poddy.share_links WHERE namespace = $1 AND workspace = $2 AND path = $3",
		s.namespace, workspace, path,
	); err != nil {
		return fmt.Errorf("delete share target: %w", err)
	}
	return nil
}

// DeleteWorkspace removes every share for a workspace.
func (s *ShareStore) DeleteWorkspace(ctx context.Context, workspace string) error {
	if err := s.ensureSeeded(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx,
		"DELETE FROM poddy.share_links WHERE namespace = $1 AND workspace = $2",
		s.namespace, workspace,
	); err != nil {
		return fmt.Errorf("delete workspace shares: %w", err)
	}
	return nil
}

func (s *ShareStore) ensureSeeded(ctx context.Context) error {
	s.seedMu.Lock()
	defer s.seedMu.Unlock()
	if s.seeded {
		return nil
	}

	var imported bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM poddy.metadata_imports
			WHERE namespace = $1 AND kind = 'share_links'
		)`, s.namespace).Scan(&imported); err != nil {
		return fmt.Errorf("check share import marker: %w", err)
	}
	if imported {
		s.seeded = true
		return nil
	}

	var exists bool
	if err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM poddy.share_links WHERE namespace = $1)",
		s.namespace,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check share links: %w", err)
	}
	var shares map[string]k8s.Share
	if !exists && s.mirror != nil {
		var err error
		shares, err = s.mirror.Load(ctx)
		if err != nil {
			return fmt.Errorf("load share seed: %w", err)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin share seed: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		s.namespace,
	); err != nil {
		return fmt.Errorf("ensure share tenant: %w", err)
	}
	if !exists {
		for token, share := range shares {
			share.Token = token
			if err := s.insertSeed(ctx, tx, share); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.metadata_imports (namespace, kind)
		VALUES ($1, 'share_links')
		ON CONFLICT (namespace, kind) DO NOTHING`, s.namespace); err != nil {
		return fmt.Errorf("record share import: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit share seed: %w", err)
	}
	s.seeded = true
	return nil
}

func (s *ShareStore) insertSeed(ctx context.Context, tx pgx.Tx, share k8s.Share) error {
	ciphertext, err := secure.Encrypt(s.key, []byte(share.Token))
	if err != nil {
		return fmt.Errorf("encrypt share token: %w", err)
	}
	createdAt := share.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := share.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.share_links (
			namespace, token_hash, token_ciphertext, workspace, port, path,
			access, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (namespace, workspace, path) DO NOTHING`,
		s.namespace, shareTokenHash(share.Token), ciphertext, share.Workspace,
		share.Port, share.Path, share.Access, share.CreatedBy,
		createdAt.UTC(), updatedAt.UTC(),
	); err != nil {
		return fmt.Errorf("seed share: %w", err)
	}
	return nil
}

func (s *ShareStore) scan(row pgx.Row) (k8s.Share, error) {
	var share k8s.Share
	var ciphertext []byte
	if err := row.Scan(
		&ciphertext, &share.Workspace, &share.Port, &share.Path, &share.Access,
		&share.CreatedBy, &share.CreatedAt, &share.UpdatedAt,
	); err != nil {
		return k8s.Share{}, err
	}
	token, err := secure.Decrypt(s.key, ciphertext)
	if err != nil {
		return k8s.Share{}, fmt.Errorf("decrypt share token: %w", err)
	}
	share.Token = string(token)
	return share, nil
}

func shareTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
