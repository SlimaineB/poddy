package server

import (
	"encoding/json"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"poddy/internal/auth"
	"poddy/internal/k8s"
	"poddy/internal/rbac"
	"poddy/internal/templates"
)

type templateInfo struct {
	templates.Info
	CanManage bool `json:"canManage"`
	Owned     bool `json:"owned"`
}

func rbacIdentity(user auth.User) rbac.Identity {
	return rbac.Identity{
		Username: user.Username,
		Email:    user.Email,
		Groups:   user.Groups,
	}
}

func ownerID(user auth.User) string {
	switch {
	case user.Email != "":
		return user.Email
	case user.Username != "":
		return user.Username
	default:
		return user.Subject
	}
}

// currentIdentity returns the request identity. When authentication is not
// configured at all the portal runs in trusted local mode and acts as an admin.
func (s *Server) currentIdentity(r *http.Request) (*identity, bool) {
	if id, ok := s.identify(r); ok {
		return id, true
	}
	if s.RegionMode {
		return nil, false
	}
	if s.Token == "" && s.OIDC == nil {
		return &identity{Method: "none", User: auth.User{Username: "local"}}, true
	}
	return nil, false
}

func (s *Server) loadRBAC(r *http.Request) (rbac.Config, error) {
	if s.RBAC == nil {
		return rbac.Config{}, nil
	}
	return s.RBAC.Load(r.Context())
}

func (s *Server) isAdmin(id *identity, cfg rbac.Config) bool {
	if id.Method == "region" {
		return id.Admin
	}
	if id.Method == "none" {
		return true
	}
	if id.Method == "token" {
		return s.TokenAdmin
	}
	if s.RBAC == nil {
		return false
	}
	return cfg.IsAdmin(rbacIdentity(id.User))
}

func (s *Server) isAuditor(id *identity, cfg rbac.Config) bool {
	if id.Method == "region" {
		return id.Admin
	}
	if id.Method == "none" {
		return true
	}
	if id.Method == "token" {
		return s.TokenAdmin
	}
	if s.RBAC == nil {
		return false
	}
	return cfg.IsAuditor(rbacIdentity(id.User))
}

func (s *Server) canViewAudit(id *identity, cfg rbac.Config) bool {
	return s.isAdmin(id, cfg) || s.isAuditor(id, cfg)
}

func (s *Server) workspaceAllowed(r *http.Request, name string, id *identity, cfg rbac.Config) (bool, error) {
	if s.isAdmin(id, cfg) {
		return true, nil
	}
	pod, err := s.Workspaces.Get(r.Context(), name)
	if err != nil {
		return false, err
	}
	return k8s.Owner(pod) == ownerID(id.User), nil
}

// workspaceAllowedAny checks ownership for running pods and stopped workspaces.
func (s *Server) workspaceAllowedAny(r *http.Request, name string, id *identity, cfg rbac.Config) (bool, error) {
	if s.isAdmin(id, cfg) {
		return true, nil
	}
	pod, err := s.Workspaces.Get(r.Context(), name)
	if err == nil {
		return k8s.Owner(pod) == ownerID(id.User), nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	state, err := s.Workspaces.State(r.Context(), name)
	if err != nil {
		return false, err
	}
	return state.Owner == ownerID(id.User), nil
}

func (s *Server) templateAccess(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if s.Workspaces.Templates == nil {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}
	if _, err := s.Workspaces.Templates.Get(name); err != nil {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}

	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	admin := s.isAdmin(id, cfg)
	if !admin && !cfg.IsOwner(name, rbacIdentity(id.User)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	access, _ := cfg.Access(name)
	writeJSON(w, http.StatusOK, map[string]any{
		"template":      name,
		"owners":        nonNil(access.Owners),
		"users":         nonNil(access.Users),
		"teams":         nonNil(access.Teams),
		"canEditOwners": admin,
	})
}

func (s *Server) updateTemplateAccess(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if s.Workspaces.Templates == nil {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}
	if _, err := s.Workspaces.Templates.Get(name); err != nil {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}

	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	admin := s.isAdmin(id, cfg)
	if !admin && !cfg.IsOwner(name, rbacIdentity(id.User)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Owners []string `json:"owners"`
		Users  []string `json:"users"`
		Teams  []string `json:"teams"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	access, _ := cfg.Access(name)
	if !admin {
		req.Owners = access.Owners
	}
	owners := rbac.Normalize(req.Owners)
	users := rbac.Normalize(req.Users)
	teams := rbac.Normalize(req.Teams)
	for _, team := range teams {
		if _, ok := cfg.Teams[team]; !ok {
			http.Error(w, "unknown team: "+team, http.StatusBadRequest)
			return
		}
	}

	updated, err := s.RBAC.Update(r.Context(), func(cfg *rbac.Config) error {
		if !admin {
			access, _ := cfg.Access(name)
			owners = access.Owners
		}
		if len(owners) == 0 && len(users) == 0 && len(teams) == 0 {
			delete(cfg.Templates, name)
			return nil
		}
		if cfg.Templates == nil {
			cfg.Templates = map[string]rbac.Access{}
		}
		cfg.Templates[name] = rbac.Access{Owners: owners, Users: users, Teams: teams}
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	s.syncTemplates(r.Context(), updated)

	writeJSON(w, http.StatusOK, map[string]any{
		"template":      name,
		"owners":        nonNil(owners),
		"users":         nonNil(users),
		"teams":         nonNil(teams),
		"canEditOwners": admin,
	})
}
