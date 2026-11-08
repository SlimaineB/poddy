package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestVolumeReadiness(t *testing.T) {
	tests := []struct {
		name    string
		volume  k8s.Volume
		restore bool
		status  int
		message string
	}{
		{
			name:   "stopped backup",
			volume: k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Stopped"},
		},
		{
			name:   "running backup",
			volume: k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Running", AccessModes: []string{"ReadWriteOnce"}},
		},
		{
			name:    "running read write once pod backup",
			volume:  k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Running", AccessModes: []string{"ReadWriteOncePod"}},
			status:  http.StatusConflict,
			message: "live backup is not supported",
		},
		{
			name:    "pending workspace backup",
			volume:  k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Pending"},
			status:  http.StatusConflict,
			message: "wait for the workspace",
		},
		{
			name:    "unbound volume",
			volume:  k8s.Volume{Phase: "Pending"},
			status:  http.StatusConflict,
			message: "not bound",
		},
		{
			name:    "restore into running workspace",
			volume:  k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Running"},
			restore: true,
			status:  http.StatusConflict,
			message: "stop the workspace",
		},
		{
			name:    "restore into stopped workspace",
			volume:  k8s.Volume{Phase: "Bound", Assigned: true, WorkspacePhase: "Stopped"},
			restore: true,
		},
		{
			name:    "restore into unassigned volume",
			volume:  k8s.Volume{Phase: "Bound"},
			restore: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ok := volumeReadyForBackup(rec, test.volume)
			if test.restore {
				ok = volumeReadyForRestore(rec, test.volume)
			}
			if ok != (test.status == 0) {
				t.Fatalf("ready = %v, status = %d, body = %s", ok, rec.Code, rec.Body.String())
			}
			if test.status != 0 && rec.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.status, rec.Body.String())
			}
			if test.message != "" && !strings.Contains(rec.Body.String(), test.message) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), test.message)
			}
		})
	}
}

func TestRestoreIntoAnotherVolumeValidation(t *testing.T) {
	s := newTestServer("")
	store, err := backup.New(backup.Config{
		Endpoint:  "http://127.0.0.1:1",
		Bucket:    "backups",
		AccessKey: "access",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Backups = store

	ctx := context.Background()
	for _, name := range []string{"app", "other"} {
		rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": name, "storage": "1Gi"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		if _, err := s.Workspaces.Stop(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	key := store.Key("poddy-app", time.Now())

	rec := request(t, s, http.MethodPost, "/api/volumes/poddy-missing/restore", map[string]any{
		"key": key, "target": "poddy-other",
	})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "source volume") {
		t.Fatalf("missing source status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, s, http.MethodPost, "/api/volumes/poddy-app/restore", map[string]any{
		"key": key, "target": "poddy-missing",
	})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "target volume") {
		t.Fatalf("missing target status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, s, http.MethodPost, "/api/volumes/poddy-app/restore", map[string]any{
		"key": "not-a-backup", "target": "poddy-other",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid key status = %d, body = %s", rec.Code, rec.Body.String())
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8s.PodName("other"),
			Namespace: "poddy",
			Labels: map[string]string{
				"app.kubernetes.io/name": "poddy",
				"poddy.io/workspace":     "other",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := s.Workspaces.Client.CoreV1().Pods("poddy").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	rec = request(t, s, http.MethodPost, "/api/volumes/poddy-app/restore", map[string]any{
		"key": key, "target": "poddy-other",
	})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stop the workspace") {
		t.Fatalf("running target status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHubRestoresIntoAnotherRegion(t *testing.T) {
	var restoredPath, restoredBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/volumes/poddy-target/restore-from" {
			raw, _ := io.ReadAll(r.Body)
			restoredPath = r.URL.Path
			restoredBody = string(raw)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(target.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/volumes" {
			writeJSON(w, http.StatusOK, map[string]any{
				"volumes": []k8s.Volume{{
					Name:     "poddy-source",
					Phase:    "Bound",
					Assigned: true,
					Owner:    "local",
				}},
				"backup": true,
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(source.Close)

	registry, err := region.Parse([]byte(
		"regions:\n" +
			"- name: eu\n  url: " + source.URL + "\n  key: shared\n  default: true\n" +
			"- name: us\n  url: " + target.URL + "\n  key: shared\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	hub := newTestServer("")
	hub.Regions = region.NewHub(registry)

	key := "poddy-backups/poddy-source/20260101T000000Z.tar.gz"
	rec := request(t, hub, http.MethodPost, "/api/volumes/poddy-source/restore?region=eu", map[string]any{
		"key": key, "target": "poddy-target", "targetRegion": "us", "clean": true,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("restore status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if restoredPath != "/api/volumes/poddy-target/restore-from" {
		t.Fatalf("restored path = %q", restoredPath)
	}
	if !strings.Contains(restoredBody, `"source":"poddy-source"`) ||
		!strings.Contains(restoredBody, `"key":"`+key+`"`) ||
		!strings.Contains(restoredBody, `"clean":true`) {
		t.Fatalf("restored body = %s", restoredBody)
	}
}

func TestRestoreFromIsHiddenOnHub(t *testing.T) {
	hub := newTestServer("")
	hub.Regions = region.NewHub(nil)
	rec := request(t, hub, http.MethodPost, "/api/volumes/poddy-target/restore-from", map[string]any{
		"source": "poddy-source",
		"key":    "poddy-backups/poddy-source/20260101T000000Z.tar.gz",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
