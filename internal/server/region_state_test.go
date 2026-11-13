package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/rbac"
	"poddy/internal/region"
)

func regionTestServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer("")
	registry, err := region.Parse([]byte("regions:\n  - name: eu\n    url: http://eu.invalid\n    key: region-key\n"))
	if err != nil {
		t.Fatal(err)
	}
	s.Regions = region.NewHub(registry)
	return s
}

func regionRequest(t *testing.T, s *Server, key, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	token, err := region.SignIdentity(key, region.Identity{
		User:   auth.User{Username: "region", Subject: "eu"},
		Admin:  true,
		Region: "eu",
	})
	if err != nil {
		t.Fatal(err)
	}
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set(region.RegionNameHeader, "eu")
	req.Header.Set(region.IdentityHeader, token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRegionControlPlaneRejectsUnknownRegion(t *testing.T) {
	s := regionTestServer(t)
	rec := regionRequest(t, s, "wrong-key", http.MethodGet, "/api/internal/region/access", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRegionControlPlaneAccessFlow(t *testing.T) {
	s := regionTestServer(t)
	ctx := context.Background()
	if _, err := s.Access.Request(ctx, "alice-ws", "admin@example.com"); err != nil {
		t.Fatal(err)
	}

	rec := regionRequest(t, s, "region-key", http.MethodGet, "/api/internal/region/access", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("load = %d, body = %s", rec.Code, rec.Body.String())
	}
	var state struct {
		Requests map[string]map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Requests["alice-ws"]["admin@example.com"]; !ok {
		t.Fatalf("requests = %+v", state.Requests)
	}

	rec = regionRequest(t, s, "region-key", http.MethodPost, "/api/internal/region/access/alice-ws/approve", map[string]any{
		"actor": "admin@example.com", "approver": "alice@example.com",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = regionRequest(t, s, "region-key", http.MethodPost, "/api/internal/region/access/alice-ws/request", map[string]any{
		"actor": "admin@example.com",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("request = %d, body = %s", rec.Code, rec.Body.String())
	}
	var granted struct {
		Granted bool `json:"granted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &granted); err != nil {
		t.Fatal(err)
	}
	if !granted.Granted {
		t.Fatal("expected an active grant after approval")
	}
}

func TestRegionControlPlaneQuotas(t *testing.T) {
	s := regionTestServer(t)
	limit := 3
	if err := s.Quota.SetUserQuota(context.Background(), "alice@example.com", rbac.Quota{MaxWorkspaces: &limit}); err != nil {
		t.Fatal(err)
	}

	rec := regionRequest(t, s, "region-key", http.MethodGet, "/api/internal/region/quotas/users", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("users = %d, body = %s", rec.Code, rec.Body.String())
	}
	var quotas map[string]rbac.Quota
	if err := json.Unmarshal(rec.Body.Bytes(), &quotas); err != nil {
		t.Fatal(err)
	}
	if quotas["alice@example.com"].MaxWorkspaces == nil || *quotas["alice@example.com"].MaxWorkspaces != limit {
		t.Fatalf("quotas = %+v", quotas)
	}

	rec = regionRequest(t, s, "region-key", http.MethodPut, "/api/internal/region/quotas/users/bob@example.com", rbac.Quota{MaxWorkspaces: &limit})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set = %d, body = %s", rec.Code, rec.Body.String())
	}
	quotas, err := s.Quota.UserQuotas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quotas["bob@example.com"].MaxWorkspaces == nil {
		t.Fatalf("stored quotas = %+v", quotas)
	}
}
