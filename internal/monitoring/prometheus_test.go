package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewPrometheusClientRejectsInvalidURL(t *testing.T) {
	for _, input := range []string{"", "ftp://prometheus", "not-a-url"} {
		if _, err := NewPrometheusClient(input, ""); err == nil {
			t.Fatalf("NewPrometheusClient(%q) succeeded", input)
		}
	}
}

func TestHistoryWindowClampsRangeAndStep(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	start, end, step, err := historyWindow(now, "720h", "1s")
	if err != nil {
		t.Fatal(err)
	}
	if !end.Equal(now) {
		t.Fatalf("end = %v", end)
	}
	if end.Sub(start) != maxHistoryRange {
		t.Fatalf("window = %v", end.Sub(start))
	}
	if step < minHistoryStep {
		t.Fatalf("step = %v", step)
	}
	if maxPoints := int64(end.Sub(start) / step); maxPoints > maxHistoryPoints {
		t.Fatalf("points = %d, want <= %d", maxPoints, maxHistoryPoints)
	}
}

func TestHistoryWindowRejectsInvalidValues(t *testing.T) {
	if _, _, _, err := historyWindow(time.Now(), "nope", ""); err == nil {
		t.Fatal("invalid range accepted")
	}
	if _, _, _, err := historyWindow(time.Now(), "1h", "nope"); err == nil {
		t.Fatal("invalid step accepted")
	}
}

func TestWorkspaceHistoryQueriesPrometheus(t *testing.T) {
	seen := []string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		seen = append(seen, r.URL.Query().Get("query"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{map[string]any{
					"values": [][]any{{1700000000, "2.5"}},
				}},
			},
		})
	}))
	defer server.Close()

	client, err := NewPrometheusClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	history, err := client.WorkspaceHistory(context.Background(), "poddy", "poddy-app-0", "poddy-app", "1h", "60s")
	if err != nil {
		t.Fatal(err)
	}
	if !history.Available || len(history.Series) != len(workspaceSeries)+len(storageSeries) {
		t.Fatalf("history = %+v", history)
	}
	if len(seen) != len(workspaceSeries)+len(storageSeries) {
		t.Fatalf("queries = %d", len(seen))
	}
	for _, query := range seen {
		if !strings.Contains(query, `namespace="poddy"`) && !strings.Contains(query, `namespace="poddy",persistentvolumeclaim="poddy-app"`) {
			t.Fatalf("query missing namespace selector: %s", query)
		}
	}
	for _, fragment := range []string{"container_fs_reads_total", "container_fs_writes_total", "container_network_receive_errors_total", "container_network_receive_packets_dropped_total"} {
		found := false
		for _, query := range seen {
			if strings.Contains(query, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("query %q was not sent", fragment)
		}
	}
	if history.Series[0].Points[0].Value != 2.5 {
		t.Fatalf("point = %+v", history.Series[0].Points[0])
	}
}

func TestWorkspaceHistoryReturnsUnavailableOnPrometheusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	history, err := client.WorkspaceHistory(context.Background(), "poddy", "poddy-app-0", "", "1h", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if history.Available || history.Reason != "Prometheus is unavailable" {
		t.Fatalf("history = %+v", history)
	}
}

func TestNamespaceHistoryFiltersWorkspacePods(t *testing.T) {
	queries := []string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{map[string]any{
					"values": [][]any{{1700000000, 1}},
				}},
			},
		})
	}))
	defer server.Close()

	client, err := NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.NamespaceHistory(context.Background(), "poddy", "1h", "60s"); err != nil {
		t.Fatal(err)
	}
	workspaceQueries := 0
	storageQueries := 0
	for _, query := range queries {
		if strings.Contains(query, `pod=~"poddy-.+-0"`) {
			workspaceQueries++
		}
		if strings.Contains(query, "kubelet_volume_stats_") {
			storageQueries++
		}
	}
	if workspaceQueries == 0 {
		t.Fatalf("no query restricts workspace pods: %v", queries)
	}
	if storageQueries == 0 {
		t.Fatalf("no global storage query: %v", queries)
	}
}
