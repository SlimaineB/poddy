package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"poddy/internal/backup"
	"poddy/internal/k8s"
	"poddy/internal/region"
)

func TestVolumesListAndDeleteUnassigned(t *testing.T) {
	s := newTestServer("")

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "storage": "1Gi"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	quantity := resource.MustParse("2Gi")
	_, err := s.Workspaces.Client.CoreV1().PersistentVolumeClaims("poddy").Create(context.Background(), &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "poddy-orphan",
			Labels: map[string]string{
				"app.kubernetes.io/name":       "poddy",
				"app.kubernetes.io/managed-by": "poddy",
				"poddy.io/workspace":           "orphan",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: quantity},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rec = request(t, s, http.MethodGet, "/api/volumes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("volumes status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Volumes []k8s.Volume  `json:"volumes"`
		Summary volumeSummary `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Volumes) != 2 {
		t.Fatalf("volumes = %+v", result.Volumes)
	}
	var assigned, orphan *k8s.Volume
	for i := range result.Volumes {
		switch result.Volumes[i].Name {
		case "poddy-app":
			assigned = &result.Volumes[i]
		case "poddy-orphan":
			orphan = &result.Volumes[i]
		}
	}
	if assigned == nil || !assigned.Assigned || assigned.Owner != "local" {
		t.Fatalf("assigned volume = %+v", assigned)
	}
	if orphan == nil || orphan.Assigned {
		t.Fatalf("orphan volume = %+v", orphan)
	}
	if result.Summary.Total != 2 || result.Summary.Assigned != 1 || result.Summary.Unassigned != 1 {
		t.Fatalf("summary = %+v", result.Summary)
	}

	rec = request(t, s, http.MethodDelete, "/api/volumes/poddy-app", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete assigned status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, s, http.MethodDelete, "/api/volumes/poddy-orphan", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete orphan status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHubAggregatesVolumes(t *testing.T) {
	regionServer := func(name string) *httptest.Server {
		server := newTestServer("")
		server.RegionMode = true
		server.RegionKey = "shared"
		server.RegionName = name
		server.Workspaces.Region = name
		store, err := backup.New(backup.Config{
			Endpoint:  "http://127.0.0.1:1",
			Bucket:    "backups",
			AccessKey: "access",
			SecretKey: "secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		server.Backups = store
		backend := httptest.NewServer(server.Handler())
		t.Cleanup(backend.Close)
		return backend
	}
	eu := regionServer("eu")
	us := regionServer("us")

	registry, err := region.Parse([]byte(
		"regions:\n" +
			"- name: eu\n  url: " + eu.URL + "\n  key: shared\n  default: true\n" +
			"- name: us\n  url: " + us.URL + "\n  key: shared\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	hub := newTestServer("")
	hub.Regions = region.NewHub(registry)

	for _, req := range []map[string]any{
		{"name": "app-eu", "region": "eu", "storage": "1Gi"},
		{"name": "app-us", "region": "us", "storage": "1Gi"},
	} {
		rec := request(t, hub, http.MethodPost, "/api/workspaces", req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %v status = %d, body = %s", req, rec.Code, rec.Body.String())
		}
	}

	rec := request(t, hub, http.MethodGet, "/api/volumes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("volumes status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Volumes []k8s.Volume  `json:"volumes"`
		Summary volumeSummary `json:"summary"`
		Backup  bool          `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Backup {
		t.Fatal("backup capability not aggregated from regions")
	}
	if len(result.Volumes) != 2 || result.Summary.Total != 2 {
		t.Fatalf("volumes = %+v summary = %+v", result.Volumes, result.Summary)
	}
	regions := map[string]bool{}
	for _, volume := range result.Volumes {
		regions[volume.Region] = true
		if !volume.Assigned {
			t.Fatalf("volume not assigned: %+v", volume)
		}
	}
	if !regions["eu"] || !regions["us"] {
		t.Fatalf("regions = %+v", regions)
	}

	rec = request(t, hub, http.MethodGet, "/api/volumes?region=eu", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered volumes status = %d, body = %s", rec.Code, rec.Body.String())
	}
	result.Volumes = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Volumes) != 1 || result.Volumes[0].Region != "eu" {
		t.Fatalf("filtered volumes = %+v", result.Volumes)
	}
}
