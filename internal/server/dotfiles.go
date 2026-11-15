package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"poddy/internal/dotfiles"
	"poddy/internal/region"
)

type dotfilesSettings struct {
	URL       string `json:"url"`
	Branch    string `json:"branch,omitempty"`
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func dotfilesSettingsFor(cfg dotfiles.Config, source string) dotfilesSettings {
	out := dotfilesSettings{
		URL:    cfg.URL,
		Branch: cfg.Branch,
		Source: source,
	}
	if !cfg.UpdatedAt.IsZero() {
		out.UpdatedAt = cfg.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

func (s *Server) myDotfiles(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, source, err := dotfiles.Effective(r.Context(), s.Dotfiles, ownerID(id.User), s.GlobalDotfiles)
	if err != nil {
		http.Error(w, "dotfiles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, dotfilesSettingsFor(cfg, source))
}

func (s *Server) setMyDotfiles(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Dotfiles == nil {
		http.Error(w, "dotfiles are not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	cfg, err := dotfiles.Normalize(dotfiles.Config{URL: req.URL, Branch: req.Branch})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.Empty() {
		http.Error(w, "dotfiles repository URL is required", http.StatusBadRequest)
		return
	}
	owner := ownerID(id.User)
	setAuditTarget(r, owner)
	setAuditDetail(r, "branch", cfg.Branch)
	if err := s.Dotfiles.Set(r.Context(), owner, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg, err = s.Dotfiles.Get(r.Context(), owner)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, dotfilesSettingsFor(cfg, dotfiles.SourceUser))
}

func (s *Server) deleteMyDotfiles(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Dotfiles == nil {
		http.Error(w, "dotfiles are not available", http.StatusNotImplemented)
		return
	}
	owner := ownerID(id.User)
	setAuditTarget(r, owner)
	if err := s.Dotfiles.Delete(r.Context(), owner); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionDotfiles(w http.ResponseWriter, r *http.Request) {
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
		Owner string `json:"owner"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		http.Error(w, "owner is required", http.StatusBadRequest)
		return
	}
	resolved, source, err := dotfiles.Effective(r.Context(), s.Dotfiles, owner, s.GlobalDotfiles)
	if err != nil {
		http.Error(w, "dotfiles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, dotfilesSettingsFor(resolved, source))
}
