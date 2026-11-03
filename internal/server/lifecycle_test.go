package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/auth"
)

func TestWorkspaceLifecycleAPI(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}

	if rec := requestAs(t, s, bob, http.MethodPost, "/api/workspaces/alice-ws/stop", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob stop = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/stop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice stop = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws["phase"] != "Stopped" {
		t.Fatalf("stop response = %+v", ws)
	}

	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/stop", nil); rec.Code != http.StatusConflict {
		t.Fatalf("stop stopped = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, alice, http.MethodGet, "/api/workspaces", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["phase"] != "Stopped" || list[0]["persistent"] != true {
		t.Fatalf("list = %+v", list)
	}

	if rec := requestAs(t, s, bob, http.MethodPost, "/api/workspaces/alice-ws/start", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob start = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/start", nil); rec.Code != http.StatusOK {
		t.Fatalf("alice start = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/start", nil); rec.Code != http.StatusConflict {
		t.Fatalf("start running = %d, body = %s", rec.Code, rec.Body.String())
	}

	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/restart", nil); rec.Code != http.StatusOK {
		t.Fatalf("alice restart = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, admin, http.MethodPost, "/api/workspaces/alice-ws/stop", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin stop = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, admin, http.MethodDelete, "/api/workspaces/alice-ws", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete stopped = %d, body = %s", rec.Code, rec.Body.String())
	}
}
