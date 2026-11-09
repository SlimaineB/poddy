package server

import (
	"encoding/json"
	"net/http"

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
	query := searchQuery(r)
	out := make([]teamInfo, 0, len(names))
	for _, name := range names {
		team := cfg.Teams[name]
		if query != "" && !matchesSearch(query, append([]string{name}, team.Members...)...) {
			continue
		}
		info := teamInfo{Name: name, MemberCount: len(team.Members), Quota: team.Quota}
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
		existing := cfg.Teams[name]
		if req.Quota == nil {
			quota = existing.Quota
		}
		cfg.Teams[name] = rbac.Team{Members: members, Quota: quota}
		return nil
	})
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncRBAC(r.Context(), updated)
	s.syncTemplates(r.Context(), updated)

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
	w.WriteHeader(http.StatusNoContent)
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
