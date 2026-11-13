package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/rbac"
)

func serverQuotaIntPtr(value int) *int {
	return &value
}

func TestUserQuotaEndpoints(t *testing.T) {
	s := newRBACServer(t, rbac.Config{Admins: []string{"admin@example.com"}})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	if rec := requestAs(t, s, alice, http.MethodGet, "/api/user-quotas", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list = %d", rec.Code)
	}

	rec := requestAs(t, s, admin, http.MethodPut, "/api/user-quotas/alice%40example.com", map[string]any{
		"maxWorkspaces": 2,
		"cpu":           "4",
		"memory":        "8Gi",
		"storage":       "20Gi",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put quota = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/user-quotas", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list quota = %d, body = %s", rec.Code, rec.Body.String())
	}
	var quotas []userQuotaInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &quotas); err != nil {
		t.Fatal(err)
	}
	if len(quotas) != 1 || quotas[0].Identity != "alice@example.com" || quotas[0].Quota.MaxWorkspaces == nil || *quotas[0].Quota.MaxWorkspaces != 2 {
		t.Fatalf("quotas = %+v", quotas)
	}
	if quotas[0].Quota.CPU != "4" || quotas[0].Quota.Memory != "8Gi" || quotas[0].Quota.Storage != "20Gi" {
		t.Fatalf("quota fields = %+v", quotas[0].Quota)
	}

	if rec := requestAs(t, s, admin, http.MethodPut, "/api/user-quotas/alice%40example.com", map[string]any{"maxWorkspaces": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative quota = %d", rec.Code)
	}
	if rec := requestAs(t, s, admin, http.MethodPut, "/api/user-quotas/%2A", map[string]any{"cpu": "1"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("wildcard quota = %d", rec.Code)
	}

	if rec := requestAs(t, s, admin, http.MethodDelete, "/api/user-quotas/alice%40example.com", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete quota = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored, err := s.Quota.UserQuotas(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("user quotas after delete = %+v", stored)
	}
}

func TestQuotaEnforcedOnCreate(t *testing.T) {
	s := newRBACServer(t, rbac.Config{Admins: []string{"admin@example.com"}})
	if err := s.Quota.SetUserQuota(t.Context(), "alice@example.com", rbac.Quota{MaxWorkspaces: serverQuotaIntPtr(1)}); err != nil {
		t.Fatal(err)
	}
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "one", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "two", "template": "base"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "workspace quota exceeded") {
		t.Fatalf("quota error = %s", rec.Body.String())
	}
}

func TestTeamQuotaInheritedOnCreate(t *testing.T) {
	s := newRBACServer(t, rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com"}},
		},
	})
	if err := s.Quota.SetTeamQuota(t.Context(), "backend", rbac.Quota{MaxWorkspaces: serverQuotaIntPtr(1)}); err != nil {
		t.Fatal(err)
	}
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "one", "template": "base"}); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "two", "template": "base"}); rec.Code != http.StatusConflict {
		t.Fatalf("second create = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTeamQuotaUpdate(t *testing.T) {
	s := newRBACServer(t, rbac.Config{Admins: []string{"admin@example.com"}})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, admin, http.MethodPut, "/api/teams/backend", map[string]any{
		"members": []string{"alice@example.com"},
		"quota":   map[string]any{"maxWorkspaces": 2, "cpu": "4"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create team = %d, body = %s", rec.Code, rec.Body.String())
	}
	var team teamInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	if team.Quota == nil || team.Quota.MaxWorkspaces == nil || *team.Quota.MaxWorkspaces != 2 || team.Quota.CPU != "4" {
		t.Fatalf("team quota = %+v", team.Quota)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/teams/backend", map[string]any{"members": []string{"alice@example.com"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update members = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	if team.Quota == nil || team.Quota.CPU != "4" {
		t.Fatalf("quota should be preserved: %+v", team.Quota)
	}

	if rec := requestAs(t, s, admin, http.MethodPut, "/api/teams/backend", map[string]any{"members": []string{}, "quota": map[string]any{"maxWorkspaces": -1}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative team quota = %d", rec.Code)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/teams/backend", map[string]any{"members": []string{}, "quota": map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear quota = %d, body = %s", rec.Code, rec.Body.String())
	}
	team = teamInfo{}
	if err := json.Unmarshal(rec.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	if team.Quota != nil {
		t.Fatalf("quota should be cleared: %+v", team.Quota)
	}
}
