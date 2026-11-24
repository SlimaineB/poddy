package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"poddy/internal/k8s"
	"poddy/internal/monitoring"
)

func monitoringTestProvider(t *testing.T) *monitoring.Provider {
	t.Helper()
	return monitoringTestProviderFor(t, k8s.PodName("app"))
}

func monitoringTestProviderFor(t *testing.T, podName string) *monitoring.Provider {
	t.Helper()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	client := metricsfake.NewSimpleClientset()
	metrics := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: "poddy"},
		Timestamp:  metav1.NewTime(now),
		Containers: []metricsv1beta1.ContainerMetrics{{
			Name: k8s.ContainerName,
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("250m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		}},
	}
	if err := client.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("pods"), metrics, "poddy"); err != nil {
		t.Fatal(err)
	}
	return monitoring.NewProviderWithClient(client, "poddy", time.Minute)
}

func TestWorkspaceMetricsEndpoint(t *testing.T) {
	s := newTestServer("")
	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	s.Monitoring = MonitoringConfig{Enabled: true, Live: true, Global: true}
	s.LiveMetrics = monitoringTestProvider(t)
	rec := request(t, s, http.MethodGet, "/api/workspaces/app/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got monitoring.WorkspaceMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || got.Usage.CPU == nil || *got.Usage.CPU != 0.25 {
		t.Fatalf("metrics = %+v", got)
	}
}

func TestWorkspaceMetricsEndpointDisabled(t *testing.T) {
	s := newTestServer("")
	if rec := request(t, s, http.MethodGet, "/api/workspaces/app/metrics", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMonitoringOverviewAggregatesWorkspaces(t *testing.T) {
	s := newTestServer("")
	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	s.Monitoring = MonitoringConfig{Enabled: true, Live: true, Global: true}
	s.LiveMetrics = monitoringTestProvider(t)
	overview, err := s.localMonitoringOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Available || overview.Reason != "" || len(overview.Workspaces) != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	row := overview.Workspaces[0]
	if row.Name != "app" || row.Usage.CPU == nil || *row.Usage.CPU != 0.25 {
		t.Fatalf("row = %+v", row)
	}
}

func TestMonitoringOverviewMatchesLegacyPodByLabel(t *testing.T) {
	s := newTestServer("")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "poddy-legacy",
			Namespace: "poddy",
			Labels: map[string]string{
				"app.kubernetes.io/name": k8s.AppName,
				"poddy.io/workspace":     "legacy",
				"poddy.io/template":      "base",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: k8s.ContainerName,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  k8s.ContainerName,
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	if _, err := s.Workspaces.Client.CoreV1().Pods("poddy").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	s.Monitoring = MonitoringConfig{Enabled: true, Live: true, Global: true}
	s.LiveMetrics = monitoringTestProviderFor(t, "poddy-legacy")

	overview, err := s.localMonitoringOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Available || overview.Reason != "" || len(overview.Workspaces) != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	row := overview.Workspaces[0]
	if row.Name != "legacy" || !row.Available || row.Usage.CPU == nil || *row.Usage.CPU != 0.25 {
		t.Fatalf("row = %+v", row)
	}
}

func TestMonitoringOverviewEndpoint(t *testing.T) {
	s := newTestServer("")
	s.Monitoring = MonitoringConfig{Enabled: true, Live: true, Global: true}
	rec := request(t, s, http.MethodGet, "/api/monitoring/overview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestMonitoringHistoryEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{map[string]any{
					"values": [][]any{{1700000000, "1.5"}},
				}},
			},
		})
	}))
	defer server.Close()

	client, err := monitoring.NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer("")
	s.Monitoring = MonitoringConfig{Enabled: true, History: true, Global: true}
	s.Prometheus = client
	rec := request(t, s, http.MethodGet, "/api/monitoring/history?range=1h", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var history monitoring.History
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if !history.Available || len(history.Series) == 0 {
		t.Fatalf("history = %+v", history)
	}

	rec = request(t, s, http.MethodGet, "/api/monitoring/history?range=0s", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid range status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceEgressEndpoint(t *testing.T) {
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		result := []any{}
		switch {
		case strings.Contains(query, "poddy_workspace_network_bytes_total"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"remote_service": "s3", "remote_endpoint": "app.s3.eu-west-3.amazonaws.com", "bucket": "app",
					"direction": "egress", "protocol": "tcp", "port": "443", "confidence": "high",
				},
				"value": []any{1700000000, "2048"},
			})
		case strings.Contains(query, "poddy_workspace_network_connections_total"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"remote_service": "s3", "remote_endpoint": "app.s3.eu-west-3.amazonaws.com", "bucket": "app",
					"protocol": "tcp", "port": "443", "confidence": "high",
				},
				"value": []any{1700000000, "2"},
			})
		case strings.Contains(query, "poddy_workspace_storage_mount_info"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"pvc": "poddy-app", "volume": "pv-app", "storage_service": "efs",
					"server": "fs-123.efs.eu-west-3.amazonaws.com", "path": "/", "fstype": "nfs4",
				},
				"value": []any{1700000000, "1"},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": result},
		})
	}))
	defer prometheus.Close()

	client, err := monitoring.NewPrometheusClient(prometheus.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer("")
	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	s.Monitoring = MonitoringConfig{Enabled: true, Egress: true}
	s.Prometheus = client

	rec := request(t, s, http.MethodGet, "/api/workspaces/app/monitoring/egress?range=1h", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var egress monitoring.Egress
	if err := json.Unmarshal(rec.Body.Bytes(), &egress); err != nil {
		t.Fatal(err)
	}
	if !egress.Available || len(egress.Network) != 1 || len(egress.Mounts) != 1 {
		t.Fatalf("egress = %+v", egress)
	}
	if egress.Network[0].Bucket != "app" || egress.Network[0].Bytes != 2048 || egress.Network[0].Connections != 2 {
		t.Fatalf("network = %+v", egress.Network[0])
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces/app/monitoring/egress?range=0s", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid range status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
