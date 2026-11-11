package k8s

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const (
	DefaultAccessSecret   = "poddy-access"
	accessDataKey         = "access.json"
	DefaultAccessGrantTTL = 30 * time.Minute
	DefaultPresenceTTL    = 2 * time.Minute
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

// AccessState is the persisted access document. It is shared by the portal and
// the bastion so an approval covers terminal, IDE and SSH access alike.
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

type AccessStore struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

func NewAccessStore(client kubernetes.Interface, namespace, name string) *AccessStore {
	if name == "" {
		name = DefaultAccessSecret
	}
	return &AccessStore{client: client, namespace: namespace, name: name}
}

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

func (s *AccessStore) Load(ctx context.Context) (AccessState, error) {
	state, _, err := s.load(ctx)
	if err != nil {
		return AccessState{}, err
	}
	state.Prune(time.Now())
	return state, nil
}

func (s *AccessStore) load(ctx context.Context) (AccessState, *corev1.Secret, error) {
	state := AccessState{}
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return state, nil, nil
	}
	if err != nil {
		return state, nil, err
	}
	if raw := strings.TrimSpace(string(secret.Data[accessDataKey])); raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return AccessState{}, nil, fmt.Errorf("parse access state: %w", err)
		}
	}
	return state, secret, nil
}

// Update applies mutate to the access document with optimistic concurrency.
func (s *AccessStore) Update(ctx context.Context, mutate func(*AccessState) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		state, secret, err := s.load(ctx)
		if err != nil {
			return err
		}
		if err := mutate(&state); err != nil {
			return err
		}
		state.Prune(time.Now())
		raw, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("marshal access state: %w", err)
		}
		if secret == nil {
			_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: s.name},
				Type:       corev1.SecretTypeOpaque,
				Data:       map[string][]byte{accessDataKey: raw},
			}, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(corev1.Resource("secrets"), s.name, err)
			}
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[accessDataKey] = raw
		_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

// Request records an access request. It returns true when a valid grant already
// exists, in which case no request is stored.
func (s *AccessStore) Request(ctx context.Context, workspace, actor string) (bool, error) {
	actor = normalizeActor(actor)
	granted := false
	err := s.Update(ctx, func(state *AccessState) error {
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

func (s *AccessStore) Approve(ctx context.Context, workspace, actor, approver string, ttl time.Duration) error {
	actor = normalizeActor(actor)
	if ttl <= 0 {
		ttl = DefaultAccessGrantTTL
	}
	return s.Update(ctx, func(state *AccessState) error {
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
		if state.Requests != nil {
			delete(state.Requests[workspace], actor)
			if len(state.Requests[workspace]) == 0 {
				delete(state.Requests, workspace)
			}
		}
		return nil
	})
}

func (s *AccessStore) Deny(ctx context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	return s.Update(ctx, func(state *AccessState) error {
		if state.Requests != nil {
			delete(state.Requests[workspace], actor)
			if len(state.Requests[workspace]) == 0 {
				delete(state.Requests, workspace)
			}
		}
		return nil
	})
}

func (s *AccessStore) Revoke(ctx context.Context, workspace, actor string) error {
	actor = normalizeActor(actor)
	return s.Update(ctx, func(state *AccessState) error {
		if state.Grants != nil {
			delete(state.Grants[workspace], actor)
			if len(state.Grants[workspace]) == 0 {
				delete(state.Grants, workspace)
			}
		}
		if state.Requests != nil {
			delete(state.Requests[workspace], actor)
			if len(state.Requests[workspace]) == 0 {
				delete(state.Requests, workspace)
			}
		}
		return nil
	})
}

func (s *AccessStore) SetPresence(ctx context.Context, workspace, session string, presence Presence) error {
	if session == "" {
		return fmt.Errorf("presence session is required")
	}
	presence.Actor = normalizeActor(presence.Actor)
	return s.Update(ctx, func(state *AccessState) error {
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

func (s *AccessStore) ClearPresence(ctx context.Context, workspace, session string) error {
	return s.Update(ctx, func(state *AccessState) error {
		if state.Presence != nil {
			delete(state.Presence[workspace], session)
			if len(state.Presence[workspace]) == 0 {
				delete(state.Presence, workspace)
			}
		}
		return nil
	})
}

func (s *AccessStore) DeleteWorkspace(ctx context.Context, workspace string) error {
	return s.Update(ctx, func(state *AccessState) error {
		delete(state.Requests, workspace)
		delete(state.Grants, workspace)
		delete(state.Presence, workspace)
		return nil
	})
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
