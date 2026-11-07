package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"poddy/internal/auth"
)

func desktopTestServer(t *testing.T) *Server {
	t.Helper()
	s := newRBACServer(t, rbacTestConfig())
	s.DesktopSecret = []byte("desktop-test-secret")
	s.SSHHost = "portal.example"
	s.SSHPort = "2222"
	return s
}

func decodeDesktopPayload(t *testing.T, rec *httptest.ResponseRecorder) desktopPayload {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(resp["url"])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(u.Query().Get("payload"))
	if err != nil {
		t.Fatal(err)
	}
	var payload desktopPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestDesktopTicketRoundTrip(t *testing.T) {
	s := &Server{DesktopSecret: []byte("secret")}
	ticket, err := s.signDesktopTicket(desktopClaims{
		Owner:     "alice@example.com",
		Workspace: "alice-ws",
		Audience:  desktopTicketAudience,
		Expires:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.verifyDesktopTicket(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Owner != "alice@example.com" || claims.Workspace != "alice-ws" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestDesktopTicketRejectsExpiredAndTampered(t *testing.T) {
	s := &Server{DesktopSecret: []byte("secret")}
	expired, err := s.signDesktopTicket(desktopClaims{
		Owner:     "alice@example.com",
		Workspace: "alice-ws",
		Audience:  desktopTicketAudience,
		Expires:   time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyDesktopTicket(expired); err == nil {
		t.Fatal("expired ticket accepted")
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
	other := &Server{DesktopSecret: []byte("other")}
	if _, err := other.verifyDesktopTicket(ticket); err == nil {
		t.Fatal("ticket signed with another secret accepted")
	}
}

func TestDesktopIsOwnerOrAdminScoped(t *testing.T) {
	s := desktopTestServer(t)
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/desktop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice desktop = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeDesktopPayload(t, rec)
	claims, err := s.verifyDesktopTicket(payload.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Owner != "alice@example.com" || payload.Workspace != "alice-ws" {
		t.Fatalf("alice payload = %+v claims = %+v", payload, claims)
	}

	if rec := requestAs(t, s, bob, http.MethodPost, "/api/workspaces/alice-ws/desktop", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob desktop = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodPost, "/api/workspaces/alice-ws/desktop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin desktop = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload = decodeDesktopPayload(t, rec)
	claims, err = s.verifyDesktopTicket(payload.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Owner != "alice@example.com" {
		t.Fatalf("admin ticket owner = %q, want workspace owner", claims.Owner)
	}
}
