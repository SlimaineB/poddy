package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"poddy/internal/k8s"
	"poddy/internal/rbac"
)

type accessDecision struct {
	Owner   string
	IsOwner bool
	IsAdmin bool
	Allowed bool
	Pending bool
	Grant   *k8s.AccessGrant
}

type accessActorRequest struct {
	Actor string `json:"actor"`
}

func (s *Server) accessStore() *k8s.AccessStore {
	if s.Workspaces == nil {
		return nil
	}
	return k8s.NewAccessStore(s.Workspaces.Client, s.Workspaces.Namespace, "")
}

func accessActor(actor string) string {
	return strings.ToLower(strings.TrimSpace(actor))
}

func (s *Server) workspaceOwner(ctx context.Context, name string) (string, error) {
	if s.Workspaces == nil {
		return "", apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}
	pod, err := s.Workspaces.Get(ctx, name)
	if err == nil {
		return k8s.Owner(pod), nil
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}
	state, err := s.Workspaces.State(ctx, name)
	if err != nil {
		return "", err
	}
	return state.Owner, nil
}

// interactiveAccess decides whether the caller may open a terminal, an IDE or a
// workspace port. Workspace owners always may. Admins need a time-limited grant
// from the owner. Logs, manifests, stop/start/delete stay plain admin actions.
func (s *Server) interactiveAccess(r *http.Request, name string, id *identity, cfg rbac.Config) (accessDecision, error) {
	decision := accessDecision{}
	if s.Workspaces == nil {
		return decision, apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}
	actor := accessActor(ownerID(id.User))
	decision.IsAdmin = s.isAdmin(id, cfg)
	if id.Method == "none" {
		decision.IsOwner = true
		decision.Allowed = true
		return decision, nil
	}
	owner, err := s.workspaceOwner(r.Context(), name)
	if err != nil {
		return decision, err
	}
	decision.Owner = owner
	if owner != "" && strings.EqualFold(owner, actor) {
		decision.IsOwner = true
		decision.Allowed = true
		return decision, nil
	}
	if !decision.IsAdmin {
		return decision, nil
	}
	if owner == "" {
		// Legacy workspace without an owner annotation: keep admins working.
		decision.Allowed = true
		return decision, nil
	}
	store := s.accessStore()
	if store == nil {
		return decision, apierrors.NewServiceUnavailable("access store is unavailable")
	}
	state, err := store.Load(r.Context())
	if err != nil {
		return decision, err
	}
	now := time.Now()
	if grant, ok := state.Grant(name, actor, now); ok {
		decision.Allowed = true
		decision.Grant = &grant
	}
	decision.Pending = state.HasRequest(name, actor)
	return decision, nil
}

// denyInteractive writes the correct refusal: 404 for ordinary users (do not
// leak workspace existence), 403 with a stable code for admins waiting for the
// owner's approval.
func (s *Server) denyInteractive(w http.ResponseWriter, id *identity, cfg rbac.Config) {
	if s.isAdmin(id, cfg) {
		w.Header().Set("X-Poddy-Access", "required")
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "Owner approval is required to enter this workspace.",
			"code":  "access_required",
		})
		return
	}
	http.Error(w, "workspace not found", http.StatusNotFound)
}

func (s *Server) workspaceAccessInfo(ctx context.Context, name string, id *identity, cfg rbac.Config) (*k8s.WorkspaceAccess, error) {
	owner, err := s.workspaceOwner(ctx, name)
	if err != nil {
		return nil, err
	}
	store := s.accessStore()
	if store == nil {
		return nil, apierrors.NewServiceUnavailable("access store is unavailable")
	}
	state, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return buildWorkspaceAccess(&state, name, owner, ownerID(id.User), s.isAdmin(id, cfg), id.Method, time.Now()), nil
}

func buildWorkspaceAccess(state *k8s.AccessState, name, owner, actor string, admin bool, method string, now time.Time) *k8s.WorkspaceAccess {
	info := &k8s.WorkspaceAccess{
		Owner:    owner,
		IsAdmin:  admin,
		Presence: state.PresenceSummary(name, now),
	}
	if method == "none" {
		info.IsOwner = true
		info.Allowed = true
		return info
	}
	if owner != "" && strings.EqualFold(owner, actor) {
		info.IsOwner = true
		info.Allowed = true
	} else if admin {
		if owner == "" {
			// Legacy workspace without an owner annotation: do not lock out
			// admins who could previously enter.
			info.Allowed = true
		} else if grant, ok := state.Grant(name, actor, now); ok {
			info.Allowed = true
			expires := grant.ExpiresAt
			info.GrantExpiresAt = &expires
		}
		info.Pending = state.HasRequest(name, actor)
	}
	if info.IsOwner || admin {
		info.PendingRequests = state.PendingActors(name)
		info.Grants = state.ActiveGrants(name, now)
	}
	return info
}

func (s *Server) enrichWorkspaceAccess(ctx context.Context, list []k8s.Workspace, id *identity, cfg rbac.Config) {
	store := s.accessStore()
	if store == nil || len(list) == 0 {
		return
	}
	state, err := store.Load(ctx)
	if err != nil {
		log.Printf("workspace access: load: %v", err)
		return
	}
	actor := ownerID(id.User)
	admin := s.isAdmin(id, cfg)
	now := time.Now()
	for i := range list {
		list[i].Access = buildWorkspaceAccess(&state, list[i].Name, list[i].Owner, actor, admin, id.Method, now)
	}
}

func (s *Server) workspaceAccess(w http.ResponseWriter, r *http.Request) {
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	info, err := s.workspaceAccessInfo(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) requestWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.isAdmin(id, cfg) {
		http.Error(w, "only admins can request access", http.StatusForbidden)
		return
	}
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	owner, err := s.workspaceOwner(r.Context(), name)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	actor := ownerID(id.User)
	if owner != "" && strings.EqualFold(owner, actor) {
		http.Error(w, "you own this workspace", http.StatusBadRequest)
		return
	}
	granted, err := s.accessStore().Request(r.Context(), name, actor)
	if err != nil {
		http.Error(w, "access request failed", http.StatusInternalServerError)
		return
	}
	setAuditTarget(r, name)
	setAuditDetail(r, "actor", accessActor(actor))
	status := "pending"
	if granted {
		status = "approved"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": status})
}

func (s *Server) approveWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	s.mutateWorkspaceAccess(w, r, "approve")
}

func (s *Server) denyWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	s.mutateWorkspaceAccess(w, r, "deny")
}

func (s *Server) revokeWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	s.mutateWorkspaceAccess(w, r, "revoke")
}

func (s *Server) mutateWorkspaceAccess(w http.ResponseWriter, r *http.Request, action string) {
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	owner, err := s.workspaceOwner(r.Context(), name)
	if err != nil {
		writeK8sError(w, err, http.StatusNotFound)
		return
	}
	if id.Method != "none" && (owner == "" || !strings.EqualFold(owner, ownerID(id.User))) {
		http.Error(w, "only the workspace owner can manage access", http.StatusForbidden)
		return
	}
	var req accessActorRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	actor := accessActor(req.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	if owner != "" && strings.EqualFold(owner, actor) {
		http.Error(w, "the owner does not need access approval", http.StatusBadRequest)
		return
	}
	store := s.accessStore()
	switch action {
	case "approve":
		err = store.Approve(r.Context(), name, actor, ownerID(id.User), k8s.DefaultAccessGrantTTL)
	case "deny":
		err = store.Deny(r.Context(), name, actor)
	case "revoke":
		err = store.Revoke(r.Context(), name, actor)
	}
	if err != nil {
		http.Error(w, "access update failed", http.StatusInternalServerError)
		return
	}
	setAuditTarget(r, name)
	setAuditDetail(r, "actor", actor)
	info, err := s.workspaceAccessInfo(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// markPresence records HTTP-based interactive access (browser IDE, port proxy).
// Writes are throttled so a chatty IDE does not hammer the Secret.
func (s *Server) markPresence(name, actor, kind string) {
	actor = accessActor(actor)
	if actor == "" || s.Workspaces == nil {
		return
	}
	key := name + "\x00" + actor + "\x00" + kind
	s.presenceMu.Lock()
	if s.presenceLast == nil {
		s.presenceLast = map[string]time.Time{}
	}
	if time.Since(s.presenceLast[key]) < 30*time.Second {
		s.presenceMu.Unlock()
		return
	}
	s.presenceLast[key] = time.Now()
	s.presenceMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now()
	session := "portal-" + kind + "-" + actor
	if err := s.accessStore().SetPresence(ctx, name, session, k8s.Presence{
		Actor:    actor,
		Kind:     kind,
		Since:    now,
		LastSeen: now,
	}); err != nil {
		log.Printf("presence %s/%s: %v", name, actor, err)
	}
}

// trackPresence registers a long-lived session and keeps it fresh until stop is
// called. Used by the terminal websocket.
func (s *Server) trackPresence(name, actor, kind string) func() {
	actor = accessActor(actor)
	if actor == "" || s.Workspaces == nil {
		return func() {}
	}
	session, err := k8s.NewPresenceID()
	if err != nil {
		return func() {}
	}
	store := s.accessStore()
	now := time.Now()
	presence := k8s.Presence{Actor: actor, Kind: kind, Since: now, LastSeen: now}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.SetPresence(ctx, name, session, presence); err != nil {
		log.Printf("presence %s/%s: %v", name, actor, err)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				presence.LastSeen = time.Now()
				hbCtx, hbCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := store.SetPresence(hbCtx, name, session, presence)
				hbCancel()
				if err != nil {
					log.Printf("presence heartbeat %s/%s: %v", name, actor, err)
				}
			}
		}
	}()
	return func() {
		close(done)
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer clearCancel()
		if err := store.ClearPresence(clearCtx, name, session); err != nil {
			log.Printf("presence clear %s/%s: %v", name, actor, err)
		}
	}
}
