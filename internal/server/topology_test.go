package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/auth"
)

func TestTopologySearchRecalculatesTotals(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	for _, item := range []struct {
		cookie *http.Cookie
		name   string
	}{
		{alice, "alice-ws"},
		{bob, "bob-ws"},
	} {
		if rec := requestAs(t, s, item.cookie, http.MethodPost, "/api/workspaces", map[string]any{"name": item.name, "template": "base"}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d, body = %s", item.name, rec.Code, rec.Body.String())
		}
	}

	rec := requestAs(t, s, admin, http.MethodGet, "/api/topology?q=alice&page=1&per_page=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("topology search = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp topologyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Users) != 1 || resp.Users[0].Identity != "alice@example.com" {
		t.Fatalf("users = %+v", resp.Users)
	}
	if resp.Total != 1 || resp.Totals.Users != 1 || resp.Totals.Workspaces != 1 || resp.Totals.Volumes != 1 {
		t.Fatalf("filtered totals = %+v (total %d)", resp.Totals, resp.Total)
	}
}

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
