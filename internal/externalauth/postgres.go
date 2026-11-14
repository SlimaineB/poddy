package externalauth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"poddy/internal/secure"
)

const postgresKeyVersion = 1

// PostgresStore persists encrypted OAuth2 tokens in PostgreSQL.
type PostgresStore struct {
	pool      *pgxpool.Pool
	namespace string
	key       []byte
}

// NewPostgres returns a PostgreSQL-backed token store. secret is the same
// stable application secret used to encrypt the Kubernetes Secret fallback.
func NewPostgres(pool *pgxpool.Pool, namespace, secret string) *PostgresStore {
	return &PostgresStore{
		pool:      pool,
		namespace: namespace,
		key:       secure.Key(secret),
	}
}

// Get returns the stored token for a user and provider.
func (s *PostgresStore) Get(ctx context.Context, user, provider string) (Token, bool, error) {
	var access, refresh []byte
	var tokenType string
	var scopes []string
	var expires pgtype.Timestamptz
	var updated time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT access_token_ciphertext, refresh_token_ciphertext, token_type,
			scopes, expires_at, updated_at
		FROM poddy.external_auth_tokens
		WHERE namespace = $1 AND owner = $2 AND provider = $3`,
		s.namespace, user, provider,
	).Scan(&access, &refresh, &tokenType, &scopes, &expires, &updated)
	if err == pgx.ErrNoRows {
		return Token{}, false, nil
	}
	if err != nil {
		return Token{}, false, fmt.Errorf("get external auth token: %w", err)
	}

	accessToken, err := s.decrypt(access)
	if err != nil {
		return Token{}, false, fmt.Errorf("decrypt access token: %w", err)
	}
	refreshToken, err := s.decrypt(refresh)
	if err != nil {
		return Token{}, false, fmt.Errorf("decrypt refresh token: %w", err)
	}
	token := Token{
		AccessToken:  string(accessToken),
		RefreshToken: string(refreshToken),
		TokenType:    tokenType,
		Scopes:       scopes,
		UpdatedAt:    updated,
	}
	if expires.Valid {
		token.Expiry = expires.Time
	}
	return token, true, nil
}

// Put stores or replaces a token.
func (s *PostgresStore) Put(ctx context.Context, user, provider string, token Token) error {
	access, err := s.encrypt(token.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	refresh, err := s.encrypt(token.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}
	scopes := token.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING`,
		s.namespace,
	); err != nil {
		return fmt.Errorf("ensure external auth tenant: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO poddy.external_auth_tokens (
			namespace, owner, provider, access_token_ciphertext,
			refresh_token_ciphertext, token_type, scopes, expires_at,
			encryption_key_version, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (namespace, owner, provider) DO UPDATE SET
			access_token_ciphertext = EXCLUDED.access_token_ciphertext,
			refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
			token_type = EXCLUDED.token_type,
			scopes = EXCLUDED.scopes,
			expires_at = EXCLUDED.expires_at,
			encryption_key_version = EXCLUDED.encryption_key_version,
			updated_at = now()`,
		s.namespace, user, provider, access, nullableBytes(refresh), token.TokenType,
		scopes, nullableExpiry(token.Expiry), postgresKeyVersion,
	)
	if err != nil {
		return fmt.Errorf("put external auth token: %w", err)
	}
	return nil
}

// Delete removes a user's token for one provider.
func (s *PostgresStore) Delete(ctx context.Context, user, provider string) error {
	if _, err := s.pool.Exec(ctx,
		"DELETE FROM poddy.external_auth_tokens WHERE namespace = $1 AND owner = $2 AND provider = $3",
		s.namespace, user, provider,
	); err != nil {
		return fmt.Errorf("delete external auth token: %w", err)
	}
	return nil
}

// SeedIfEmpty imports Kubernetes Secret tokens once. Existing database values
// always win, and an import marker prevents deleted tokens from reappearing
// after a restart.
func (s *PostgresStore) SeedIfEmpty(ctx context.Context, tokens map[string]map[string]Token) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin external auth seed: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		s.namespace,
	); err != nil {
		return fmt.Errorf("ensure external auth tenant: %w", err)
	}
	var imported bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM poddy.metadata_imports
			WHERE namespace = $1 AND kind = 'external_auth_tokens'
		)`, s.namespace).Scan(&imported); err != nil {
		return fmt.Errorf("check external auth import marker: %w", err)
	}
	if imported {
		return tx.Commit(ctx)
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM poddy.external_auth_tokens WHERE namespace = $1)",
		s.namespace,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check external auth tokens: %w", err)
	}
	if !exists {
		for user, providers := range tokens {
			for provider, token := range providers {
				access, err := s.encrypt(token.AccessToken)
				if err != nil {
					return fmt.Errorf("encrypt access token: %w", err)
				}
				refresh, err := s.encrypt(token.RefreshToken)
				if err != nil {
					return fmt.Errorf("encrypt refresh token: %w", err)
				}
				scopes := token.Scopes
				if scopes == nil {
					scopes = []string{}
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO poddy.external_auth_tokens (
						namespace, owner, provider, access_token_ciphertext,
						refresh_token_ciphertext, token_type, scopes, expires_at,
						encryption_key_version, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
					ON CONFLICT (namespace, owner, provider) DO NOTHING`,
					s.namespace, user, provider, access, nullableBytes(refresh), token.TokenType,
					scopes, nullableExpiry(token.Expiry), postgresKeyVersion,
				); err != nil {
					return fmt.Errorf("seed external auth token: %w", err)
				}
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO poddy.metadata_imports (namespace, kind)
		VALUES ($1, 'external_auth_tokens')
		ON CONFLICT (namespace, kind) DO NOTHING`, s.namespace); err != nil {
		return fmt.Errorf("record external auth import: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit external auth seed: %w", err)
	}
	return nil
}

func (s *PostgresStore) encrypt(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return secure.Encrypt(s.key, []byte(value))
}

func (s *PostgresStore) decrypt(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return secure.Decrypt(s.key, value)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableExpiry(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
