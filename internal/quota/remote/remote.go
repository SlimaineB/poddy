// Package remote implements the quota store by calling the central control
// plane. Region portals use it so they never connect to the hub database
// directly.
package remote

import (
	"context"
	"net/http"
	"net/url"

	"poddy/internal/rbac"
	"poddy/internal/region"
)

// Store proxies quota operations to the hub.
type Store struct {
	client   *region.Client
	identity region.Identity
}

// New returns a store backed by the given hub client.
func New(client *region.Client, identity region.Identity) *Store {
	return &Store{client: client, identity: identity}
}

func (s *Store) UserQuotas(ctx context.Context) (map[string]rbac.Quota, error) {
	var out map[string]rbac.Quota
	err := s.client.DoJSON(ctx, http.MethodGet, "/api/internal/region/quotas/users", s.identity, nil, &out)
	return out, err
}

func (s *Store) TeamQuotas(ctx context.Context) (map[string]rbac.Quota, error) {
	var out map[string]rbac.Quota
	err := s.client.DoJSON(ctx, http.MethodGet, "/api/internal/region/quotas/teams", s.identity, nil, &out)
	return out, err
}

func (s *Store) SetUserQuota(ctx context.Context, identity string, quota rbac.Quota) error {
	path := "/api/internal/region/quotas/users/" + url.PathEscape(identity)
	return s.client.DoJSON(ctx, http.MethodPut, path, s.identity, quota, nil)
}

func (s *Store) DeleteUserQuota(ctx context.Context, identity string) error {
	path := "/api/internal/region/quotas/users/" + url.PathEscape(identity)
	return s.client.DoJSON(ctx, http.MethodDelete, path, s.identity, nil, nil)
}

func (s *Store) SetTeamQuota(ctx context.Context, team string, quota rbac.Quota) error {
	path := "/api/internal/region/quotas/teams/" + url.PathEscape(team)
	return s.client.DoJSON(ctx, http.MethodPut, path, s.identity, quota, nil)
}

func (s *Store) DeleteTeamQuota(ctx context.Context, team string) error {
	path := "/api/internal/region/quotas/teams/" + url.PathEscape(team)
	return s.client.DoJSON(ctx, http.MethodDelete, path, s.identity, nil, nil)
}
