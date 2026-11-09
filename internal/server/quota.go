package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"poddy/internal/k8s"
	"poddy/internal/rbac"
)

type quotaExceededError struct {
	err error
}

func (e *quotaExceededError) Error() string { return e.err.Error() }

func (e *quotaExceededError) Unwrap() error { return e.err }

func writeQuotaError(w http.ResponseWriter, err error) {
	var quotaErr *quotaExceededError
	if errors.As(err, &quotaErr) {
		http.Error(w, quotaErr.Error(), http.StatusConflict)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (s *Server) quotaGuard(ctx context.Context, id *identity, cfg rbac.Config) (func(k8s.ResourceUsage) error, error) {
	if s.RegionMode || s.RBAC == nil || s.isAdmin(id, cfg) {
		return nil, nil
	}
	quota, err := cfg.EffectiveQuota(rbacIdentity(id.User))
	if err != nil {
		return nil, err
	}
	if quota.IsZero() {
		return nil, nil
	}
	current, err := s.workspaceUsage(ctx, id, cfg)
	if err != nil {
		return nil, err
	}
	return func(usage k8s.ResourceUsage) error {
		projected := current
		projected.Workspaces++
		projected.CPU.Add(usage.CPU)
		projected.Memory.Add(usage.Memory)
		projected.Storage.Add(usage.Storage)
		if err := quota.Check(projected); err != nil {
			return &quotaExceededError{err: err}
		}
		return nil
	}, nil
}

func (s *Server) workspaceUsage(ctx context.Context, id *identity, cfg rbac.Config) (rbac.Usage, error) {
	var list []k8s.Workspace
	var err error
	if s.Regions != nil {
		var problems []error
		list, problems = s.Regions.List(ctx, s.hubIdentity(id, s.isAdmin(id, cfg)))
		if len(problems) > 0 {
			return rbac.Usage{}, fmt.Errorf("cannot calculate quota across regions: %w", errors.Join(problems...))
		}
	} else if s.Workspaces != nil {
		list, err = s.Workspaces.ListByOwner(ctx, ownerID(id.User))
	} else {
		return rbac.Usage{}, fmt.Errorf("workspace manager is not configured")
	}
	if err != nil {
		return rbac.Usage{}, err
	}
	return usageFromWorkspaces(list)
}

func usageFromWorkspaces(list []k8s.Workspace) (rbac.Usage, error) {
	usage := rbac.Usage{Workspaces: len(list)}
	for _, workspace := range list {
		if err := addQuotaQuantity(&usage.CPU, workspace.CPU); err != nil {
			return rbac.Usage{}, fmt.Errorf("workspace %q CPU: %w", workspace.Name, err)
		}
		if err := addQuotaQuantity(&usage.Memory, workspace.Memory); err != nil {
			return rbac.Usage{}, fmt.Errorf("workspace %q memory: %w", workspace.Name, err)
		}
		if err := addQuotaQuantity(&usage.Storage, workspace.Storage); err != nil {
			return rbac.Usage{}, fmt.Errorf("workspace %q storage: %w", workspace.Name, err)
		}
	}
	return usage, nil
}

func addQuotaQuantity(target *resource.Quantity, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return err
	}
	target.Add(quantity)
	return nil
}

type userQuotaInfo struct {
	Identity string     `json:"identity"`
	Quota    rbac.Quota `json:"quota"`
}

func (s *Server) userQuotas(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
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
	if !s.isAdmin(id, cfg) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	out := make([]userQuotaInfo, 0, len(cfg.UserQuotas))
	for identity, quota := range cfg.UserQuotas {
		out = append(out, userQuotaInfo{Identity: identity, Quota: quota})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Identity) < strings.ToLower(out[j].Identity)
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateUserQuota(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
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
	if !s.isAdmin(id, cfg) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := strings.TrimSpace(r.PathValue("identity"))
	if identity == "" || identity == rbac.Wildcard {
		http.Error(w, "invalid user identity", http.StatusBadRequest)
		return
	}
	setAuditTarget(r, identity)

	var quota rbac.Quota
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&quota); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	quota.Normalize()
	if err := quota.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	updated, err := s.RBAC.Update(r.Context(), func(cfg *rbac.Config) error {
		if cfg.UserQuotas == nil {
			cfg.UserQuotas = map[string]rbac.Quota{}
		}
		if quota.IsZero() {
			delete(cfg.UserQuotas, identity)
			return nil
		}
		cfg.UserQuotas[identity] = quota
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	writeJSON(w, http.StatusOK, userQuotaInfo{Identity: identity, Quota: quota})
}

func (s *Server) deleteUserQuota(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
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
	if !s.isAdmin(id, cfg) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := strings.TrimSpace(r.PathValue("identity"))
	if identity == "" {
		http.Error(w, "invalid user identity", http.StatusBadRequest)
		return
	}
	setAuditTarget(r, identity)
	updated, err := s.RBAC.Update(r.Context(), func(cfg *rbac.Config) error {
		delete(cfg.UserQuotas, identity)
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	w.WriteHeader(http.StatusNoContent)
}
