package access

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const pruneInterval = 2 * time.Minute

// Postgres stores access state in the poddy PostgreSQL schema. Presence
// heartbeats update a single row instead of rewriting a whole document.
type Postgres struct {
	pool      *pgxpool.Pool
	namespace string
	lastPrune atomic.Int64
}

// NewPostgres returns a PostgreSQL-backed store for one workspace namespace.
func NewPostgres(pool *pgxpool.Pool, namespace string) *Postgres {
	return &Postgres{pool: pool, namespace: namespace}
}

func (p *Postgres) Load(ctx context.Context) (AccessState, error) {
	state := AccessState{}
	now := time.Now()

	requests, err := p.pool.Query(ctx,
		`SELECT workspace, actor, requested_at FROM poddy.access_requests WHERE namespace = $1`,
		p.namespace)
	if err != nil {
		return AccessState{}, fmt.Errorf("load access requests: %w", err)
	}
	for requests.Next() {
		var workspace string
		var request AccessRequest
		if err := requests.Scan(&workspace, &request.Actor, &request.RequestedAt); err != nil {
			requests.Close()
			return AccessState{}, fmt.Errorf("scan access request: %w", err)
		}
		if state.Requests == nil {
			state.Requests = map[string]map[string]AccessRequest{}
		}
		if state.Requests[workspace] == nil {
			state.Requests[workspace] = map[string]AccessRequest{}
		}
		state.Requests[workspace][request.Actor] = request
	}
	requests.Close()
	if err := requests.Err(); err != nil {
		return AccessState{}, fmt.Errorf("load access requests: %w", err)
	}

	grants, err := p.pool.Query(ctx,
		`SELECT workspace, actor, approved_by, approved_at, expires_at
		 FROM poddy.access_grants WHERE namespace = $1 AND expires_at > $2`,
		p.namespace, now)
	if err != nil {
		return AccessState{}, fmt.Errorf("load access grants: %w", err)
	}
	for grants.Next() {
		var workspace string
		var grant AccessGrant
		if err := grants.Scan(&workspace, &grant.Actor, &grant.ApprovedBy, &grant.ApprovedAt, &grant.ExpiresAt); err != nil {
			grants.Close()
			return AccessState{}, fmt.Errorf("scan access grant: %w", err)
		}
		if state.Grants == nil {
			state.Grants = map[string]map[string]AccessGrant{}
		}
		if state.Grants[workspace] == nil {
			state.Grants[workspace] = map[string]AccessGrant{}
		}
		state.Grants[workspace][grant.Actor] = grant
	}
	grants.Close()
	if err := grants.Err(); err != nil {
		return AccessState{}, fmt.Errorf("load access grants: %w", err)
	}

	presence, err := p.pool.Query(ctx,
		`SELECT workspace, session, actor, kind, since, last_seen
		 FROM poddy.access_presence WHERE namespace = $1 AND last_seen > $2`,
		p.namespace, now.Add(-DefaultPresenceTTL))
	if err != nil {
		return AccessState{}, fmt.Errorf("load access presence: %w", err)
	}
	for presence.Next() {
		var workspace, session string
		var entry Presence
		if err := presence.Scan(&workspace, &session, &entry.Actor, &entry.Kind, &entry.Since, &entry.LastSeen); err != nil {
			presence.Close()
			return AccessState{}, fmt.Errorf("scan access presence: %w", err)
		}
		if state.Presence == nil {
			state.Presence = map[string]map[string]Presence{}
		}
		if state.Presence[workspace] == nil {
			state.Presence[workspace] = map[string]Presence{}
		}
		state.Presence[workspace][session] = entry
	}
	presence.Close()
	if err := presence.Err(); err != nil {
		return AccessState{}, fmt.Errorf("load access presence: %w", err)
	}

	p.maybePrune(ctx)
	return state, nil
}

func (p *Postgres) Request(ctx context.Context, workspace, actor string) (bool, error) {
	actor = normalizeActor(actor)
	now := time.Now()

	var granted bool
	if err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM poddy.access_grants
			WHERE namespace = $1 AND workspace = $2 AND actor = $3 AND expires_at > $4
		)`,
		p.namespace, workspace, actor, now).Scan(&granted); err != nil {
		return false, fmt.Errorf("check access grant: %w", err)
	}
	if granted {
		return true, nil
	}

	if _, err := p.pool.Exec(ctx,
		`INSERT INTO poddy.access_requests (namespace, workspace, actor, requested_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (namespace, workspace, actor)
		 DO UPDATE SET requested_at = EXCLUDED.requested_at`,
		p.namespace, workspace, actor, now); err != nil {
		return false, fmt.Errorf("store access request: %w", err)
	}
	p.maybePrune(ctx)
	return false, nil
}

func (p *Postgres) Approve(ctx context.Context, workspace, actor, approver string, ttl time.Duration) error {
	actor = normalizeActor(actor)
	if ttl <= 0 {
		ttl = DefaultAccessGrantTTL
	}
	now := time.Now()

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("approve access: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO poddy.access_grants (namespace, workspace, actor, approved_by, approved_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (namespace, workspace, actor)
		 DO UPDATE SET approved_by = EXCLUDED.approved_by,
		               approved_at = EXCLUDED.approved_at,
		               expires_at = EXCLUDED.expires_at`,
		p.namespace, workspace, actor, approver, now, now.Add(ttl)); err != nil {
		return fmt.Errorf("store access grant: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM poddy.access_requests WHERE namespace = $1 AND workspace = $2 AND actor = $3`,
		p.namespace, workspace, actor); err != nil {
		return fmt.Errorf("clear access request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("approve access: %w", err)
	}
	return nil
}

func (p *Postgres) Deny(ctx context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	if _, err := p.pool.Exec(ctx,
		`DELETE FROM poddy.access_requests WHERE namespace = $1 AND workspace = $2 AND actor = $3`,
		p.namespace, workspace, actor); err != nil {
		return fmt.Errorf("deny access: %w", err)
	}
	return nil
}

func (p *Postgres) Revoke(ctx context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM poddy.access_grants WHERE namespace = $1 AND workspace = $2 AND actor = $3`,
		p.namespace, workspace, actor); err != nil {
		return fmt.Errorf("revoke access grant: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM poddy.access_requests WHERE namespace = $1 AND workspace = $2 AND actor = $3`,
		p.namespace, workspace, actor); err != nil {
		return fmt.Errorf("clear access request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	return nil
}

func (p *Postgres) SetPresence(ctx context.Context, workspace, session string, presence Presence) error {
	if session == "" {
		return fmt.Errorf("presence session is required")
	}
	presence.Actor = normalizeActor(presence.Actor)
	now := time.Now()
	if presence.Since.IsZero() {
		presence.Since = now
	}
	if presence.LastSeen.IsZero() {
		presence.LastSeen = now
	}
	if _, err := p.pool.Exec(ctx,
		`INSERT INTO poddy.access_presence (namespace, workspace, session, actor, kind, since, last_seen)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (namespace, workspace, session)
		 DO UPDATE SET actor = EXCLUDED.actor,
		               kind = EXCLUDED.kind,
		               last_seen = EXCLUDED.last_seen`,
		p.namespace, workspace, session, presence.Actor, presence.Kind, presence.Since, presence.LastSeen); err != nil {
		return fmt.Errorf("store presence: %w", err)
	}
	p.maybePrune(ctx)
	return nil
}

func (p *Postgres) ClearPresence(ctx context.Context, workspace, session string) error {
	if _, err := p.pool.Exec(ctx,
		`DELETE FROM poddy.access_presence WHERE namespace = $1 AND workspace = $2 AND session = $3`,
		p.namespace, workspace, session); err != nil {
		return fmt.Errorf("clear presence: %w", err)
	}
	return nil
}

func (p *Postgres) DeleteWorkspace(ctx context.Context, workspace string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete workspace access: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, table := range []string{"access_requests", "access_grants", "access_presence"} {
		if _, err := tx.Exec(ctx,
			"DELETE FROM poddy."+table+" WHERE namespace = $1 AND workspace = $2",
			p.namespace, workspace); err != nil {
			return fmt.Errorf("delete workspace %s: %w", table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete workspace access: %w", err)
	}
	return nil
}

// maybePrune removes expired rows at most once per pruneInterval per instance.
func (p *Postgres) maybePrune(ctx context.Context) {
	now := time.Now()
	last := p.lastPrune.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < pruneInterval {
		return
	}
	if !p.lastPrune.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	_, _ = p.pool.Exec(ctx,
		`DELETE FROM poddy.access_grants WHERE namespace = $1 AND expires_at <= $2`,
		p.namespace, now)
	_, _ = p.pool.Exec(ctx,
		`DELETE FROM poddy.access_presence WHERE namespace = $1 AND last_seen <= $2`,
		p.namespace, now.Add(-DefaultPresenceTTL))
}
