package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
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
	admin := s.isAdmin(id, cfg)
	regionFilter := regionQuery(r)
	if s.Regions != nil {
		if regionFilter != "" {
			if _, ok := s.Regions.Get(regionFilter); !ok {
				http.Error(w, "unknown region", http.StatusBadRequest)
				return
			}
			list, err = s.Regions.ListRegion(r.Context(), regionFilter, s.hubIdentity(id, admin))
		} else {
			list, err = s.hubList(r.Context(), s.hubIdentity(id, admin))
		}
	} else if admin {
		list, err = s.Workspaces.List(r.Context())
	} else {
		list, err = s.Workspaces.ListByOwner(r.Context(), ownerID(id.User))
	}
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if regionFilter != "" && s.Regions == nil {
		filtered := make([]k8s.Workspace, 0, len(list))
		for _, workspace := range list {
			if workspace.Region == regionFilter {
				filtered = append(filtered, workspace)
			}
		}
		list = filtered
	}
	query := searchQuery(r)
	if query != "" {
		filtered := make([]k8s.Workspace, 0, len(list))
		for _, workspace := range list {
			if matchesSearch(query, workspace.Name, workspace.Template, workspace.Owner, workspace.Region, workspace.Phase, workspace.Image) {
				filtered = append(filtered, workspace)
			}
		}
		list = filtered
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	if s.Regions == nil {
		s.enrichWorkspaceAccess(r.Context(), list, id, cfg)
	}
	if !pagedRequest(r) {
		writeJSON(w, http.StatusOK, list)
		return
	}
	page, perPage := pageRequest(r)
	writeJSON(w, http.StatusOK, paginateResult(list, page, perPage))
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
	if s.Regions != nil {
		details, detailErr := s.Regions.Details(r.Context(), name, s.hubIdentity(id, s.isAdmin(id, cfg)))
		if detailErr != nil {
			writeRegionError(w, detailErr)
			return
		}
		writeJSON(w, http.StatusOK, details)
		return
	}

	ws, pod, err := s.Workspaces.GetWorkspacePod(r.Context(), name)
	if err != nil {
		writeK8sError(w, err, http.StatusNotFound)
		return
	}

	// Check access using the workspace already fetched above.
	if !s.isAdmin(id, cfg) && !strings.EqualFold(ws.Owner, ownerID(id.User)) {
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

	// Pod info comes from the same fetch as the workspace.
	var podIP, nodeName, podPhase string
	if pod != nil {
		podIP = pod.Status.PodIP
		nodeName = pod.Spec.NodeName
		podPhase = string(pod.Status.Phase)
	}

	// Volumes attached to this workspace (owner/admin only, already authorized above)
	var volumes []k8s.Volume
	if attached, lerr := s.Workspaces.ListWorkspaceVolumes(r.Context(), name); lerr == nil {
		for i := range attached {
			attached[i].Assigned = true
			attached[i].Owner = ws.Owner
			attached[i].WorkspacePhase = ws.Phase
		}
		volumes = attached
	}

	accessInfo, accessErr := s.workspaceAccessInfoFor(r.Context(), name, ws.Owner, id, cfg)
	if accessErr != nil {
		log.Printf("workspace access %s: %v", name, accessErr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":          ws.Name,
		"template":      ws.Template,
		"phase":         ws.Phase,
		"image":         ws.Image,
		"owner":         ws.Owner,
		"persistent":    ws.Persistent,
		"browserVSCode": ws.BrowserVSCode,
		"jupyterLab":    ws.JupyterLab,
		"createdAt":     ws.CreatedAt,
		"schedule":      ws.Schedule,
		"podIP":         podIP,
		"nodeName":      nodeName,
		"podPhase":      podPhase,
		"volumes":       volumes,
		"owners":        nonNil(owners),
		"users":         nonNil(users),
		"teams":         nonNil(teams),
		"teamMembers":   teamMembers,
		"access":        accessInfo,
	})
}

func (s *Server) workspaceLogs(w http.ResponseWriter, r *http.Request) {
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
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	tail := int64(0)
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
			tail = value
		}
	}
	logs, err := s.Workspaces.Logs(r.Context(), name, tail)
	if err != nil {
		writeK8sError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

var workspacePrefixCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func workspaceNamePrefix(id *identity) string {
	if id == nil {
		return ""
	}
	raw := id.User.Username
	if raw == "" {
		raw = id.User.Email
	}
	if at := strings.Index(raw, "@"); at > 0 {
		raw = raw[:at]
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
	if name == "" || id == nil || id.Method == "none" || id.Method == "region" {
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

	var input struct {
		k8s.CreateRequest
		Region string `json:"region"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req := input.CreateRequest
	if id.Method != "region" {
		req.ExternalAuthReady = false
		req.ExternalAuthEnv = nil
		req.WorkspaceToken = ""
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
	if s.RegionMode && s.HubURL != "" {
		req.PortalURL = s.HubURL
	}
	quotaCheck, err := s.quotaGuard(r.Context(), id, cfg)
	if err != nil {
		http.Error(w, "quota: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.Regions != nil {
		if err := s.prepareRemoteCreate(r, &req, templateName); err != nil {
			if s.writeExternalAuthRequired(w, r, err) {
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		regionName := input.Region
		if regionName == "" {
			if cfg, ok := s.Regions.Default(); ok {
				regionName = cfg.Name
			}
		}
		if quotaCheck != nil {
			if s.Workspaces == nil {
				http.Error(w, "quota: workspace manager is not configured", http.StatusInternalServerError)
				return
			}
			usage, err := s.Workspaces.PreviewUsage(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := quotaCheck(usage); err != nil {
				writeQuotaError(w, err)
				return
			}
		}
		start := time.Now()
		result := "error"
		defer func() {
			s.Metrics.WorkspaceOperation("create", result)
			s.Metrics.WorkspaceCreate(templateName, result, time.Since(start))
		}()
		ws, err := s.Regions.Create(r.Context(), regionName, req, s.hubIdentity(id, s.isAdmin(id, cfg)))
		if err != nil {
			writeRegionError(w, err)
			return
		}
		result = "success"
		writeJSON(w, http.StatusCreated, ws)
		return
	}

	start := time.Now()
	result := "error"
	defer func() {
		s.Metrics.WorkspaceOperation("create", result)
		s.Metrics.WorkspaceCreate(templateName, result, time.Since(start))
	}()

	req.QuotaCheck = quotaCheck
	ws, err := s.Workspaces.Create(r.Context(), req)
	if err != nil {
		var quotaErr *quotaExceededError
		if errors.As(err, &quotaErr) {
			http.Error(w, quotaErr.Error(), http.StatusConflict)
			return
		}
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
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
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
	if err := s.shareStore().DeleteWorkspace(r.Context(), name); err != nil {
		log.Printf("workspace shares %s: delete: %v", name, err)
	}
	if err := s.accessStore().DeleteWorkspace(r.Context(), name); err != nil {
		log.Printf("workspace access %s: delete: %v", name, err)
	}
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

func (s *Server) updateWorkspaceSchedule(w http.ResponseWriter, r *http.Request) {
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
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var schedule k8s.Schedule
	if err := json.NewDecoder(r.Body).Decode(&schedule); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := schedule.Normalize(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setAuditTarget(r, name)
	setAuditDetail(r, "schedule", schedule)

	ws, err := s.Workspaces.SetSchedule(r.Context(), name, schedule)
	if apierrors.IsNotFound(err) {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	s.syncWorkspace(r.Context(), name)
	writeJSON(w, http.StatusOK, ws)
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
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
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
