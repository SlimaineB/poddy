// Package access stores workspace access requests, time-limited grants, and
// interactive presence. It is shared by the portal and the SSH bastion so an
// owner approval covers terminal, IDE, port, and SSH access alike.
//
// The state is mutable and written frequently (presence heartbeats), so it does
// not belong in a Kubernetes Secret. The PostgreSQL backend is used when a
// database is configured; the in-memory backend is a single-replica fallback.
package access

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAccessGrantTTL is how long an owner approval lasts.
	DefaultAccessGrantTTL = 30 * time.Minute
	// DefaultPresenceTTL is how long a presence entry is considered fresh.
	DefaultPresenceTTL = 2 * time.Minute
)

// AccessRequest is an admin asking the workspace owner for interactive access.
type AccessRequest struct {
	Actor       string    `json:"actor"`
	RequestedAt time.Time `json:"requestedAt"`
}

// AccessGrant is a time-limited approval from the owner for one admin.
type AccessGrant struct {
	Actor      string    `json:"actor"`
	ApprovedBy string    `json:"approvedBy"`
	ApprovedAt time.Time `json:"approvedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Presence is one active interactive session in a workspace.
type Presence struct {
	Actor    string    `json:"actor"`
	Kind     string    `json:"kind"`
	Since    time.Time `json:"since"`
	LastSeen time.Time `json:"lastSeen"`
}

// AccessState is the access document shared by the portal and the bastion.
type AccessState struct {
	Requests map[string]map[string]AccessRequest `json:"requests,omitempty"`
	Grants   map[string]map[string]AccessGrant   `json:"grants,omitempty"`
	Presence map[string]map[string]Presence      `json:"presence,omitempty"`
}

// PresenceSummary aggregates the sessions of one actor for the UI.
type PresenceSummary struct {
	Actor string    `json:"actor"`
	Kinds []string  `json:"kinds"`
	Since time.Time `json:"since"`
}

// WorkspaceAccess is the access view attached to a workspace for the caller.
type WorkspaceAccess struct {
	Owner           string            `json:"owner,omitempty"`
	IsOwner         bool              `json:"isOwner"`
	IsAdmin         bool              `json:"isAdmin"`
	Allowed         bool              `json:"allowed"`
	Pending         bool              `json:"pending"`
	GrantExpiresAt  *time.Time        `json:"grantExpiresAt,omitempty"`
	Presence        []PresenceSummary `json:"presence,omitempty"`
	PendingRequests []string          `json:"pendingRequests,omitempty"`
	Grants          []AccessGrant     `json:"grants,omitempty"`
}

// Store is the persistence contract for access state.
type Store interface {
	Load(ctx context.Context) (AccessState, error)
	Request(ctx context.Context, workspace, actor string) (bool, error)
	Approve(ctx context.Context, workspace, actor, approver string, ttl time.Duration) error
	Deny(ctx context.Context, workspace, actor string) error
	Revoke(ctx context.Context, workspace, actor string) error
	SetPresence(ctx context.Context, workspace, session string, presence Presence) error
	ClearPresence(ctx context.Context, workspace, session string) error
	DeleteWorkspace(ctx context.Context, workspace string) error
}

// NewPresenceID returns an opaque, unguessable session identifier.
func NewPresenceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate presence id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func normalizeActor(actor string) string {
	return strings.ToLower(strings.TrimSpace(actor))
}

func (state AccessState) Grant(workspace, actor string, now time.Time) (AccessGrant, bool) {
	grant, ok := state.Grants[workspace][normalizeActor(actor)]
	if !ok || !grant.ExpiresAt.After(now) {
		return AccessGrant{}, false
	}
	return grant, true
}

func (state AccessState) HasRequest(workspace, actor string) bool {
	_, ok := state.Requests[workspace][normalizeActor(actor)]
	return ok
}

func (state AccessState) PendingActors(workspace string) []string {
	requests := state.Requests[workspace]
	out := make([]string, 0, len(requests))
	for _, request := range requests {
		out = append(out, request.Actor)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func (state AccessState) ActiveGrants(workspace string, now time.Time) []AccessGrant {
	grants := make([]AccessGrant, 0, len(state.Grants[workspace]))
	for _, grant := range state.Grants[workspace] {
		if grant.ExpiresAt.After(now) {
			grants = append(grants, grant)
		}
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Actor < grants[j].Actor })
	return grants
}

func (state AccessState) PresenceSummary(workspace string, now time.Time) []PresenceSummary {
	byActor := map[string]*PresenceSummary{}
	for _, presence := range state.Presence[workspace] {
		if now.Sub(presence.LastSeen) > DefaultPresenceTTL {
			continue
		}
		actor := strings.TrimSpace(presence.Actor)
		if actor == "" {
			continue
		}
		summary, ok := byActor[actor]
		if !ok {
			summary = &PresenceSummary{Actor: actor, Since: presence.Since}
			byActor[actor] = summary
		}
		if summary.Since.IsZero() || (!presence.Since.IsZero() && presence.Since.Before(summary.Since)) {
			summary.Since = presence.Since
		}
		if presence.Kind == "" {
			continue
		}
		found := false
		for _, kind := range summary.Kinds {
			if kind == presence.Kind {
				found = true
				break
			}
		}
		if !found {
			summary.Kinds = append(summary.Kinds, presence.Kind)
		}
	}
	out := make([]PresenceSummary, 0, len(byActor))
	for _, summary := range byActor {
		sort.Strings(summary.Kinds)
		out = append(out, *summary)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Actor) < strings.ToLower(out[j].Actor) })
	return out
}

// Prune removes expired grants and stale presence entries in place.
func (state *AccessState) Prune(now time.Time) {
	for workspace, grants := range state.Grants {
		for actor, grant := range grants {
			if !grant.ExpiresAt.After(now) {
				delete(grants, actor)
			}
		}
		if len(grants) == 0 {
			delete(state.Grants, workspace)
		}
	}
	for workspace, entries := range state.Presence {
		for session, presence := range entries {
			if now.Sub(presence.LastSeen) > DefaultPresenceTTL {
				delete(entries, session)
			}
		}
		if len(entries) == 0 {
			delete(state.Presence, workspace)
		}
	}
}

func (state AccessState) clone() AccessState {
	out := AccessState{}
	if state.Requests != nil {
		out.Requests = make(map[string]map[string]AccessRequest, len(state.Requests))
		for workspace, requests := range state.Requests {
			cp := make(map[string]AccessRequest, len(requests))
			for actor, request := range requests {
				cp[actor] = request
			}
			out.Requests[workspace] = cp
		}
	}
	if state.Grants != nil {
		out.Grants = make(map[string]map[string]AccessGrant, len(state.Grants))
		for workspace, grants := range state.Grants {
			cp := make(map[string]AccessGrant, len(grants))
			for actor, grant := range grants {
				cp[actor] = grant
			}
			out.Grants[workspace] = cp
		}
	}
	if state.Presence != nil {
		out.Presence = make(map[string]map[string]Presence, len(state.Presence))
		for workspace, entries := range state.Presence {
			cp := make(map[string]Presence, len(entries))
			for session, presence := range entries {
				cp[session] = presence
			}
			out.Presence[workspace] = cp
		}
	}
	return out
}

// Memory is an in-process store for single-replica deployments and tests. It is
// not shared between the portal and the bastion processes.
type Memory struct {
	mu    sync.Mutex
	state AccessState
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{}
}

func (m *Memory) Load(_ context.Context) (AccessState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state.clone()
	state.Prune(time.Now())
	return state, nil
}

func (m *Memory) update(mutate func(*AccessState) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := mutate(&m.state); err != nil {
		return err
	}
	m.state.Prune(time.Now())
	return nil
}

func (m *Memory) Request(_ context.Context, workspace, actor string) (bool, error) {
	actor = normalizeActor(actor)
	granted := false
	err := m.update(func(state *AccessState) error {
		now := time.Now()
		if _, ok := state.Grant(workspace, actor, now); ok {
			granted = true
			return nil
		}
		if state.Requests == nil {
			state.Requests = map[string]map[string]AccessRequest{}
		}
		if state.Requests[workspace] == nil {
			state.Requests[workspace] = map[string]AccessRequest{}
		}
		state.Requests[workspace][actor] = AccessRequest{Actor: actor, RequestedAt: now}
		return nil
	})
	return granted, err
}

func (m *Memory) Approve(_ context.Context, workspace, actor, approver string, ttl time.Duration) error {
	actor = normalizeActor(actor)
	if ttl <= 0 {
		ttl = DefaultAccessGrantTTL
	}
	return m.update(func(state *AccessState) error {
		now := time.Now()
		if state.Grants == nil {
			state.Grants = map[string]map[string]AccessGrant{}
		}
		if state.Grants[workspace] == nil {
			state.Grants[workspace] = map[string]AccessGrant{}
		}
		state.Grants[workspace][actor] = AccessGrant{
			Actor:      actor,
			ApprovedBy: approver,
			ApprovedAt: now,
			ExpiresAt:  now.Add(ttl),
		}
		delete(state.Requests[workspace], actor)
		if len(state.Requests[workspace]) == 0 {
			delete(state.Requests, workspace)
		}
		return nil
	})
}

func (m *Memory) Deny(_ context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	return m.update(func(state *AccessState) error {
		delete(state.Requests[workspace], actor)
		if len(state.Requests[workspace]) == 0 {
			delete(state.Requests, workspace)
		}
		return nil
	})
}

func (m *Memory) Revoke(_ context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	return m.update(func(state *AccessState) error {
		delete(state.Grants[workspace], actor)
		if len(state.Grants[workspace]) == 0 {
			delete(state.Grants, workspace)
		}
		delete(state.Requests[workspace], actor)
		if len(state.Requests[workspace]) == 0 {
			delete(state.Requests, workspace)
		}
		return nil
	})
}

func (m *Memory) SetPresence(_ context.Context, workspace, session string, presence Presence) error {
	if session == "" {
		return fmt.Errorf("presence session is required")
	}
	presence.Actor = normalizeActor(presence.Actor)
	return m.update(func(state *AccessState) error {
		if state.Presence == nil {
			state.Presence = map[string]map[string]Presence{}
		}
		if state.Presence[workspace] == nil {
			state.Presence[workspace] = map[string]Presence{}
		}
		if existing, ok := state.Presence[workspace][session]; ok && !existing.Since.IsZero() {
			presence.Since = existing.Since
		}
		state.Presence[workspace][session] = presence
		return nil
	})
}

func (m *Memory) ClearPresence(_ context.Context, workspace, session string) error {
	return m.update(func(state *AccessState) error {
		delete(state.Presence[workspace], session)
		if len(state.Presence[workspace]) == 0 {
			delete(state.Presence, workspace)
		}
		return nil
	})
}

func (m *Memory) DeleteWorkspace(_ context.Context, workspace string) error {
	return m.update(func(state *AccessState) error {
		delete(state.Requests, workspace)
		delete(state.Grants, workspace)
		delete(state.Presence, workspace)
		return nil
	})
}
