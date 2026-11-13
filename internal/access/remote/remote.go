// Package remote implements the access store by calling the central control
// plane. Region portals and bastions use it so they never connect to the hub
// database directly.
package remote

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"poddy/internal/access"
	"poddy/internal/region"
)

// Store proxies access operations to the hub.
type Store struct {
	client   *region.Client
	identity region.Identity
}

// New returns a store backed by the given hub client.
func New(client *region.Client, identity region.Identity) *Store {
	return &Store{client: client, identity: identity}
}

func (s *Store) Load(ctx context.Context) (access.AccessState, error) {
	var state access.AccessState
	err := s.client.DoJSON(ctx, http.MethodGet, "/api/internal/region/access", s.identity, nil, &state)
	return state, err
}

func (s *Store) Request(ctx context.Context, workspace, actor string) (bool, error) {
	var out struct {
		Granted bool `json:"granted"`
	}
	err := s.client.DoJSON(ctx, http.MethodPost, accessPath(workspace, "request"), s.identity, map[string]string{"actor": actor}, &out)
	return out.Granted, err
}

func (s *Store) Approve(ctx context.Context, workspace, actor, approver string, ttl time.Duration) error {
	body := struct {
		Actor      string `json:"actor"`
		Approver   string `json:"approver"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{Actor: actor, Approver: approver, TTLSeconds: int(ttl / time.Second)}
	return s.client.DoJSON(ctx, http.MethodPost, accessPath(workspace, "approve"), s.identity, body, nil)
}

func (s *Store) Deny(ctx context.Context, workspace, actor string) error {
	return s.client.DoJSON(ctx, http.MethodPost, accessPath(workspace, "deny"), s.identity, map[string]string{"actor": actor}, nil)
}

func (s *Store) Revoke(ctx context.Context, workspace, actor string) error {
	return s.client.DoJSON(ctx, http.MethodPost, accessPath(workspace, "revoke"), s.identity, map[string]string{"actor": actor}, nil)
}

func (s *Store) SetPresence(ctx context.Context, workspace, session string, presence access.Presence) error {
	path := accessPath(workspace, "presence/"+url.PathEscape(session))
	return s.client.DoJSON(ctx, http.MethodPut, path, s.identity, presence, nil)
}

func (s *Store) ClearPresence(ctx context.Context, workspace, session string) error {
	path := accessPath(workspace, "presence/"+url.PathEscape(session))
	return s.client.DoJSON(ctx, http.MethodDelete, path, s.identity, nil, nil)
}

func (s *Store) DeleteWorkspace(ctx context.Context, workspace string) error {
	path := "/api/internal/region/access/" + url.PathEscape(workspace)
	return s.client.DoJSON(ctx, http.MethodDelete, path, s.identity, nil, nil)
}

func accessPath(workspace, suffix string) string {
	return "/api/internal/region/access/" + url.PathEscape(workspace) + "/" + suffix
}
