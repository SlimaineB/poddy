package server

import (
	"net/http"
	"strings"
	"time"

	"poddy/internal/metadata"
)

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
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
	if !s.canViewAudit(id, cfg) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.Metadata == nil || !s.Metadata.AuditEnabled() {
		http.Error(w, "audit store not configured", http.StatusNotImplemented)
		return
	}

	page, perPage := pageRequest(r)
	filter := metadata.AuditFilter{
		Query:     searchQuery(r),
		Action:    strings.TrimSpace(r.URL.Query().Get("action")),
		Outcome:   strings.TrimSpace(r.URL.Query().Get("outcome")),
		Actor:     strings.TrimSpace(r.URL.Query().Get("actor")),
		Workspace: strings.TrimSpace(r.URL.Query().Get("workspace")),
		Page:      page,
		PerPage:   perPage,
	}
	if value := strings.TrimSpace(r.URL.Query().Get("since")); value != "" {
		filter.Since, err = parseAuditTime(value, false)
		if err != nil {
			http.Error(w, "invalid since: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("until")); value != "" {
		filter.Until, err = parseAuditTime(value, true)
		if err != nil {
			http.Error(w, "invalid until: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	result, err := s.Metadata.ListAudit(r.Context(), s.Workspaces.Namespace, filter)
	if err != nil {
		http.Error(w, "audit: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseAuditTime(value string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return t.Add(24 * time.Hour), nil
	}
	return t, nil
}
