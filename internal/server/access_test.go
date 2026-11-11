package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"poddy/internal/auth"
	"poddy/internal/k8s"
)

func TestWorkspaceAccessApprovalFlow(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}

	access := func(cookie *http.Cookie) k8s.WorkspaceAccess {
		t.Helper()
		rec := requestAs(t, s, cookie, http.MethodGet, "/api/workspaces/alice-ws/access", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get access = %d, body = %s", rec.Code, rec.Body.String())
		}
		var info k8s.WorkspaceAccess
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info
	}

	info := access(admin)
	if info.Allowed || info.IsOwner || !info.IsAdmin {
		t.Fatalf("admin access before approval = %+v", info)
	}

	rec = requestAs(t, s, admin, http.MethodPost, "/api/workspaces/alice-ws/access/request", map[string]any{})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d, body = %s", rec.Code, rec.Body.String())
	}

	info = access(alice)
	if !info.IsOwner || !info.Allowed || len(info.PendingRequests) != 1 || info.PendingRequests[0] != "admin@example.com" {
		t.Fatalf("owner access with request = %+v", info)
	}

	rec = requestAs(t, s, bob, http.MethodPost, "/api/workspaces/alice-ws/access/approve", map[string]any{"actor": "admin@example.com"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner approve = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/access/approve", map[string]any{"actor": "admin@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner approve = %d, body = %s", rec.Code, rec.Body.String())
	}

	info = access(admin)
	if !info.Allowed || info.Pending || info.GrantExpiresAt == nil {
		t.Fatalf("admin access after approval = %+v", info)
	}
	if len(info.Grants) != 1 || info.Grants[0].Actor != "admin@example.com" {
		t.Fatalf("active grants = %+v", info.Grants)
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/access/revoke", map[string]any{"actor": "admin@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner revoke = %d, body = %s", rec.Code, rec.Body.String())
	}
	info = access(admin)
	if info.Allowed || info.Pending {
		t.Fatalf("admin access after revoke = %+v", info)
	}
}

func TestWorkspaceAccessPresence(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}
	now := time.Now()
	if err := s.accessStore().SetPresence(context.Background(), "alice-ws", "s1", k8s.Presence{
		Actor: "alice@example.com", Kind: "terminal", Since: now, LastSeen: now,
	}); err != nil {
		t.Fatal(err)
	}

	rec = requestAs(t, s, alice, http.MethodGet, "/api/workspaces/alice-ws/access", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get access = %d, body = %s", rec.Code, rec.Body.String())
	}
	var info k8s.WorkspaceAccess
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Presence) != 1 || info.Presence[0].Actor != "alice@example.com" {
		t.Fatalf("presence = %+v", info.Presence)
	}
	if len(info.Presence[0].Kinds) != 1 || info.Presence[0].Kinds[0] != "terminal" {
		t.Fatalf("presence kinds = %+v", info.Presence[0].Kinds)
	}
}

func TestAdminKeepsNonInteractiveAccess(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Details, logs and manifests are metadata: admins keep them without
	// asking the owner. Only terminal/IDE/ports/SSH require approval.
	if rec := requestAs(t, s, admin, http.MethodGet, "/api/workspaces/alice-ws", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin details = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, admin, http.MethodGet, "/api/workspaces/alice-ws/manifests", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin manifests = %d, body = %s", rec.Code, rec.Body.String())
	}
}
