package server

import (
	"net/http"
)

func (s *Server) templates(w http.ResponseWriter, r *http.Request) {
	if s.Workspaces.Templates == nil {
		writeJSON(w, http.StatusOK, []any{})
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
	identity := rbacIdentity(id.User)
	query := searchQuery(r)
	manageableOnly := r.URL.Query().Get("manageable") == "1"

	all := s.Workspaces.Templates.List()
	out := make([]templateInfo, 0, len(all))
	for _, template := range all {
		if !admin && !cfg.CanUse(template.Name, identity) {
			continue
		}
		canManage := s.RBAC != nil && (admin || cfg.IsOwner(template.Name, identity))
		if manageableOnly && !canManage {
			continue
		}
		if query != "" && !matchesSearch(query, template.Name, template.DisplayName, template.Description, template.Image, template.Version) {
			continue
		}
		out = append(out, templateInfo{
			Info:      template,
			CanManage: canManage,
			Owned:     cfg.IsOwner(template.Name, identity),
		})
	}
	if !pagedRequest(r) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	page, perPage := pageRequest(r)
	writeJSON(w, http.StatusOK, paginateResult(out, page, perPage))
}
