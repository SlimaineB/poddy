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

	"poddy/internal/auth"
	"poddy/internal/envstore"
	"poddy/internal/k8s"
	"poddy/internal/quota"
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
	if s.RegionMode || s.RBAC == nil || s.isAdmin(id, cfg) || s.Quota == nil {
		return nil, nil
	}
	limit, err := quota.Effective(ctx, s.Quota, cfg, rbacIdentity(id.User))
	if err != nil {
		return nil, err
	}
	if limit.IsZero() {
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
		if err := limit.Check(projected); err != nil {
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

type quotaUsageInfo struct {
	Workspaces int    `json:"workspaces"`
	CPU        string `json:"cpu,omitempty"`
	Memory     string `json:"memory,omitempty"`
	Storage    string `json:"storage,omitempty"`
}

type userQuotaDetails struct {
	Identity  string         `json:"identity"`
	Effective rbac.Quota     `json:"effective"`
	Usage     quotaUsageInfo `json:"usage"`
}

type quotaSourceInfo struct {
	Scope  string `json:"scope"`
	Target string `json:"target,omitempty"`
}

type workspaceQuotaUsage struct {
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Storage string `json:"storage,omitempty"`
}

type workspaceQuotaInfo struct {
	Owner     string                     `json:"owner"`
	Effective rbac.Quota                 `json:"effective"`
	Usage     quotaUsageInfo             `json:"usage"`
	Workspace workspaceQuotaUsage        `json:"workspace"`
	Sources   map[string]quotaSourceInfo `json:"sources"`
}

// workspaceQuota resolves the owner's effective quota, usage, and the source of
// each limit. The viewer must already be authorized for the workspace.
func (s *Server) workspaceQuota(ctx context.Context, viewer *identity, cfg rbac.Config, owner, cpu, memory, storage string) (*workspaceQuotaInfo, error) {
	if s.Quota == nil {
		return nil, nil
	}
	identity := envstore.IdentityForOwner(ctx, s.Users, owner)
	userQuotas, err := s.Quota.UserQuotas(ctx)
	if err != nil {
		return nil, err
	}
	teamQuotas, err := s.Quota.TeamQuotas(ctx)
	if err != nil {
		return nil, err
	}
	effective, err := cfg.EffectiveQuotaWith(identity, userQuotas, teamQuotas)
	if err != nil {
		return nil, err
	}
	var usage rbac.Usage
	if viewer != nil && !s.isAdmin(viewer, cfg) && strings.EqualFold(owner, ownerID(viewer.User)) {
		usage, err = s.workspaceUsage(ctx, viewer, cfg)
	} else {
		usage, err = s.usageForOwner(ctx, viewer, owner)
	}
	if err != nil {
		return nil, err
	}
	return &workspaceQuotaInfo{
		Owner:     owner,
		Effective: effective,
		Usage:     usageInfo(usage),
		Workspace: workspaceQuotaUsage{CPU: cpu, Memory: memory, Storage: storage},
		Sources:   quotaSources(cfg, identity, owner, userQuotas, teamQuotas, effective),
	}, nil
}

func quotaSources(cfg rbac.Config, id rbac.Identity, owner string, userQuotas, teamQuotas map[string]rbac.Quota, effective rbac.Quota) map[string]quotaSourceInfo {
	out := map[string]quotaSourceInfo{}
	user, hasUser := (rbac.Config{UserQuotas: userQuotas}).UserQuota(id)
	user.Normalize()
	if hasUser {
		for _, field := range []string{"maxWorkspaces", "cpu", "memory", "storage"} {
			if quotaFieldSet(user, field) {
				out[field] = quotaSourceInfo{Scope: "user", Target: owner}
			}
		}
	}
	for _, field := range []string{"maxWorkspaces", "cpu", "memory", "storage"} {
		if _, ok := out[field]; ok || !quotaFieldSet(effective, field) {
			continue
		}
		if team, ok := quotaTeamSource(cfg, id, teamQuotas, effective, field); ok {
			out[field] = quotaSourceInfo{Scope: "team", Target: team}
		}
	}
	return out
}

func quotaTeamSource(cfg rbac.Config, id rbac.Identity, teamQuotas map[string]rbac.Quota, effective rbac.Quota, field string) (string, bool) {
	for _, name := range cfg.TeamNames() {
		quota, ok := teamQuotas[name]
		if !ok || !cfg.InTeam(name, id) {
			continue
		}
		if quotaFieldEqual(quota, effective, field) {
			return name, true
		}
	}
	return "", false
}

func quotaFieldSet(quota rbac.Quota, field string) bool {
	switch field {
	case "maxWorkspaces":
		return quota.MaxWorkspaces != nil
	case "cpu":
		return strings.TrimSpace(quota.CPU) != ""
	case "memory":
		return strings.TrimSpace(quota.Memory) != ""
	case "storage":
		return strings.TrimSpace(quota.Storage) != ""
	default:
		return false
	}
}

func quotaFieldEqual(left, right rbac.Quota, field string) bool {
	switch field {
	case "maxWorkspaces":
		return left.MaxWorkspaces != nil && right.MaxWorkspaces != nil && *left.MaxWorkspaces == *right.MaxWorkspaces
	case "cpu", "memory", "storage":
		var leftValue, rightValue string
		switch field {
		case "cpu":
			leftValue, rightValue = left.CPU, right.CPU
		case "memory":
			leftValue, rightValue = left.Memory, right.Memory
		case "storage":
			leftValue, rightValue = left.Storage, right.Storage
		}
		leftValue, rightValue = strings.TrimSpace(leftValue), strings.TrimSpace(rightValue)
		if leftValue == "" || rightValue == "" {
			return false
		}
		leftQuantity, err := resource.ParseQuantity(leftValue)
		if err != nil {
			return false
		}
		rightQuantity, err := resource.ParseQuantity(rightValue)
		if err != nil {
			return false
		}
		return leftQuantity.Cmp(rightQuantity) == 0
	default:
		return false
	}
}

func usageInfo(usage rbac.Usage) quotaUsageInfo {
	return quotaUsageInfo{
		Workspaces: usage.Workspaces,
		CPU:        usage.CPU.String(),
		Memory:     usage.Memory.String(),
		Storage:    usage.Storage.String(),
	}
}

// quotaIdentity turns a stored quota key (plain identity, user:<name>, or
// email:<address>) into the identity used to resolve team memberships.
func quotaIdentity(value string) auth.User {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "email:")
	value = strings.TrimPrefix(value, "user:")
	if strings.Contains(value, "@") {
		return auth.User{Email: value}
	}
	return auth.User{Username: value}
}

// usageForOwner sums the workspaces owned by one identity. The caller must be an
// admin so the full list is visible across regions.
func (s *Server) usageForOwner(ctx context.Context, id *identity, owner string) (rbac.Usage, error) {
	var list []k8s.Workspace
	var err error
	if s.Regions != nil {
		var problems []error
		list, problems = s.Regions.List(ctx, s.hubIdentity(id, true))
		if len(problems) > 0 {
			return rbac.Usage{}, fmt.Errorf("cannot calculate quota across regions: %w", errors.Join(problems...))
		}
	} else if s.Workspaces != nil {
		list, err = s.Workspaces.List(ctx)
	} else {
		return rbac.Usage{}, fmt.Errorf("workspace manager is not configured")
	}
	if err != nil {
		return rbac.Usage{}, err
	}
	owned := make([]k8s.Workspace, 0, len(list))
	for _, workspace := range list {
		if strings.EqualFold(workspace.Owner, owner) {
			owned = append(owned, workspace)
		}
	}
	return usageFromWorkspaces(owned)
}

type myQuotaInfo struct {
	Effective rbac.Quota     `json:"effective"`
	Usage     quotaUsageInfo `json:"usage"`
}

func (s *Server) myQuota(w http.ResponseWriter, r *http.Request) {
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
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
		return
	}
	effective, err := quota.Effective(r.Context(), s.Quota, cfg, rbacIdentity(id.User))
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	usage, err := s.workspaceUsage(r.Context(), id, cfg)
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, myQuotaInfo{Effective: effective, Usage: usageInfo(usage)})
}

func (s *Server) userQuotaDetails(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		http.Error(w, "RBAC is not configured", http.StatusNotImplemented)
		return
	}
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
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
	user := quotaIdentity(identity)
	effective, err := quota.Effective(r.Context(), s.Quota, cfg, rbacIdentity(user))
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	usage, err := s.usageForOwner(r.Context(), id, ownerID(user))
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, userQuotaDetails{
		Identity:  identity,
		Effective: effective,
		Usage:     usageInfo(usage),
	})
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
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
		return
	}
	quotas, err := s.Quota.UserQuotas(r.Context())
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]userQuotaInfo, 0, len(quotas))
	for identity, quota := range quotas {
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
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
		return
	}

	identity := strings.TrimSpace(r.PathValue("identity"))
	if identity == "" || identity == rbac.Wildcard {
		http.Error(w, "invalid user identity", http.StatusBadRequest)
		return
	}
	setAuditTarget(r, identity)

	var value rbac.Quota
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	value.Normalize()
	if err := value.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if value.IsZero() {
		err = s.Quota.DeleteUserQuota(r.Context(), identity)
	} else {
		err = s.Quota.SetUserQuota(r.Context(), identity, value)
	}
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, userQuotaInfo{Identity: identity, Quota: value})
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
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
		return
	}

	identity := strings.TrimSpace(r.PathValue("identity"))
	if identity == "" {
		http.Error(w, "invalid user identity", http.StatusBadRequest)
		return
	}
	setAuditTarget(r, identity)
	if err := s.Quota.DeleteUserQuota(r.Context(), identity); err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
