package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/yaml"

	"poddy/internal/access"
	"poddy/internal/envstore"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/quota"
	"poddy/internal/rbac"
	"poddy/internal/templates"
	"poddy/internal/userstore"
)

// Postgres stores metadata in the optional poddy PostgreSQL schema.
type Postgres struct {
	pool *pgxpool.Pool
}

// Access returns a PostgreSQL-backed workspace access store that shares this
// connection pool.
func (p *Postgres) Access(namespace string) access.Store {
	return access.NewPostgres(p.pool, namespace)
}

// Quota returns a PostgreSQL-backed quota store that shares this connection
// pool.
func (p *Postgres) Quota(namespace string) quota.Store {
	return quota.NewPostgres(p.pool, namespace)
}

// PGFirstReady reports whether the tables used by the PostgreSQL control plane
// are present, including the one-time import marker used by external auth and
// shares. The portal refuses to start when auto-migration is disabled and the
// schema is incomplete.
func (p *Postgres) PGFirstReady(ctx context.Context) bool {
	var ready bool
	err := p.pool.QueryRow(ctx, `
		SELECT to_regclass('poddy.rbac_documents') IS NOT NULL
			AND to_regclass('poddy.share_links') IS NOT NULL
			AND to_regclass('poddy.users') IS NOT NULL
			AND to_regclass('poddy.external_auth_tokens') IS NOT NULL
			AND to_regclass('poddy.metadata_imports') IS NOT NULL
			AND to_regclass('poddy.user_quotas') IS NOT NULL
			AND to_regclass('poddy.team_quotas') IS NOT NULL
			AND to_regclass('poddy.access_requests') IS NOT NULL
			AND to_regclass('poddy.access_grants') IS NOT NULL
			AND to_regclass('poddy.access_presence') IS NOT NULL
			AND to_regclass('poddy.env_vars') IS NOT NULL`,
	).Scan(&ready)
	return err == nil && ready
}

// RBACStore returns a PostgreSQL-backed RBAC store. mirror is used to import
// the existing ConfigMap and to keep it as a materialized view.
func (p *Postgres) RBACStore(namespace string, mirror *rbac.Store) *rbac.PostgresStore {
	return rbac.NewPostgres(p.pool, namespace, mirror)
}

// UserStore returns a PostgreSQL-backed OIDC user directory.
func (p *Postgres) UserStore(namespace string, mirror *userstore.Store) *userstore.PostgresStore {
	return userstore.NewPostgres(p.pool, namespace, mirror)
}

// ExternalAuthStore returns a PostgreSQL-backed encrypted token store.
func (p *Postgres) ExternalAuthStore(namespace, secret string) *externalauth.PostgresStore {
	return externalauth.NewPostgres(p.pool, namespace, secret)
}

// ShareStore returns a PostgreSQL-backed workspace share store. mirror is only
// used to import existing Kubernetes Secret shares once.
func (p *Postgres) ShareStore(namespace, secret string, mirror *k8s.ShareStore) *ShareStore {
	return NewShareStore(p.pool, namespace, secret, mirror)
}

// EnvStore returns a PostgreSQL-backed scoped environment store.
func (p *Postgres) EnvStore(namespace, secret string) *envstore.PostgresStore {
	return envstore.NewPostgres(p.pool, namespace, secret)
}

// Open connects to PostgreSQL. An empty URL returns the disabled Nop backend.
func Open(ctx context.Context, url string, autoMigrate bool) (Store, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return Nop{}, nil
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if autoMigrate {
		if err := ApplyMigrations(pingCtx, pool); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres migrations: %w", err)
		}
	}
	return &Postgres{pool: pool}, nil
}

func OpenWithRetry(ctx context.Context, url string, autoMigrate bool, timeout time.Duration) (Store, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return Nop{}, nil
	}
	if timeout <= 0 {
		return Open(ctx, url, autoMigrate)
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if remaining := time.Until(deadline); remaining < 10*time.Second {
			cancel()
			attemptCtx, cancel = context.WithDeadline(ctx, deadline)
		}
		store, err := Open(attemptCtx, url, autoMigrate)
		cancel()
		if err == nil {
			return store, nil
		}
		lastErr = err
		if time.Now().Add(2 * time.Second).After(deadline) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

func (p *Postgres) RecordAudit(ctx context.Context, event Event) error {
	if strings.TrimSpace(event.Action) == "" {
		return fmt.Errorf("audit action is required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.ActorType == "" {
		event.ActorType = "user"
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	details, err := jsonObject(event.Details)
	if err != nil {
		return err
	}

	var duration any
	if event.Duration > 0 {
		duration = event.Duration.Milliseconds()
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, event.Namespace); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO poddy.audit_events (
			namespace, occurred_at, actor, actor_type, action, outcome,
			target_type, target_name, target_namespace, workspace_name,
			source_ip, user_agent, request_id, duration_ms, error, details
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11::inet, $12, $13, $14, $15, $16::jsonb
		)`,
		event.Namespace,
		event.OccurredAt,
		event.Actor,
		event.ActorType,
		event.Action,
		event.Outcome,
		event.TargetType,
		event.TargetName,
		nullableString(event.TargetNamespace),
		event.Workspace,
		sourceIP(event.SourceIP),
		event.UserAgent,
		event.RequestID,
		duration,
		event.Error,
		details,
	)
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AuditEnabled() bool { return true }

func (p *Postgres) ListAudit(ctx context.Context, namespace string, filter AuditFilter) (AuditPage, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	perPage := filter.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	if perPage > 100 {
		perPage = 100
	}

	var since, until any
	if !filter.Since.IsZero() {
		since = filter.Since
	}
	if !filter.Until.IsZero() {
		until = filter.Until
	}

	where := `
		WHERE namespace = $1
		  AND ($2 = '' OR action = $2)
		  AND ($3 = '' OR outcome = $3)
		  AND ($4 = '' OR actor = $4)
		  AND ($5 = '' OR workspace_name = $5)
		  AND ($6::timestamptz IS NULL OR occurred_at >= $6)
		  AND ($7::timestamptz IS NULL OR occurred_at < $7)
		  AND ($8 = '' OR action ILIKE '%' || $8 || '%'
		       OR actor ILIKE '%' || $8 || '%'
		       OR target_name ILIKE '%' || $8 || '%'
		       OR workspace_name ILIKE '%' || $8 || '%'
		       OR details::text ILIKE '%' || $8 || '%')`
	args := []any{
		namespace,
		strings.TrimSpace(filter.Action),
		strings.TrimSpace(filter.Outcome),
		strings.TrimSpace(filter.Actor),
		strings.TrimSpace(filter.Workspace),
		since,
		until,
		strings.TrimSpace(filter.Query),
	}

	var total int
	if err := p.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.audit_events"+where, args...).Scan(&total); err != nil {
		return AuditPage{}, fmt.Errorf("count audit events: %w", err)
	}

	rows, err := p.pool.Query(ctx, `
		SELECT id, occurred_at, actor, actor_type, action, outcome,
		       target_type, target_name, COALESCE(target_namespace, ''),
		       workspace_name, COALESCE(host(source_ip), ''),
		       user_agent, request_id, duration_ms, error, details
		FROM poddy.audit_events`+where+`
		ORDER BY occurred_at DESC, id DESC
		LIMIT $9 OFFSET $10`,
		append(args, perPage, (page-1)*perPage)...,
	)
	if err != nil {
		return AuditPage{}, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()

	items := make([]AuditRecord, 0, perPage)
	for rows.Next() {
		var item AuditRecord
		var details []byte
		var duration sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.OccurredAt, &item.Actor, &item.ActorType,
			&item.Action, &item.Outcome, &item.TargetType, &item.TargetName,
			&item.TargetNamespace, &item.Workspace, &item.SourceIP,
			&item.UserAgent, &item.RequestID, &duration, &item.Error,
			&details,
		); err != nil {
			return AuditPage{}, fmt.Errorf("scan audit event: %w", err)
		}
		if duration.Valid {
			value := int(duration.Int64)
			item.DurationMS = &value
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &item.Details); err != nil {
				return AuditPage{}, fmt.Errorf("decode audit details: %w", err)
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, fmt.Errorf("iterate audit events: %w", err)
	}

	pages := 0
	if total > 0 {
		pages = (total + perPage - 1) / perPage
	}
	return AuditPage{Items: items, Total: total, Page: page, PerPage: perPage, Pages: pages}, nil
}

func (p *Postgres) SyncRBAC(ctx context.Context, namespace string, cfg rbac.Config) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, namespace); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM poddy.global_admins WHERE namespace = $1", namespace); err != nil {
		return fmt.Errorf("clear global admins: %w", err)
	}
	for _, admin := range rbac.Normalize(cfg.Admins) {
		if _, err := tx.Exec(ctx,
			"INSERT INTO poddy.global_admins (namespace, identity) VALUES ($1, $2)",
			namespace, admin,
		); err != nil {
			return fmt.Errorf("insert global admin %q: %w", admin, err)
		}
	}

	if _, err := tx.Exec(ctx, "DELETE FROM poddy.teams WHERE namespace = $1", namespace); err != nil {
		return fmt.Errorf("clear teams: %w", err)
	}
	names := make([]string, 0, len(cfg.Teams))
	for name := range cfg.Teams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		team := cfg.Teams[name]
		if _, err := tx.Exec(ctx, `
			INSERT INTO poddy.teams (namespace, name) VALUES ($1, $2)`,
			namespace, name,
		); err != nil {
			return fmt.Errorf("insert team %q: %w", name, err)
		}
		for _, member := range rbac.Normalize(team.Members) {
			if _, err := tx.Exec(ctx,
				"INSERT INTO poddy.team_members (namespace, team_name, member) VALUES ($1, $2, $3)",
				namespace, name, member,
			); err != nil {
				return fmt.Errorf("insert team member %q/%q: %w", name, member, err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SyncTemplates(ctx context.Context, namespace string, catalog *templates.Catalog, cfg rbac.Config) error {
	if catalog == nil {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, namespace); err != nil {
		return err
	}

	snapshot := catalog.Snapshot()
	names := make([]string, 0, len(snapshot))
	for _, template := range snapshot {
		names = append(names, template.Name)
		templateYAML, err := yaml.Marshal(template)
		if err != nil {
			return fmt.Errorf("marshal template %q: %w", template.Name, err)
		}
		parameters, err := jsonArray(template.Parameters)
		if err != nil {
			return fmt.Errorf("template %q parameters: %w", template.Name, err)
		}
		links, err := jsonArray(template.Links)
		if err != nil {
			return fmt.Errorf("template %q links: %w", template.Name, err)
		}
		externalAuth, err := jsonArray(template.ExternalAuth)
		if err != nil {
			return fmt.Errorf("template %q external auth: %w", template.Name, err)
		}
		sum := sha256.Sum256([]byte(string(templateYAML) + "\n" + template.Pod))
		version := template.Version
		if version == "" {
			version = "0"
		}

		access, hasAccess := cfg.Access(template.Name)
		public := !hasAccess || (len(access.Owners) == 0 && len(access.Users) == 0 && len(access.Teams) == 0)
		if hasAccess && containsWildcard(access.Users) {
			public = true
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO poddy.templates (namespace, name, display_name, description, is_public)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (namespace, name) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				description = EXCLUDED.description,
				is_public = EXCLUDED.is_public,
				updated_at = now()`,
			namespace, template.Name, template.DisplayName, template.Description, public,
		); err != nil {
			return fmt.Errorf("upsert template %q: %w", template.Name, err)
		}

		if _, err := tx.Exec(ctx,
			"UPDATE poddy.template_versions SET is_active = false WHERE namespace = $1 AND template_name = $2 AND is_active",
			namespace, template.Name,
		); err != nil {
			return fmt.Errorf("deactivate template %q: %w", template.Name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO poddy.template_versions (
				namespace, template_name, version, image, parameters, links,
				external_auth, template_yaml, pod_template, checksum, is_active
			) VALUES (
				$1, $2, $3, $4, $5::jsonb, $6::jsonb,
				$7::jsonb, $8, $9, $10, true
			)
			ON CONFLICT (namespace, template_name, version) DO UPDATE SET
				image = EXCLUDED.image,
				parameters = EXCLUDED.parameters,
				links = EXCLUDED.links,
				external_auth = EXCLUDED.external_auth,
				template_yaml = EXCLUDED.template_yaml,
				pod_template = EXCLUDED.pod_template,
				checksum = EXCLUDED.checksum,
				is_active = true,
				updated_at = now()`,
			namespace, template.Name, version, template.Image,
			parameters, links, externalAuth,
			string(templateYAML), template.Pod, hex.EncodeToString(sum[:]),
		); err != nil {
			return fmt.Errorf("upsert template version %q/%q: %w", template.Name, version, err)
		}

		if _, err := tx.Exec(ctx, "DELETE FROM poddy.template_owners WHERE namespace = $1 AND template_name = $2", namespace, template.Name); err != nil {
			return fmt.Errorf("clear template owners %q: %w", template.Name, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM poddy.template_users WHERE namespace = $1 AND template_name = $2", namespace, template.Name); err != nil {
			return fmt.Errorf("clear template users %q: %w", template.Name, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM poddy.template_teams WHERE namespace = $1 AND template_name = $2", namespace, template.Name); err != nil {
			return fmt.Errorf("clear template teams %q: %w", template.Name, err)
		}
		if hasAccess {
			for _, owner := range rbac.Normalize(access.Owners) {
				if _, err := tx.Exec(ctx,
					"INSERT INTO poddy.template_owners (namespace, template_name, principal) VALUES ($1, $2, $3)",
					namespace, template.Name, owner,
				); err != nil {
					return fmt.Errorf("insert template owner %q/%q: %w", template.Name, owner, err)
				}
			}
			for _, user := range rbac.Normalize(access.Users) {
				if _, err := tx.Exec(ctx,
					"INSERT INTO poddy.template_users (namespace, template_name, principal) VALUES ($1, $2, $3)",
					namespace, template.Name, user,
				); err != nil {
					return fmt.Errorf("insert template user %q/%q: %w", template.Name, user, err)
				}
			}
			for _, team := range rbac.Normalize(access.Teams) {
				if _, ok := cfg.Teams[team]; !ok {
					continue
				}
				if _, err := tx.Exec(ctx,
					"INSERT INTO poddy.template_teams (namespace, template_name, team_name) VALUES ($1, $2, $3)",
					namespace, template.Name, team,
				); err != nil {
					return fmt.Errorf("insert template team %q/%q: %w", template.Name, team, err)
				}
			}
		}
	}
	if _, err := tx.Exec(ctx,
		"DELETE FROM poddy.templates WHERE namespace = $1 AND NOT (name = ANY($2))",
		namespace, names,
	); err != nil {
		return fmt.Errorf("prune templates: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SyncUser(ctx context.Context, namespace string, user userstore.User) error {
	identity := UserIdentity(user)
	if identity == "" {
		return nil
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, namespace); err != nil {
		return err
	}
	groups := user.Groups
	if groups == nil {
		groups = []string{}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO poddy.users (
			namespace, identity, subject, username, email, display_name,
			groups, first_seen, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (namespace, identity) DO UPDATE SET
			subject = EXCLUDED.subject,
			username = EXCLUDED.username,
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			groups = EXCLUDED.groups,
			first_seen = COALESCE(poddy.users.first_seen, EXCLUDED.first_seen),
			last_seen = EXCLUDED.last_seen,
			updated_at = now()`,
		namespace,
		identity,
		nullableString(user.Subject),
		nullableString(user.Username),
		nullableString(strings.ToLower(user.Email)),
		user.Name,
		groups,
		nullableTime(user.FirstSeen),
		nullableTime(user.LastSeen),
	)
	if err != nil {
		return fmt.Errorf("upsert user %q: %w", identity, err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SyncWorkspace(ctx context.Context, namespace string, snapshot k8s.WorkspaceSnapshot) error {
	if snapshot.Name == "" {
		return fmt.Errorf("workspace name is required")
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = snapshot.CreatedAt
	}
	if snapshot.DesiredState == "" {
		snapshot.DesiredState = "running"
	}
	renderedPod := string(snapshot.RenderedPod)
	if strings.TrimSpace(renderedPod) == "" {
		renderedPod = "{}"
	}
	parameters, err := jsonObject(snapshot.Parameters)
	if err != nil {
		return fmt.Errorf("workspace %q parameters: %w", snapshot.Name, err)
	}
	labels, err := jsonObject(snapshot.Labels)
	if err != nil {
		return fmt.Errorf("workspace %q labels: %w", snapshot.Name, err)
	}
	annotations, err := jsonObject(snapshot.Annotations)
	if err != nil {
		return fmt.Errorf("workspace %q annotations: %w", snapshot.Name, err)
	}
	externalAuthEnv, err := jsonArray(snapshot.ExternalAuthEnv)
	if err != nil {
		return fmt.Errorf("workspace %q external auth env: %w", snapshot.Name, err)
	}

	var lastSeen any
	if snapshot.DesiredState == "running" {
		lastSeen = time.Now().UTC()
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureTenant(ctx, tx, namespace); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO poddy.workspaces (
			namespace, name, owner, template_name, template_version, image,
			persistent, storage, storage_class, parameters, labels, annotations,
			rendered_pod, external_auth_env, portal_url, phase, desired_state,
			created_at, updated_at, stopped_at, last_seen_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb,
			$13::jsonb, $14::jsonb, $15, $16, $17,
			$18, $19, $20, $21
		)
		ON CONFLICT (namespace, name) DO UPDATE SET
			owner = EXCLUDED.owner,
			template_name = EXCLUDED.template_name,
			template_version = EXCLUDED.template_version,
			image = EXCLUDED.image,
			persistent = EXCLUDED.persistent,
			storage = EXCLUDED.storage,
			storage_class = EXCLUDED.storage_class,
			parameters = EXCLUDED.parameters,
			labels = EXCLUDED.labels,
			annotations = EXCLUDED.annotations,
			rendered_pod = EXCLUDED.rendered_pod,
			external_auth_env = EXCLUDED.external_auth_env,
			portal_url = EXCLUDED.portal_url,
			phase = EXCLUDED.phase,
			desired_state = EXCLUDED.desired_state,
			updated_at = EXCLUDED.updated_at,
			stopped_at = EXCLUDED.stopped_at,
			last_seen_at = EXCLUDED.last_seen_at`,
		namespace,
		snapshot.Name,
		snapshot.Owner,
		nullableString(snapshot.Template),
		nullableString(snapshot.TemplateVersion),
		snapshot.Image,
		snapshot.Persistent,
		snapshot.Storage,
		snapshot.StorageClass,
		parameters,
		labels,
		annotations,
		renderedPod,
		externalAuthEnv,
		snapshot.PortalURL,
		snapshot.Phase,
		snapshot.DesiredState,
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
		nullableTime(snapshot.StoppedAt),
		lastSeen,
	)
	if err != nil {
		return fmt.Errorf("upsert workspace %q: %w", snapshot.Name, err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) PruneWorkspaces(ctx context.Context, namespace string, names []string) error {
	if names == nil {
		names = []string{}
	}
	_, err := p.pool.Exec(ctx,
		"DELETE FROM poddy.workspaces WHERE namespace = $1 AND NOT (name = ANY($2))",
		namespace, names,
	)
	if err != nil {
		return fmt.Errorf("prune workspaces: %w", err)
	}
	return nil
}

func (p *Postgres) DeleteWorkspace(ctx context.Context, namespace, name string) error {
	_, err := p.pool.Exec(ctx,
		"DELETE FROM poddy.workspaces WHERE namespace = $1 AND name = $2",
		namespace, name,
	)
	if err != nil {
		return fmt.Errorf("delete workspace %q: %w", name, err)
	}
	return nil
}

func ensureTenant(ctx context.Context, tx pgx.Tx, namespace string) error {
	if strings.TrimSpace(namespace) == "" {
		return fmt.Errorf("namespace is required")
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO poddy.tenants (namespace) VALUES ($1) ON CONFLICT DO NOTHING",
		namespace,
	); err != nil {
		return fmt.Errorf("ensure tenant %q: %w", namespace, err)
	}
	return nil
}

func containsWildcard(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == rbac.Wildcard {
			return true
		}
	}
	return false
}

func jsonObject(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	return string(raw), nil
}

func jsonArray(value any) (string, error) {
	if value == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "[]", nil
	}
	return string(raw), nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func sourceIP(value string) any {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	if value == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if net.ParseIP(value) == nil {
		return nil
	}
	return value
}
