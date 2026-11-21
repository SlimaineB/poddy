package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDesktopMeshConfig(t *testing.T) {
	s := &Server{
		DesktopSecret:     []byte("desktop-test-secret"),
		MeshEnabled:       true,
		MeshControlURL:    "http://headscale.internal:8080",
		MeshLoginURL:      "https://mesh.example.com",
		MeshClientAuthKey: "mesh-client-key",
	}
	ticket, err := s.signDesktopTicket(desktopClaims{
		Owner:     "alice@example.com",
		Workspace: "alice-ws",
		Audience:  desktopTicketAudience,
		Expires:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/desktop/mesh?ticket="+url.QueryEscape(ticket), nil)
	rec := httptest.NewRecorder()
	s.desktopMesh(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg meshClientConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ControlURL != s.MeshControlURL || cfg.AuthKey != s.MeshClientAuthKey ||
		cfg.Workspace != "alice-ws" || cfg.SSHHost != "ws-alice-ws" {
		t.Fatalf("mesh config = %+v", cfg)
	}
}

func TestDesktopMeshRejectsBadTicket(t *testing.T) {
	s := &Server{
		DesktopSecret:     []byte("desktop-test-secret"),
		MeshEnabled:       true,
		MeshControlURL:    "http://headscale.internal:8080",
		MeshClientAuthKey: "mesh-client-key",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/desktop/mesh?ticket=bad", nil)
	rec := httptest.NewRecorder()
	s.desktopMesh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestDesktopMeshDisabled(t *testing.T) {
	s := &Server{DesktopSecret: []byte("desktop-test-secret")}
	req := httptest.NewRequest(http.MethodGet, "/api/desktop/mesh", nil)
	rec := httptest.NewRecorder()
	s.desktopMesh(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
