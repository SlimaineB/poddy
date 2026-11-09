package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"poddy/internal/auth"
	"poddy/internal/metadata"
)

type recordingMetadata struct {
	metadata.Nop
	mu          sync.Mutex
	events      []metadata.Event
	auditPage   metadata.AuditPage
	auditFilter metadata.AuditFilter
}

func (r *recordingMetadata) AuditEnabled() bool { return true }

func (r *recordingMetadata) ListAudit(_ context.Context, _ string, filter metadata.AuditFilter) (metadata.AuditPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditFilter = filter
	return r.auditPage, nil
}

func (r *recordingMetadata) RecordAudit(_ context.Context, event metadata.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *recordingMetadata) last() metadata.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return metadata.Event{}
	}
	return r.events[len(r.events)-1]
}

func TestAuditWorkspaceCreateAndDelete(t *testing.T) {
	s := newTestServer("")
	store := &recordingMetadata{}
	s.Metadata = store

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	event := store.last()
	if event.Action != "workspace.create" || event.Outcome != "success" || event.TargetName != "app" || event.Workspace != "app" {
		t.Fatalf("create audit = %+v", event)
	}
	if event.Actor != "local" || event.ActorType != "system" {
		t.Fatalf("create actor = %q/%q", event.Actor, event.ActorType)
	}
	if event.Details["template"] != "base" {
		t.Fatalf("create details = %+v", event.Details)
	}

	rec = request(t, s, http.MethodDelete, "/api/workspaces/app", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	event = store.last()
	if event.Action != "workspace.delete" || event.Outcome != "success" || event.TargetName != "app" {
		t.Fatalf("delete audit = %+v", event)
	}
}

func TestAuditDeniedAndErrorOutcomes(t *testing.T) {
	s := newTestServer("secret")
	store := &recordingMetadata{}
	s.Metadata = store

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": "app"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", rec.Code)
	}
	event := store.last()
	if event.Action != "auth.denied" || event.Outcome != "denied" {
		t.Fatalf("denied audit = %+v", event)
	}

	rec = request(t, s, http.MethodPost, "/api/workspaces/missing/stop", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stop status = %d", rec.Code)
	}
}

func TestAuditRequestMetadata(t *testing.T) {
	s := newTestServer("")
	store := &recordingMetadata{}
	s.Metadata = store

	req := newRequest(http.MethodPost, "/api/workspaces", map[string]any{"name": "app"})
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")
	req.Header.Set("X-Request-Id", "req-123")
	req.Header.Set("User-Agent", "audit-test")
	rec := doRequest(s, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	event := store.last()
	if event.SourceIP != "203.0.113.7" || event.RequestID != "req-123" || event.UserAgent != "audit-test" {
		t.Fatalf("request metadata = %+v", event)
	}
}

func newRequest(method, path string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	return httptest.NewRequest(method, path, reader)
}

func doRequest(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAuditReadRequiresAuditor(t *testing.T) {
	cfg := rbacTestConfig()
	cfg.Auditors = []string{"audit@example.com"}
	s := newRBACServer(t, cfg)
	store := &recordingMetadata{auditPage: metadata.AuditPage{
		Items: []metadata.AuditRecord{{
			ID:         7,
			OccurredAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
			Actor:      "alice@example.com",
			Action:     "workspace.create",
			Outcome:    "success",
			TargetType: "workspace",
			TargetName: "app",
		}},
		Total: 1, Page: 1, PerPage: 25, Pages: 1,
	}}
	s.Metadata = store
	auditor := sessionFor(t, s, auth.User{Username: "audit", Email: "audit@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	rec := requestAs(t, s, alice, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("regular user status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, auditor, http.MethodGet, "/api/audit?action=workspace.create&actor=alice%40example.com&page=2&per_page=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auditor status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page metadata.AuditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Action != "workspace.create" {
		t.Fatalf("page = %+v", page)
	}
	if store.auditFilter.Action != "workspace.create" || store.auditFilter.Actor != "alice@example.com" || store.auditFilter.Page != 2 || store.auditFilter.PerPage != 10 {
		t.Fatalf("filter = %+v", store.auditFilter)
	}
}

func TestAuditReadWithPostgres(t *testing.T) {
	if os.Getenv("PODDY_TEST_DATABASE_URL") == "" {
		t.Skip("set PODDY_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	cfg := rbacTestConfig()
	cfg.Auditors = []string{"audit@example.com"}
	s := newRBACServer(t, cfg)
	store, err := metadata.Open(context.Background(), os.Getenv("PODDY_TEST_DATABASE_URL"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.Metadata = store

	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	auditor := sessionFor(t, s, auth.User{Username: "audit", Email: "audit@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	rec := requestAs(t, s, alice, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("regular user status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, auditor, http.MethodGet, "/api/audit?action=audit.view", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auditor status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page metadata.AuditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total < 1 {
		t.Fatalf("expected at least one audit.view event, got %+v", page)
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/audit?action=workspace.create&outcome=success&page=1&per_page=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin filtered status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuditReadUnavailableWithoutPostgres(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	s.Metadata = metadata.Nop{}
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	rec := requestAs(t, s, admin, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
