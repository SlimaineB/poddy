package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"poddy/internal/k8s"
)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
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

	var list []k8s.Workspace
	if s.isAdmin(id, cfg) {
		list, err = s.Workspaces.List(r.Context())
	} else {
		list, err = s.Workspaces.ListByOwner(r.Context(), ownerID(id.User))
	}
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) workspaceDetails(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ws, err := s.Workspaces.GetWorkspace(r.Context(), name)
	if err != nil {
		writeK8sError(w, err, http.StatusNotFound)
		return
	}

	// Check access
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Get template access info from RBAC
	var users []string
	var teams []string
	var owners []string
	if ws.Template != "" {
		if access, ok := cfg.Access(ws.Template); ok {
			owners = access.Owners
			users = access.Users
			teams = access.Teams
		}
	}

	// Resolve team members
	teamMembers := map[string][]string{}
	for _, t := range teams {
		if tCfg, ok := cfg.Teams[t]; ok {
			teamMembers[t] = tCfg.Members
		}
	}

	// Get pod info for running workspaces
	var podIP, nodeName, podPhase string
	if ws.Phase != "Stopped" {
		if pod, err := s.Workspaces.Get(r.Context(), name); err == nil {
			podIP = pod.Status.PodIP
			nodeName = pod.Spec.NodeName
			podPhase = string(pod.Status.Phase)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":        ws.Name,
		"template":    ws.Template,
		"phase":       ws.Phase,
		"image":       ws.Image,
		"owner":       ws.Owner,
		"persistent":  ws.Persistent,
		"createdAt":   ws.CreatedAt,
		"podIP":       podIP,
		"nodeName":    nodeName,
		"podPhase":    podPhase,
		"owners":      nonNil(owners),
		"users":       nonNil(users),
		"teams":       nonNil(teams),
		"teamMembers": teamMembers,
	})
}

var workspacePrefixCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func workspaceNamePrefix(id *identity) string {
	if id == nil {
		return ""
	}
	raw := id.User.Username
	if raw == "" && id.User.Email != "" {
		raw = strings.SplitN(id.User.Email, "@", 2)[0]
	}
	if raw == "" {
		raw = id.User.Subject
	}
	raw = strings.ToLower(raw)
	raw = workspacePrefixCleaner.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	if len(raw) > 32 {
		raw = strings.TrimRight(raw[:32], "-")
	}
	if raw == "" {
		raw = "user"
	}
	return raw
}

func workspaceNameFor(id *identity, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || id == nil || id.Method == "none" {
		return name
	}
	prefix := workspaceNamePrefix(id)
	if prefix == "" || name == prefix || strings.HasPrefix(name, prefix+"-") {
		return name
	}
	return prefix + "-" + name
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
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

	var req k8s.CreateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Name = workspaceNameFor(id, req.Name)
	setAuditTarget(r, req.Name)

	templateName := req.Template
	if templateName == "" {
		templateName = s.Workspaces.DefaultTemplate
		if templateName == "" {
			templateName = "base"
		}
	}
	setAuditDetail(r, "template", templateName)
	if !s.isAdmin(id, cfg) && !cfg.CanUse(templateName, rbacIdentity(id.User)) {
		http.Error(w, "template not available for this user", http.StatusForbidden)
		return
	}
	req.Owner = ownerID(id.User)
	req.PortalURL = s.externalAuthPortalURL(r)

	start := time.Now()
	result := "error"
	defer func() {
		s.Metrics.WorkspaceOperation("create", result)
		s.Metrics.WorkspaceCreate(templateName, result, time.Since(start))
	}()

	ws, err := s.Workspaces.Create(r.Context(), req)
	if err != nil {
		if s.writeExternalAuthRequired(w, r, err) {
			return
		}
		writeK8sError(w, err, http.StatusBadRequest)
		return
	}
	result = "success"
	s.syncWorkspace(r.Context(), ws.Name)
	writeJSON(w, http.StatusCreated, ws)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	if s.Forwards != nil {
		s.Forwards.Remove(name)
	}
	if s.Local != nil {
		_ = s.Local.RemoveSSHConfig(name)
	}

	result := "error"
	defer func() { s.Metrics.WorkspaceOperation("delete", result) }()

	err = s.Workspaces.Delete(r.Context(), name)
	if apierrors.IsNotFound(err) {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	result = "success"
	s.deleteWorkspaceMetadata(r.Context(), name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.workspaceLifecycle(w, r, "stop")
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	s.workspaceLifecycle(w, r, "start")
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	s.workspaceLifecycle(w, r, "restart")
}

func (s *Server) workspaceLifecycle(w http.ResponseWriter, r *http.Request, action string) {
	name := r.PathValue("name")
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
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	result := "error"
	defer func() { s.Metrics.WorkspaceOperation(action, result) }()

	var ws k8s.Workspace
	switch action {
	case "stop":
		ws, err = s.Workspaces.Stop(r.Context(), name)
	case "start":
		ws, err = s.Workspaces.Start(r.Context(), name)
	case "restart":
		ws, err = s.Workspaces.Restart(r.Context(), name)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		if s.writeExternalAuthRequired(w, r, err) {
			return
		}
		switch {
		case apierrors.IsNotFound(err):
			http.Error(w, "workspace not found", http.StatusNotFound)
		case errors.Is(err, k8s.ErrAlreadyRunning), errors.Is(err, k8s.ErrNotRunning):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			writeK8sError(w, err, http.StatusInternalServerError)
		}
		return
	}
	if action != "start" {
		if s.Forwards != nil {
			s.Forwards.Remove(name)
		}
		if s.Local != nil {
			_ = s.Local.RemoveSSHConfig(name)
		}
	}
	result = "success"
	s.syncWorkspace(r.Context(), name)
	writeJSON(w, http.StatusOK, ws)
}
