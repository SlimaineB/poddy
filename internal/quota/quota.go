// Package quota stores user and team resource budgets. Quotas are mutable
// portal state, so they live in PostgreSQL instead of the RBAC ConfigMap. The
// RBAC document keeps team membership and admin roles only.
//
// The in-memory backend is a single-replica fallback used when no database is
// configured (tests and local development).
package quota

import (
	"context"
	"sort"
	"sync"

	"poddy/internal/rbac"
)

// Store reads and writes quotas for one workspace namespace.
type Store interface {
	UserQuotas(ctx context.Context) (map[string]rbac.Quota, error)
	TeamQuotas(ctx context.Context) (map[string]rbac.Quota, error)
	SetUserQuota(ctx context.Context, identity string, quota rbac.Quota) error
	DeleteUserQuota(ctx context.Context, identity string) error
	SetTeamQuota(ctx context.Context, team string, quota rbac.Quota) error
	DeleteTeamQuota(ctx context.Context, team string) error
}

// Effective resolves the quota of a user from stored overrides and the team
// memberships in the RBAC configuration.
func Effective(ctx context.Context, store Store, cfg rbac.Config, id rbac.Identity) (rbac.Quota, error) {
	users, err := store.UserQuotas(ctx)
	if err != nil {
		return rbac.Quota{}, err
	}
	teams, err := store.TeamQuotas(ctx)
	if err != nil {
		return rbac.Quota{}, err
	}
	return cfg.EffectiveQuotaWith(id, users, teams)
}

// Memory is an in-process Store.
type Memory struct {
	mu    sync.RWMutex
	users map[string]rbac.Quota
	teams map[string]rbac.Quota
}

// NewMemory returns an empty in-memory quota store.
func NewMemory() *Memory {
	return &Memory{users: map[string]rbac.Quota{}, teams: map[string]rbac.Quota{}}
}

func (m *Memory) UserQuotas(context.Context) (map[string]rbac.Quota, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.users), nil
}

func (m *Memory) TeamQuotas(context.Context) (map[string]rbac.Quota, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.teams), nil
}

func (m *Memory) SetUserQuota(_ context.Context, identity string, quota rbac.Quota) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[identity] = quota
	return nil
}

func (m *Memory) DeleteUserQuota(_ context.Context, identity string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, identity)
	return nil
}

func (m *Memory) SetTeamQuota(_ context.Context, team string, quota rbac.Quota) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.teams[team] = quota
	return nil
}

func (m *Memory) DeleteTeamQuota(_ context.Context, team string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.teams, team)
	return nil
}

func clone(source map[string]rbac.Quota) map[string]rbac.Quota {
	out := make(map[string]rbac.Quota, len(source))
	for key, quota := range source {
		out[key] = quota
	}
	return out
}

// SortedIdentities returns the sorted keys of a quota map.
func SortedIdentities(quotas map[string]rbac.Quota) []string {
	keys := make([]string, 0, len(quotas))
	for key := range quotas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
