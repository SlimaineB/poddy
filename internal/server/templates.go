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

	all := s.Workspaces.Templates.List()
	out := make([]templateInfo, 0, len(all))
	for _, template := range all {
		if !admin && !cfg.CanUse(template.Name, identity) {
			continue
		}
		out = append(out, templateInfo{
			Info:      template,
			CanManage: s.RBAC != nil && (admin || cfg.IsOwner(template.Name, identity)),
			Owned:     cfg.IsOwner(template.Name, identity),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
