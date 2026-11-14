package server

import (
	"encoding/json"
	"log"
	"net/http"

	"poddy/internal/envstore"
	"poddy/internal/rbac"
)

type teamInfo struct {
	Name        string      `json:"name"`
	Members     []string    `json:"members,omitempty"`
	MemberCount int         `json:"memberCount"`
	Quota       *rbac.Quota `json:"quota,omitempty"`
}

func (s *Server) teams(w http.ResponseWriter, r *http.Request) {
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

	admin := s.isAdmin(id, cfg)
	if !admin && len(cfg.OwnedTemplates(rbacIdentity(id.User))) == 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	names := cfg.TeamNames()
	teamQuotas := map[string]rbac.Quota{}
	if s.Quota != nil {
		teamQuotas, err = s.Quota.TeamQuotas(r.Context())
		if err != nil {
			http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	query := searchQuery(r)
	out := make([]teamInfo, 0, len(names))
	for _, name := range names {
		team := cfg.Teams[name]
		if query != "" && !matchesSearch(query, append([]string{name}, team.Members...)...) {
			continue
		}
		info := teamInfo{Name: name, MemberCount: len(team.Members), Quota: quotaPtr(teamQuotas[name])}
		if admin {
			info.Members = nonNil(team.Members)
		}
		out = append(out, info)
	}
	if !pagedRequest(r) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	page, perPage := pageRequest(r)
	writeJSON(w, http.StatusOK, paginateResult(out, page, perPage))
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request) {
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

	name := r.PathValue("name")
	if err := rbac.ValidateTeamName(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Members []string    `json:"members"`
		Quota   *rbac.Quota `json:"quota"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	quota := req.Quota
	if quota != nil {
		quota.Normalize()
		if err := quota.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if quota.IsZero() {
			quota = nil
		}
	}

	members := rbac.Normalize(req.Members)
	updated, err := s.RBAC.Update(r.Context(), func(cfg *rbac.Config) error {
		if cfg.Teams == nil {
			cfg.Teams = map[string]rbac.Team{}
		}
		cfg.Teams[name] = rbac.Team{Members: members}
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	s.syncTemplates(r.Context(), updated)

	if req.Quota != nil {
		if quota == nil {
			err = s.Quota.DeleteTeamQuota(r.Context(), name)
		} else {
			err = s.Quota.SetTeamQuota(r.Context(), name, *quota)
		}
		if err != nil {
			http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else if stored, loadErr := s.Quota.TeamQuotas(r.Context()); loadErr == nil {
		quota = quotaPtr(stored[name])
	}

	writeJSON(w, http.StatusOK, teamInfo{
		Name:        name,
		Members:     nonNil(members),
		MemberCount: len(members),
		Quota:       quota,
	})
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
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

	name := r.PathValue("name")
	updated, err := s.RBAC.Update(r.Context(), func(cfg *rbac.Config) error {
		delete(cfg.Teams, name)
		for template, access := range cfg.Templates {
			access.Teams = removeString(access.Teams, name)
			if len(access.Owners) == 0 && len(access.Users) == 0 && len(access.Teams) == 0 {
				delete(cfg.Templates, template)
				continue
			}
			cfg.Templates[template] = access
		}
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	s.syncTemplates(r.Context(), updated)
	if s.Quota != nil {
		if err := s.Quota.DeleteTeamQuota(r.Context(), name); err != nil {
			http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if s.Environment != nil {
		if err := s.Environment.DeleteTarget(r.Context(), envstore.ScopeTeam, name); err != nil {
			log.Printf("team environment %s: delete: %v", name, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func quotaPtr(quota rbac.Quota) *rbac.Quota {
	if quota.IsZero() {
		return nil
	}
	return &quota
}

func removeString(values []string, drop string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != drop {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
