package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceEgress(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
		result := []any{}
		switch {
		case strings.Contains(query, "poddy_workspace_network_bytes_total"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"remote_service": "s3", "remote_endpoint": "alice.s3.eu-west-3.amazonaws.com", "bucket": "alice",
					"direction": "egress", "protocol": "tcp", "port": "443", "confidence": "high",
				},
				"value": []any{1700000000, "1024"},
			})
		case strings.Contains(query, "poddy_workspace_network_connections_total"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"remote_service": "s3", "remote_endpoint": "alice.s3.eu-west-3.amazonaws.com", "bucket": "alice",
					"protocol": "tcp", "port": "443", "confidence": "high",
				},
				"value": []any{1700000000, "3"},
			})
		case strings.Contains(query, "poddy_workspace_storage_mount_info"):
			result = append(result, map[string]any{
				"metric": map[string]string{
					"workspace": "alice", "pvc": "poddy-alice", "volume": "pv-alice", "server": "fs-123.efs.eu-west-3.amazonaws.com",
					"path": "/", "fstype": "nfs4", "storage_service": "efs", "access_mode": "ReadWriteMany",
					"provisioner": "efs.csi.aws.com",
				},
				"value": []any{1700000000, "1"},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result":     result,
			},
		})
	}))
	defer server.Close()

	client, err := NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	egress, err := client.WorkspaceEgress(context.Background(), "alice", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !egress.Available || len(egress.Network) != 1 || len(egress.Mounts) != 1 {
		t.Fatalf("egress = %+v", egress)
	}
	network := egress.Network[0]
	if network.Bucket != "alice" || network.Bytes != 1024 || network.Connections != 3 || network.Direction != "egress" {
		t.Fatalf("network = %+v", network)
	}
	if network.BytesPerSecond != 1024.0/3600.0 {
		t.Fatalf("bytes per second = %v", network.BytesPerSecond)
	}
	if egress.Mounts[0].Service != "efs" || egress.Mounts[0].PVC != "poddy-alice" {
		t.Fatalf("mount = %+v", egress.Mounts[0])
	}
	if len(queries) != 3 {
		t.Fatalf("queries = %d", len(queries))
	}
	for _, query := range queries {
		if !strings.Contains(query, `workspace="alice"`) {
			t.Fatalf("query does not filter workspace: %s", query)
		}
		if strings.Contains(query, "poddy_workspace_network_") && !strings.Contains(query, `remote_service!=""`) {
			t.Fatalf("network query does not filter empty remote service: %s", query)
		}
		if strings.Contains(query, "poddy_workspace_storage_mount_info") && !strings.Contains(query, `storage_service!=""`) {
			t.Fatalf("mount query does not filter empty storage service: %s", query)
		}
	}
}

func TestWorkspaceEgressEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": []any{}},
		})
	}))
	defer server.Close()
	client, err := NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	egress, err := client.WorkspaceEgress(context.Background(), "alice", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if egress.Available || !strings.Contains(egress.Reason, "No egress data") {
		t.Fatalf("egress = %+v", egress)
	}
}

func TestWorkspaceEgressInvalidRange(t *testing.T) {
	client, err := NewPrometheusClient("http://prometheus.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.WorkspaceEgress(context.Background(), "alice", "0s"); err == nil {
		t.Fatal("expected invalid range error")
	}
}

func TestWorkspaceEgressPrometheusUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewPrometheusClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	egress, err := client.WorkspaceEgress(context.Background(), "alice", "1h")
	if err == nil {
		t.Fatal("expected error")
	}
	if egress.Available || egress.Reason != "Prometheus is unavailable" {
		t.Fatalf("egress = %+v", egress)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("query took too long")
	}
}
