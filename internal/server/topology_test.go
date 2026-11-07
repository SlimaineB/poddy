package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/auth"
)

func TestTopologyGroupsByUserAndIncludesVolumes(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"}); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec := requestAs(t, s, alice, http.MethodGet, "/api/topology", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice topology = %d, body = %s", rec.Code, rec.Body.String())
	}
	var aliceResp topologyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &aliceResp); err != nil {
		t.Fatal(err)
	}
	if len(aliceResp.Users) != 1 || aliceResp.Users[0].Identity != "alice@example.com" || !aliceResp.Users[0].Current {
		t.Fatalf("alice users = %+v", aliceResp.Users)
	}
	if aliceResp.Totals.Workspaces != 1 || aliceResp.Totals.Volumes != 1 {
		t.Fatalf("alice totals = %+v", aliceResp.Totals)
	}
	resources := aliceResp.Users[0].Workspaces[0].Resources
	if len(resources) != 1 || resources[0].Kind != "PersistentVolumeClaim" || resources[0].Name != "poddy-alice-ws" {
		t.Fatalf("alice resources = %+v", resources)
	}
	if resources[0].Capacity != "5Gi" {
		t.Fatalf("capacity = %q", resources[0].Capacity)
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/topology", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin topology = %d, body = %s", rec.Code, rec.Body.String())
	}
	var adminResp topologyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatal(err)
	}
	if len(adminResp.Users) != 1 {
		t.Fatalf("admin users = %+v", adminResp.Users)
	}
	if adminResp.Users[0].Identity != "alice@example.com" || adminResp.Users[0].Current {
		t.Fatalf("admin user = %+v", adminResp.Users[0])
	}
	if adminResp.Totals.Workspaces != 1 || adminResp.Totals.Volumes != 1 {
		t.Fatalf("admin totals = %+v", adminResp.Totals)
	}
}
