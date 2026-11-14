package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"poddy/internal/envstore"
	"poddy/internal/rbac"
	"poddy/internal/region"
)

func (s *Server) listEnvironment(w http.ResponseWriter, r *http.Request) {
	scope, err := envstore.ParseScope(r.URL.Query().Get("scope"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, _, _, ok := s.authorizeEnvironment(w, r, scope, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	items, err := s.Environment.List(r.Context(), scope, target)
	if err != nil {
		http.Error(w, "environment: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":  scope,
		"target": target,
		"items":  items,
	})
}

func (s *Server) setEnvironment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string `json:"scope"`
		Target string `json:"target"`
		Name   string `json:"name"`
		Value  string `json:"value"`
		Secret bool   `json:"secret"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	scope, err := envstore.ParseScope(req.Scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, _, _, ok := s.authorizeEnvironment(w, r, scope, req.Target)
	if !ok {
		return
	}
	setAuditTarget(r, target)
	setAuditDetail(r, "scope", string(scope))
	setAuditDetail(r, "name", req.Name)
	setAuditDetail(r, "secret", req.Secret)
	if err := s.Environment.Set(r.Context(), scope, target, req.Name, req.Value, req.Secret); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	scope, err := envstore.ParseScope(r.URL.Query().Get("scope"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, _, _, ok := s.authorizeEnvironment(w, r, scope, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	setAuditTarget(r, target)
	setAuditDetail(r, "scope", string(scope))
	setAuditDetail(r, "name", name)
	if err := s.Environment.Delete(r.Context(), scope, target, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authorizeEnvironment(w http.ResponseWriter, r *http.Request, scope envstore.Scope, rawTarget string) (string, *identity, rbac.Config, bool) {
	if s.Environment == nil {
		http.Error(w, "environment store is not configured", http.StatusNotImplemented)
		return "", nil, rbac.Config{}, false
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", nil, rbac.Config{}, false
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return "", nil, rbac.Config{}, false
	}
	target, err := envstore.CanonicalTarget(scope, rawTarget)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", nil, rbac.Config{}, false
	}
	admin := s.isAdmin(id, cfg)
	owner := ownerID(id.User)

	switch scope {
	case envstore.ScopeTemplate:
		if s.Workspaces == nil || s.Workspaces.Templates == nil {
			http.Error(w, "template catalog is not configured", http.StatusNotImplemented)
			return "", nil, rbac.Config{}, false
		}
		if _, err := s.Workspaces.Templates.Get(target); err != nil {
			http.Error(w, "template not found", http.StatusNotFound)
			return "", nil, rbac.Config{}, false
		}
		if !admin && !cfg.IsOwner(target, rbacIdentity(id.User)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return "", nil, rbac.Config{}, false
		}
	case envstore.ScopeTeam:
		if !admin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return "", nil, rbac.Config{}, false
		}
		if _, ok := cfg.Teams[target]; !ok {
			http.Error(w, "team not found", http.StatusNotFound)
			return "", nil, rbac.Config{}, false
		}
	case envstore.ScopeUser:
		if !admin && !strings.EqualFold(target, owner) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return "", nil, rbac.Config{}, false
		}
	case envstore.ScopeWorkspace:
		if !admin {
			allowed := false
			if s.Regions != nil {
				_, info, err := s.Regions.FindWorkspace(r.Context(), target, s.hubIdentity(id, false))
				if err != nil {
					http.Error(w, "workspace not found", http.StatusNotFound)
					return "", nil, rbac.Config{}, false
				}
				allowed = strings.EqualFold(info.Owner, owner)
			} else if s.Workspaces != nil {
				var err error
				allowed, err = s.workspaceAllowedAny(r, target, id, cfg)
				if err != nil {
					if apierrors.IsNotFound(err) {
						http.Error(w, "workspace not found", http.StatusNotFound)
					} else {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return "", nil, rbac.Config{}, false
				}
			}
			if !allowed {
				http.Error(w, "forbidden", http.StatusForbidden)
				return "", nil, rbac.Config{}, false
			}
		}
	default:
		http.Error(w, "invalid environment scope", http.StatusBadRequest)
		return "", nil, rbac.Config{}, false
	}
	return target, id, cfg, true
}

func (s *Server) resolveEnvironment(ctx context.Context, owner, template, workspace string) (map[string]string, []string, error) {
	if s.Environment == nil {
		return nil, nil, errEnvironmentDisabled
	}
	resolved, err := envstore.ResolveForOwner(ctx, s.Environment, s.RBAC, s.Users, owner, template, workspace)
	if err != nil {
		return nil, nil, err
	}
	secrets := make([]string, 0)
	for name, secret := range resolved.Secrets {
		if secret {
			secrets = append(secrets, name)
		}
	}
	sort.Strings(secrets)
	return resolved.Values, secrets, nil
}

func (s *Server) deleteRegionEnvironment(w http.ResponseWriter, r *http.Request) {
	if s.Regions == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cfg, ok := s.Regions.Get(r.Header.Get(region.RegionNameHeader))
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	claims, err := region.VerifyIdentity(cfg.Key, r.Header.Get(region.IdentityHeader))
	if err != nil || !claims.Admin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.Environment == nil {
		http.Error(w, "environment store is not configured", http.StatusNotImplemented)
		return
	}
	target, err := envstore.CanonicalTarget(envstore.ScopeWorkspace, r.URL.Query().Get("target"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Environment.DeleteTarget(r.Context(), envstore.ScopeWorkspace, target); err != nil {
		http.Error(w, "environment: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionEnvironment(w http.ResponseWriter, r *http.Request) {
	if s.Regions == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cfg, ok := s.Regions.Get(r.Header.Get(region.RegionNameHeader))
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	claims, err := region.VerifyIdentity(cfg.Key, r.Header.Get(region.IdentityHeader))
	if err != nil || !claims.Admin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		Owner     string `json:"owner"`
		Template  string `json:"template"`
		Workspace string `json:"workspace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	values, secrets, err := s.resolveEnvironment(r.Context(), req.Owner, req.Template, req.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values, "secrets": secrets})
}

var errEnvironmentDisabled = &environmentDisabledError{}

type environmentDisabledError struct{}

func (*environmentDisabledError) Error() string { return "environment store is not configured" }
