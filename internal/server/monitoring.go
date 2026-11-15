package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
	"poddy/internal/monitoring"
)

// MonitoringConfig controls every monitoring surface independently. The
// server enforces these flags for API routes; the UI only reflects them.
type MonitoringConfig struct {
	Enabled       bool
	Live          bool
	History       bool
	Global        bool
	Costs         bool
	CostsForUsers bool
}

func (s *Server) workspaceMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.Live {
		http.NotFound(w, r)
		return
	}
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
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
	name := r.PathValue("name")
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	_, pod, err := s.Workspaces.GetWorkspacePod(r.Context(), name)
	if err != nil {
		writeK8sError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.LiveMetrics.Workspace(r.Context(), pod))
}

func (s *Server) workspaceMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.History {
		http.NotFound(w, r)
		return
	}
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
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
	name := r.PathValue("name")
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if !validHistoryParams(r) {
		http.Error(w, "invalid range or step", http.StatusBadRequest)
		return
	}
	if s.Prometheus == nil {
		writeJSON(w, http.StatusOK, monitoring.History{
			Available: false,
			Reason:    "Prometheus is not configured",
		})
		return
	}
	history, err := s.Prometheus.WorkspaceHistory(
		r.Context(),
		s.Workspaces.Namespace,
		k8s.PodName(name),
		k8s.PVCName(name),
		r.URL.Query().Get("range"),
		r.URL.Query().Get("step"),
	)
	if err != nil {
		log.Printf("monitoring history %s: %v", name, err)
		if history.Reason == "" {
			history.Reason = "Prometheus is unavailable"
		}
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) monitoringOverview(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.Global {
		http.NotFound(w, r)
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
	if s.Regions != nil {
		overview, problems := s.Regions.MonitoringOverview(r.Context(), s.hubAdminIdentity())
		for _, problem := range problems {
			log.Printf("monitoring overview: %v", problem)
		}
		writeJSON(w, http.StatusOK, overview)
		return
	}
	overview, err := s.localMonitoringOverview(r.Context())
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) monitoringHistory(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.History || !s.Monitoring.Global {
		http.NotFound(w, r)
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
	if !validHistoryParams(r) {
		http.Error(w, "invalid range or step", http.StatusBadRequest)
		return
	}
	if s.Regions != nil {
		history, problems := s.Regions.MonitoringHistory(
			r.Context(),
			s.hubAdminIdentity(),
			r.URL.Query().Get("range"),
			r.URL.Query().Get("step"),
		)
		for _, problem := range problems {
			log.Printf("monitoring history: %v", problem)
		}
		writeJSON(w, http.StatusOK, history)
		return
	}
	if s.Prometheus == nil {
		writeJSON(w, http.StatusOK, monitoring.History{
			Available: false,
			Reason:    "Prometheus is not configured",
		})
		return
	}
	history, err := s.Prometheus.NamespaceHistory(
		r.Context(),
		s.Workspaces.Namespace,
		r.URL.Query().Get("range"),
		r.URL.Query().Get("step"),
	)
	if err != nil {
		log.Printf("monitoring history: %v", err)
		if history.Reason == "" {
			history.Reason = "Prometheus is unavailable"
		}
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) localMonitoringOverview(ctx context.Context) (monitoring.Overview, error) {
	workspaces, err := s.Workspaces.List(ctx)
	if err != nil {
		return monitoring.Overview{}, err
	}
	pods, err := s.Workspaces.ListPods(ctx)
	if err != nil {
		return monitoring.Overview{}, err
	}
	podByName := make(map[string]*corev1.Pod, len(pods))
	for _, pod := range pods {
		name := k8s.PodWorkspaceName(pod)
		if name == "" {
			continue
		}
		existing := podByName[name]
		if existing == nil || (existing.Status.Phase != corev1.PodRunning && pod.Status.Phase == corev1.PodRunning) {
			podByName[name] = pod
		}
	}

	overview := monitoring.Overview{Workspaces: make([]monitoring.WorkspaceOverview, 0, len(workspaces))}
	if s.LiveMetrics == nil {
		overview.Reason = monitoring.Reason(monitoring.ErrNotConfigured)
	}
	for _, workspace := range workspaces {
		pod := podByName[workspace.Name]
		metrics := s.LiveMetrics.Workspace(ctx, pod)
		row := monitoring.WorkspaceOverview{
			Name:       workspace.Name,
			Template:   workspace.Template,
			Owner:      workspace.Owner,
			Phase:      workspace.Phase,
			Region:     workspace.Region,
			Available:  metrics.Available,
			Reason:     metrics.Reason,
			Usage:      metrics.Usage,
			Requests:   metrics.Requests,
			Limits:     metrics.Limits,
			ObservedAt: metrics.ObservedAt,
		}
		for _, container := range metrics.Containers {
			row.Restarts += container.Restarts
		}
		row.Ready = containersReady(metrics.Containers)
		overview.Workspaces = append(overview.Workspaces, row)
		overview.Usage.Add(row.Usage)
		overview.Requests.Add(row.Requests)
		overview.Limits.Add(row.Limits)
		if row.Available {
			overview.Available = true
			overview.Source = "metrics-server"
			if row.ObservedAt != nil && (overview.ObservedAt == nil || row.ObservedAt.After(*overview.ObservedAt)) {
				overview.ObservedAt = row.ObservedAt
			}
		} else if overview.Reason == "" {
			overview.Reason = row.Reason
		}
	}
	if overview.Available {
		overview.Reason = ""
	}
	return overview, nil
}

func containersReady(containers []monitoring.ContainerMetrics) bool {
	if len(containers) == 0 {
		return false
	}
	for _, container := range containers {
		if !container.Ready {
			return false
		}
	}
	return true
}

func validHistoryParams(r *http.Request) bool {
	for _, key := range []string{"range", "step"} {
		value := strings.TrimSpace(r.URL.Query().Get(key))
		if value == "" {
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return false
		}
	}
	return true
}
