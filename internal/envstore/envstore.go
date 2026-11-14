// Package envstore persists environment variables scoped to templates, RBAC
// teams, users, and workspaces. Workspace creation resolves all scopes with
// workspace > user > team > template precedence.
package envstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"poddy/internal/secure"
)

type Scope string

const (
	ScopeTemplate  Scope = "template"
	ScopeTeam      Scope = "team"
	ScopeUser      Scope = "user"
	ScopeWorkspace Scope = "workspace"
)

var (
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reserved = map[string]bool{
		"GIT_ASKPASS":             true,
		"GIT_TERMINAL_PROMPT":     true,
		"PODDY_AGENT_SIDECAR":     true,
		"PODDY_AGENT_TOKEN":       true,
		"PODDY_AGENT_URL":         true,
		"PODDY_EXTERNAL_AUTH_IDS": true,
		"PODDY_PORTAL_URL":        true,
		"PODDY_WORKSPACE_NAME":    true,
		"PODDY_WORKSPACE_TOKEN":   true,
	}
)

// Entry is one stored environment variable. Secret values are never returned;
// Set tells the UI that a value exists.
type Entry struct {
	Scope     Scope     `json:"scope"`
	Target    string    `json:"target"`
	Name      string    `json:"name"`
	Value     string    `json:"value,omitempty"`
	Secret    bool      `json:"secret"`
	Set       bool      `json:"set"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Resolved is the effective environment for one workspace.
type Resolved struct {
	Values  map[string]string
	Secrets map[string]bool
}

// Store manages scoped environment variables and resolves them for a
// workspace.
type Store interface {
	List(ctx context.Context, scope Scope, target string) ([]Entry, error)
	Set(ctx context.Context, scope Scope, target, name, value string, secret bool) error
	Delete(ctx context.Context, scope Scope, target, name string) error
	DeleteTarget(ctx context.Context, scope Scope, target string) error
	Resolve(ctx context.Context, owner string, teams []string, template, workspace string) (Resolved, error)
}

// PostgresStore is the PostgreSQL-backed environment store.
type PostgresStore struct {
	pool      *pgxpool.Pool
	namespace string
	key       []byte
}

func NewPostgres(pool *pgxpool.Pool, namespace, secret string) *PostgresStore {
	store := &PostgresStore{pool: pool, namespace: namespace}
	if strings.TrimSpace(secret) != "" {
		store.key = secure.Key(secret)
	}
	return store
}

func ParseScope(raw string) (Scope, error) {
	scope := Scope(strings.ToLower(strings.TrimSpace(raw)))
	switch scope {
	case ScopeTemplate, ScopeTeam, ScopeUser, ScopeWorkspace:
		return scope, nil
	default:
		return "", fmt.Errorf("invalid environment scope %q", raw)
	}
}

// CanonicalTarget normalizes the target for a scope. User identities are
// case-insensitive; template, team, and workspace names keep their case.
func CanonicalTarget(scope Scope, target string) (string, error) {
	if _, err := ParseScope(string(scope)); err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("environment target is required")
	}
	if len(target) > 253 {
		return "", fmt.Errorf("environment target must be 253 characters or less")
	}
	if scope == ScopeUser {
		target = strings.ToLower(target)
	}
	return target, nil
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("environment variable name is required")
	}
	if len(name) > 255 || !envName.MatchString(name) {
		return fmt.Errorf("invalid environment variable name %q", name)
	}
	if strings.HasPrefix(name, "PODDY_") || strings.HasPrefix(name, "KUBERNETES_") {
		return fmt.Errorf("environment variable name %q is reserved", name)
	}
	if reserved[name] {
		return fmt.Errorf("environment variable name %q is reserved", name)
	}
	return nil
}

func (p *PostgresStore) List(ctx context.Context, scope Scope, target string) ([]Entry, error) {
	target, err := CanonicalTarget(scope, target)
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT scope, target, name, value, value_ciphertext, is_secret, updated_at
		FROM poddy.env_vars
		WHERE namespace = $1 AND scope = $2 AND target = $3
		ORDER BY name`,
		p.namespace, string(scope), target,
	)
	if err != nil {
		return nil, fmt.Errorf("list environment: %w", err)
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var entry Entry
		var value *string
		var ciphertext []byte
		var secret bool
		if err := rows.Scan(&entry.Scope, &entry.Target, &entry.Name, &value, &ciphertext, &secret, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entry.Secret = secret
		entry.Set = true
		if !secret && value != nil {
			entry.Value = *value
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (p *PostgresStore) Set(ctx context.Context, scope Scope, target, name, value string, secret bool) error {
	target, err := CanonicalTarget(scope, target)
	if err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if len(value) > 64*1024 {
		return fmt.Errorf("environment value must be 64 KiB or less")
	}
	name = strings.TrimSpace(name)

	var plain any
	var ciphertext any
	if secret {
		if len(p.key) == 0 {
			return errors.New("environment secrets require PODDY_EXTERNAL_AUTH_KEY, PODDY_OIDC_SESSION_KEY, PODDY_DESKTOP_SECRET, or PODDY_DATABASE_URL")
		}
		encrypted, err := secure.Encrypt(p.key, []byte(value))
		if err != nil {
			return fmt.Errorf("encrypt environment secret: %w", err)
		}
		ciphertext = encrypted
	} else {
		plain = value
	}

	if _, err := p.pool.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT DO NOTHING",
		p.namespace,
	); err != nil {
		return fmt.Errorf("ensure tenant %q: %w", p.namespace, err)
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO poddy.env_vars (
			namespace, scope, target, name, value, value_ciphertext, is_secret
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (namespace, scope, target, name) DO UPDATE SET
			value = EXCLUDED.value,
			value_ciphertext = EXCLUDED.value_ciphertext,
			is_secret = EXCLUDED.is_secret,
			updated_at = now()`,
		p.namespace, string(scope), target, name, plain, ciphertext, secret,
	)
	if err != nil {
		return fmt.Errorf("save environment variable: %w", err)
	}
	return nil
}

func (p *PostgresStore) Delete(ctx context.Context, scope Scope, target, name string) error {
	target, err := CanonicalTarget(scope, target)
	if err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `
		DELETE FROM poddy.env_vars
		WHERE namespace = $1 AND scope = $2 AND target = $3 AND name = $4`,
		p.namespace, string(scope), target, strings.TrimSpace(name),
	); err != nil {
		return fmt.Errorf("delete environment variable: %w", err)
	}
	return nil
}

func (p *PostgresStore) DeleteTarget(ctx context.Context, scope Scope, target string) error {
	target, err := CanonicalTarget(scope, target)
	if err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `
		DELETE FROM poddy.env_vars
		WHERE namespace = $1 AND scope = $2 AND target = $3`,
		p.namespace, string(scope), target,
	); err != nil {
		return fmt.Errorf("delete environment target: %w", err)
	}
	return nil
}

type row struct {
	scope      Scope
	target     string
	name       string
	value      string
	ciphertext []byte
	secret     bool
}

func (p *PostgresStore) Resolve(ctx context.Context, owner string, teams []string, template, workspace string) (Resolved, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	template = strings.TrimSpace(template)
	workspace = strings.TrimSpace(workspace)
	teams = cleanTeams(teams)

	rows, err := p.pool.Query(ctx, `
		SELECT scope, target, name, COALESCE(value, ''), value_ciphertext, is_secret
		FROM poddy.env_vars
		WHERE namespace = $1 AND (
			($2 <> '' AND scope = 'template' AND target = $2)
			OR (cardinality($3::text[]) > 0 AND scope = 'team' AND target = ANY($3))
			OR ($4 <> '' AND scope = 'user' AND target = $4)
			OR ($5 <> '' AND scope = 'workspace' AND target = $5)
		)
		ORDER BY scope, target, name`,
		p.namespace, template, teams, owner, workspace,
	)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve environment: %w", err)
	}
	defer rows.Close()

	var all []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.scope, &item.target, &item.name, &item.value, &item.ciphertext, &item.secret); err != nil {
			return Resolved{}, err
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		return Resolved{}, err
	}
	return p.merge(all, template, teams, owner, workspace)
}

func (p *PostgresStore) merge(rows []row, template string, teams []string, owner, workspace string) (Resolved, error) {
	resolved := Resolved{Values: map[string]string{}, Secrets: map[string]bool{}}
	apply := func(item row) error {
		value := item.value
		if item.secret {
			if len(p.key) == 0 {
				return fmt.Errorf("environment secret %q: no stable encryption key is configured", item.name)
			}
			plaintext, err := secure.Decrypt(p.key, item.ciphertext)
			if err != nil {
				return fmt.Errorf("decrypt environment secret %q: %w", item.name, err)
			}
			value = string(plaintext)
		}
		resolved.Values[item.name] = value
		resolved.Secrets[item.name] = item.secret
		return nil
	}

	for _, item := range rows {
		if item.scope == ScopeTemplate && item.target == template {
			if err := apply(item); err != nil {
				return Resolved{}, err
			}
		}
	}
	for _, team := range teams {
		for _, item := range rows {
			if item.scope == ScopeTeam && item.target == team {
				if err := apply(item); err != nil {
					return Resolved{}, err
				}
			}
		}
	}
	for _, item := range rows {
		if item.scope == ScopeUser && item.target == owner {
			if err := apply(item); err != nil {
				return Resolved{}, err
			}
		}
	}
	for _, item := range rows {
		if item.scope == ScopeWorkspace && item.target == workspace {
			if err := apply(item); err != nil {
				return Resolved{}, err
			}
		}
	}
	return resolved, nil
}

func cleanTeams(teams []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(teams))
	for _, team := range teams {
		team = strings.TrimSpace(team)
		if team == "" || seen[team] {
			continue
		}
		seen[team] = true
		out = append(out, team)
	}
	sort.Strings(out)
	return out
}
