package userstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore persists OIDC users in PostgreSQL. The Kubernetes ConfigMap is
// kept as a best-effort materialized mirror.
type PostgresStore struct {
	pool      *pgxpool.Pool
	namespace string
	mirror    *Store
	reconcile sync.Once
}

// NewPostgres returns a PostgreSQL-backed user directory. When the users table
// is empty for the namespace, the mirror ConfigMap is imported once.
func NewPostgres(pool *pgxpool.Pool, namespace string, mirror *Store) *PostgresStore {
	return &PostgresStore{pool: pool, namespace: namespace, mirror: mirror}
}

// Load returns every stored user keyed by stable identity.
func (s *PostgresStore) Load(ctx context.Context) (map[string]User, error) {
	users, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]User, len(users))
	for _, user := range users {
		out[key(user)] = user
	}
	return out, nil
}

// List returns users sorted by email, username, then name.
func (s *PostgresStore) List(ctx context.Context) ([]User, error) {
	if err := s.ensureSeeded(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT subject, username, email, display_name, groups, first_seen, last_seen
		FROM poddy.users
		WHERE namespace = $1
		ORDER BY lower(COALESCE(email, '')), lower(COALESCE(username, '')), lower(display_name)`,
		s.namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	s.reconcileMirror(ctx, users)
	return users, nil
}

// Upsert records a login. The first-seen timestamp is preserved.
func (s *PostgresStore) Upsert(ctx context.Context, user User) (User, error) {
	normalizeUser(&user)
	if identityFor(user) == "" {
		return User{}, fmt.Errorf("user has no subject, email or username")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin user update: %w", err)
	}
	defer tx.Rollback(ctx)

	saved, err := s.upsert(ctx, tx, user)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit user update: %w", err)
	}
	if s.mirror != nil {
		if _, err := s.mirror.Upsert(ctx, saved); err != nil {
			log.Printf("users: reconcile ConfigMap %s: %v", DefaultConfigMap, err)
		}
	}
	return saved, nil
}

// Save replaces the complete user directory.
func (s *PostgresStore) Save(ctx context.Context, users map[string]User) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin user save: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := ensureUserTenant(ctx, tx, s.namespace); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM poddy.users WHERE namespace = $1", s.namespace); err != nil {
		return fmt.Errorf("clear users: %w", err)
	}
	for _, user := range users {
		normalizeUser(&user)
		if identityFor(user) == "" {
			continue
		}
		if _, err := s.upsert(ctx, tx, user); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user save: %w", err)
	}
	if s.mirror != nil {
		if err := s.mirror.Save(ctx, users); err != nil {
			log.Printf("users: reconcile ConfigMap %s: %v", DefaultConfigMap, err)
		}
	}
	return nil
}

// Search returns users whose email, username or name contains q.
func (s *PostgresStore) Search(ctx context.Context, q string, limit int) ([]User, error) {
	users, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]User, 0, len(users))
	for _, user := range users {
		if q != "" {
			haystack := strings.ToLower(user.Email + " " + user.Username + " " + user.Name)
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		out = append(out, user)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *PostgresStore) ensureSeeded(ctx context.Context) error {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM poddy.users WHERE namespace = $1)",
		s.namespace,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check users: %w", err)
	}
	if exists || s.mirror == nil {
		return nil
	}

	users, err := s.mirror.Load(ctx)
	if err != nil {
		return fmt.Errorf("load user seed: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin user seed: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := ensureUserTenant(ctx, tx, s.namespace); err != nil {
		return err
	}
	for _, user := range users {
		normalizeUser(&user)
		if identityFor(user) == "" {
			continue
		}
		if _, err := s.upsert(ctx, tx, user); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user seed: %w", err)
	}
	return nil
}

func (s *PostgresStore) upsert(ctx context.Context, tx pgx.Tx, user User) (User, error) {
	if err := ensureUserTenant(ctx, tx, s.namespace); err != nil {
		return User{}, err
	}

	identity := identityFor(user)
	firstSeen := user.FirstSeen.UTC()
	if firstSeen.IsZero() {
		firstSeen = time.Now().UTC()
	}
	lastSeen := time.Now().UTC()
	if !user.LastSeen.IsZero() {
		lastSeen = user.LastSeen.UTC()
	}
	args := []any{
		s.namespace,
		identity,
		nullableText(user.Subject),
		nullableText(user.Username),
		nullableText(strings.ToLower(user.Email)),
		user.Name,
		user.Groups,
		firstSeen,
		lastSeen,
	}

	var saved User
	var subject, username, email pgtype.Text
	var groups []string
	var first, last pgtype.Timestamptz

	if user.Subject != "" {
		var oldIdentity string
		err := tx.QueryRow(ctx,
			"SELECT identity FROM poddy.users WHERE namespace = $1 AND subject = $2 FOR UPDATE",
			s.namespace, user.Subject,
		).Scan(&oldIdentity)
		switch {
		case err == nil && oldIdentity != identity:
			args[1] = identity
			err = tx.QueryRow(ctx, `
				UPDATE poddy.users SET
					identity = $2,
					subject = $3,
					username = $4,
					email = $5,
					display_name = $6,
					groups = COALESCE($7::text[], groups),
					first_seen = COALESCE(first_seen, $8),
					last_seen = $9,
					updated_at = now()
				WHERE namespace = $1 AND identity = $10
				RETURNING subject, username, email, display_name, groups, first_seen, last_seen`,
				append(args, oldIdentity)...,
			).Scan(&subject, &username, &email, &saved.Name, &groups, &first, &last)
			if err != nil {
				return User{}, fmt.Errorf("update user %q: %w", identity, err)
			}
			saved = scanParts(saved, subject, username, email, groups, first, last)
			return saved, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return User{}, fmt.Errorf("find user by subject: %w", err)
		}
	}

	err := tx.QueryRow(ctx, `
		INSERT INTO poddy.users (
			namespace, identity, subject, username, email, display_name,
			groups, first_seen, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7::text[], '{}'::text[]), $8, $9)
		ON CONFLICT (namespace, identity) DO UPDATE SET
			subject = EXCLUDED.subject,
			username = EXCLUDED.username,
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			groups = COALESCE(EXCLUDED.groups, poddy.users.groups),
			first_seen = COALESCE(poddy.users.first_seen, EXCLUDED.first_seen),
			last_seen = EXCLUDED.last_seen,
			updated_at = now()
		RETURNING subject, username, email, display_name, groups, first_seen, last_seen`,
		args...,
	).Scan(&subject, &username, &email, &saved.Name, &groups, &first, &last)
	if err != nil {
		return User{}, fmt.Errorf("upsert user %q: %w", identity, err)
	}
	return scanParts(saved, subject, username, email, groups, first, last), nil
}

func (s *PostgresStore) reconcileMirror(ctx context.Context, users []User) {
	if s.mirror == nil {
		return
	}
	s.reconcile.Do(func() {
		byKey := make(map[string]User, len(users))
		for _, user := range users {
			byKey[key(user)] = user
		}
		if err := s.mirror.Save(ctx, byKey); err != nil {
			log.Printf("users: reconcile ConfigMap %s: %v", DefaultConfigMap, err)
		}
	})
}

func ensureUserTenant(ctx context.Context, tx pgx.Tx, namespace string) error {
	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT (namespace) DO NOTHING",
		namespace,
	); err != nil {
		return fmt.Errorf("ensure tenant %q: %w", namespace, err)
	}
	return nil
}

func normalizeUser(user *User) {
	user.Subject = strings.TrimSpace(user.Subject)
	user.Username = strings.TrimSpace(user.Username)
	user.Email = strings.TrimSpace(user.Email)
	user.Name = strings.TrimSpace(user.Name)
}

func identityFor(user User) string {
	switch {
	case strings.TrimSpace(user.Email) != "":
		return strings.ToLower(strings.TrimSpace(user.Email))
	case strings.TrimSpace(user.Username) != "":
		return strings.ToLower(strings.TrimSpace(user.Username))
	default:
		return strings.TrimSpace(user.Subject)
	}
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	var subject, username, email pgtype.Text
	var groups []string
	var first, last pgtype.Timestamptz
	if err := row.Scan(&subject, &username, &email, &user.Name, &groups, &first, &last); err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return scanParts(user, subject, username, email, groups, first, last), nil
}

func scanParts(user User, subject, username, email pgtype.Text, groups []string, first, last pgtype.Timestamptz) User {
	user.Subject = subject.String
	user.Username = username.String
	user.Email = email.String
	user.Groups = groups
	if first.Valid {
		user.FirstSeen = first.Time
	}
	if last.Valid {
		user.LastSeen = last.Time
	}
	return user
}
