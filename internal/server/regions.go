package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"poddy/internal/auth"
	"poddy/internal/k8s"
	"poddy/internal/region"
	"poddy/internal/templates"
)

func (s *Server) hubIdentity(id *identity, admin bool) region.Identity {
	return region.Identity{User: id.User, Admin: admin}
}

func (s *Server) hubAdminIdentity() region.Identity {
	return region.Identity{User: auth.User{Username: "hub", Subject: "hub"}, Admin: true}
}

func (s *Server) proxyWorkspace(w http.ResponseWriter, r *http.Request) {
	if s.Regions == nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
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
	identity := s.hubIdentity(id, s.isAdmin(id, cfg))
	name := r.PathValue("name")
	if name == "" {
		name = r.URL.Query().Get("name")
	}
	regionCfg, _, err := s.Regions.FindWorkspace(r.Context(), name, identity)
	if err != nil {
		if region.IsNotFound(err) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		log.Printf("region lookup %s: %v", name, err)
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}
	client := s.Regions.Client(regionCfg.Name)
	if client == nil {
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}
	client.Proxy(w, r, identity)
}

func (s *Server) hubList(ctx context.Context, identity region.Identity) ([]k8s.Workspace, error) {
	list, problems := s.Regions.List(ctx, identity)
	for _, problem := range problems {
		log.Printf("region list: %v", problem)
	}
	return list, nil
}

func (s *Server) hubSyncSSHKeys(ctx context.Context, owner string) {
	if s.Regions == nil || s.UserKeys == nil {
		return
	}
	keys, err := s.UserKeys.ListForOwner(ctx, owner)
	if err != nil {
		log.Printf("ssh keys: list for region sync: %v", err)
		return
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key.Line)
	}
	for _, problem := range s.Regions.SyncSSHKeys(ctx, owner, lines, s.hubAdminIdentity()) {
		log.Printf("ssh keys: region sync: %v", problem)
	}
}

func (s *Server) regionReplaceSSHKeys(w http.ResponseWriter, r *http.Request) {
	if !s.RegionMode || s.UserKeys == nil || s.SSHKeys == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok || id.Method != "region" || !id.Admin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		Keys []string `json:"keys"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	owner := r.PathValue("owner")
	if err := s.UserKeys.ReplaceForOwner(r.Context(), owner, req.Keys); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.reloadSSHKeys(r.Context()); err != nil {
		log.Printf("region ssh keys: reload: %v", err)
	}
	if err := s.Workspaces.SyncSSHKeysForOwner(r.Context(), owner); err != nil {
		log.Printf("region ssh keys: sync workspaces: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func regionConfigInfo(cfg region.Config) map[string]any {
	return map[string]any{
		"name":        cfg.Name,
		"displayName": cfg.DisplayName,
		"default":     cfg.Default,
		"sshHost":     cfg.SSHHost,
		"sshPort":     cfg.SSHPort,
		"lat":         cfg.Lat,
		"lon":         cfg.Lon,
	}
}

func (s *Server) regionSummaries(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Regions == nil {
		writeJSON(w, http.StatusOK, []region.Summary{})
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.Regions.Summaries(r.Context(), s.hubIdentity(id, s.isAdmin(id, cfg))))
}

func (s *Server) prepareRemoteCreate(r *http.Request, req *k8s.CreateRequest, templateName string) error {
	req.ExternalAuthReady = true
	if s.Workspaces == nil || s.Workspaces.Templates == nil || s.ExternalAuth == nil {
		return nil
	}
	tmpl, err := s.Workspaces.Templates.Get(templateName)
	if err != nil || len(tmpl.ExternalAuth) == 0 {
		return err
	}
	env, err := s.ExternalAuth.Prepare(r.Context(), strings.ToLower(strings.TrimSpace(req.Owner)), tmpl.ExternalAuth)
	if err != nil {
		return err
	}
	token, err := s.ExternalAuth.SignWorkspace(req.Name, req.Owner)
	if err != nil {
		return err
	}
	req.ExternalAuthEnv = env
	req.WorkspaceToken = token
	return nil
}

func writeRegionError(w http.ResponseWriter, err error) {
	var httpErr *region.HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.Status == http.StatusNotFound:
			http.Error(w, "workspace not found", http.StatusNotFound)
		case httpErr.Status == http.StatusForbidden:
			http.Error(w, "forbidden", http.StatusForbidden)
		case httpErr.Status == http.StatusConflict:
			http.Error(w, httpErr.Message, http.StatusConflict)
		case httpErr.Status >= 400 && httpErr.Status < 500:
			http.Error(w, httpErr.Message, httpErr.Status)
		default:
			http.Error(w, "region unavailable", http.StatusBadGateway)
		}
		return
	}
	if region.IsNotFound(err) {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	log.Printf("region request: %v", err)
	http.Error(w, "region unavailable", http.StatusBadGateway)
}

func (s *Server) regionExternalAuth(w http.ResponseWriter, r *http.Request) {
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
		Owner        string                   `json:"owner"`
		Workspace    string                   `json:"workspace"`
		PortalURL    string                   `json:"portalUrl"`
		Requirements []templates.ExternalAuth `json:"requirements"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	env, err := s.ExternalAuth.Prepare(r.Context(), strings.ToLower(strings.TrimSpace(req.Owner)), req.Requirements)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	token, err := s.ExternalAuth.SignWorkspace(req.Workspace, req.Owner)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env": env, "token": token})
}
