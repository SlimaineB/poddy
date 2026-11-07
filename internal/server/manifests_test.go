package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/k8s"
)

func TestWorkspaceManifestsAdminOnly(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}

	if rec := requestAs(t, s, alice, http.MethodGet, "/api/workspaces/"+ws.Name+"/manifests", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("owner manifests status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/workspaces/"+ws.Name+"/manifests", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin manifests status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var manifests map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &manifests); err != nil {
		t.Fatal(err)
	}
	pod, _ := manifests["pod"].(string)
	pvc, _ := manifests["pvc"].(string)
	if !strings.Contains(pod, "kind: Pod") || !strings.Contains(pod, "name: poddy-"+ws.Name) {
		t.Fatalf("pod manifest = %s", pod)
	}
	if !strings.Contains(pvc, "kind: PersistentVolumeClaim") || !strings.Contains(pvc, "name: poddy-"+ws.Name) {
		t.Fatalf("pvc manifest = %s", pvc)
	}
	if manifests["podSource"] != "live" || manifests["template"] != "base" {
		t.Fatalf("metadata = %+v", manifests)
	}
}
