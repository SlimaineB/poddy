package server

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"poddy/internal/envstore"
	"poddy/internal/k8s"
	"poddy/internal/monitoring"
	"poddy/internal/rbac"
)

// MonitoringConfig controls every monitoring surface independently. The
// server enforces these flags for API routes; the UI only reflects them.
type MonitoringConfig struct {
	Enabled       bool
	Live          bool
	History       bool
	Egress        bool
	Global        bool
	Costs         bool
	CostsForUsers bool
	Pricing       monitoring.Pricing
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
		log.Printf("monitoring history %s: %v", strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
		if history.Reason == "" {
			history.Reason = "Prometheus is unavailable"
		}
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) workspaceEgress(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.Egress {
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
		writeJSON(w, http.StatusOK, monitoring.Egress{
			Available: false,
			Reason:    "Prometheus is not configured",
			Network:   []monitoring.EgressNetwork{},
			Mounts:    []monitoring.EgressMount{},
		})
		return
	}
	egress, err := s.Prometheus.WorkspaceEgress(
		r.Context(),
		name,
		r.URL.Query().Get("range"),
	)
	if err != nil {
		log.Printf("monitoring egress %s: %v", strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
		if egress.Reason == "" {
			egress.Reason = "Prometheus is unavailable"
		}
	}
	if egress.Network == nil {
		egress.Network = []monitoring.EgressNetwork{}
	}
	if egress.Mounts == nil {
		egress.Mounts = []monitoring.EgressMount{}
	}
	writeJSON(w, http.StatusOK, egress)
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
			Name:         workspace.Name,
			Template:     workspace.Template,
			Owner:        workspace.Owner,
			Phase:        workspace.Phase,
			Region:       workspace.Region,
			Available:    metrics.Available,
			Reason:       metrics.Reason,
			Usage:        metrics.Usage,
			Requests:     metrics.Requests,
			Limits:       metrics.Limits,
			ObservedAt:   metrics.ObservedAt,
			StorageBytes: storageBytes(workspace.Storage),
		}
		for _, container := range metrics.Containers {
			row.Restarts += container.Restarts
			if container.OOMKilled {
				row.OOMKilled = true
			}
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

func storageBytes(value string) int64 {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	return quantity.Value()
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

func (s *Server) finOps(w http.ResponseWriter, r *http.Request) {
	if !s.Monitoring.Enabled || !s.Monitoring.Live {
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
	admin := s.isAdmin(id, cfg)

	pricing := s.Monitoring.Pricing
	costVisible := s.Monitoring.Costs && pricing.Configured() && (admin || s.Monitoring.CostsForUsers)
	if !costVisible {
		pricing = monitoring.Pricing{}
	}

	if s.LiveMetrics == nil {
		writeJSON(w, http.StatusOK, monitoring.FinOpsReport{
			Available:   false,
			Reason:      "Live metrics are not configured",
			Admin:       admin,
			CostVisible: costVisible,
			Pricing:     pricing,
			Workspaces:  []monitoring.FinOpsWorkspace{},
		})
		return
	}

	overview, err := s.finOpsOverview(r.Context(), id, admin)
	if err != nil {
		log.Printf("finops overview: %v", err)
		writeJSON(w, http.StatusOK, monitoring.FinOpsReport{
			Available:   false,
			Reason:      "Live metrics are unavailable",
			Admin:       admin,
			CostVisible: costVisible,
			Pricing:     pricing,
			Workspaces:  []monitoring.FinOpsWorkspace{},
		})
		return
	}

	report := monitoring.ComputeFinOps(overview.Workspaces, pricing)
	report.Available = overview.Available
	report.Reason = overview.Reason
	report.Source = overview.Source
	report.ObservedAt = overview.ObservedAt
	report.Admin = admin
	report.CostVisible = costVisible
	s.attributeFinOpsTeams(r.Context(), cfg, report.Workspaces)
	writeJSON(w, http.StatusOK, report)
}

// attributeFinOpsTeams resolves the Poddy teams of each workspace owner so the
// admin view can rank waste per team and member. Directory lookups are indexed
// once to avoid listing users per workspace.
func (s *Server) attributeFinOpsTeams(ctx context.Context, cfg rbac.Config, rows []monitoring.FinOpsWorkspace) {
	if len(rows) == 0 {
		return
	}
	index := map[string]rbac.Identity{}
	if s.Users != nil {
		if list, err := s.Users.List(ctx); err == nil {
			for _, user := range list {
				identity := rbac.Identity{Username: user.Username, Email: user.Email, Groups: user.Groups}
				for _, key := range []string{user.Email, user.Username, user.Subject} {
					key = strings.ToLower(strings.TrimSpace(key))
					if key != "" {
						index[key] = identity
					}
				}
			}
		}
	}
	for i := range rows {
		owner := strings.TrimSpace(rows[i].Owner)
		if owner == "" {
			continue
		}
		identity, ok := index[strings.ToLower(owner)]
		if !ok {
			identity = rbac.Identity{Username: owner}
			if strings.Contains(owner, "@") {
				identity.Email = owner
			}
		}
		rows[i].Teams = envstore.TeamsForIdentity(cfg, identity)
	}
}

func (s *Server) finOpsOverview(ctx context.Context, id *identity, admin bool) (monitoring.Overview, error) {
	if admin {
		if s.Regions != nil {
			overview, problems := s.Regions.MonitoringOverview(ctx, s.hubAdminIdentity())
			for _, problem := range problems {
				log.Printf("finops overview: %v", problem)
			}
			return overview, nil
		}
		return s.localMonitoringOverview(ctx)
	}
	if s.Regions != nil {
		return s.ownedRegionOverview(ctx, id)
	}
	overview, err := s.localMonitoringOverview(ctx)
	if err != nil {
		return monitoring.Overview{}, err
	}
	return filterOverviewByOwner(overview, ownerID(id.User)), nil
}

func (s *Server) ownedRegionOverview(ctx context.Context, id *identity) (monitoring.Overview, error) {
	identity := s.hubIdentity(id, false)
	list, problems := s.Regions.List(ctx, identity)
	for _, problem := range problems {
		log.Printf("finops overview list: %v", problem)
	}
	overview := monitoring.Overview{Workspaces: make([]monitoring.WorkspaceOverview, 0, len(list))}
	for _, workspace := range list {
		row := monitoring.WorkspaceOverview{
			Name:         workspace.Name,
			Template:     workspace.Template,
			Owner:        workspace.Owner,
			Phase:        workspace.Phase,
			Region:       workspace.Region,
			StorageBytes: storageBytes(workspace.Storage),
		}
		client := s.Regions.Client(workspace.Region)
		if client == nil {
			row.Reason = "region is not connected"
			overview.Workspaces = append(overview.Workspaces, row)
			continue
		}
		var metrics monitoring.WorkspaceMetrics
		path := "/api/workspaces/" + url.PathEscape(workspace.Name) + "/metrics"
		if err := client.DoJSON(ctx, http.MethodGet, path, identity, nil, &metrics); err != nil {
			row.Reason = "live metrics are unavailable"
			overview.Workspaces = append(overview.Workspaces, row)
			continue
		}
		row.Available = metrics.Available
		row.Reason = metrics.Reason
		row.Usage = metrics.Usage
		row.Requests = metrics.Requests
		row.Limits = metrics.Limits
		row.ObservedAt = metrics.ObservedAt
		for _, container := range metrics.Containers {
			row.Restarts += container.Restarts
			if container.OOMKilled {
				row.OOMKilled = true
			}
		}
		row.Ready = containersReady(metrics.Containers)
		overview.Workspaces = append(overview.Workspaces, row)
	}
	recomputeOverview(&overview)
	return overview, nil
}

func filterOverviewByOwner(overview monitoring.Overview, owner string) monitoring.Overview {
	filtered := monitoring.Overview{Workspaces: make([]monitoring.WorkspaceOverview, 0, len(overview.Workspaces))}
	for _, row := range overview.Workspaces {
		if strings.EqualFold(row.Owner, owner) {
			filtered.Workspaces = append(filtered.Workspaces, row)
		}
	}
	recomputeOverview(&filtered)
	return filtered
}

func recomputeOverview(overview *monitoring.Overview) {
	overview.Available = false
	overview.Reason = ""
	overview.Source = ""
	overview.ObservedAt = nil
	overview.Usage = monitoring.ResourceUsage{}
	overview.Requests = monitoring.ResourceUsage{}
	overview.Limits = monitoring.ResourceUsage{}
	for _, row := range overview.Workspaces {
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
	if overview.Reason == "" && len(overview.Workspaces) == 0 {
		overview.Reason = "No workspaces found"
	}
}
