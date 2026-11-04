package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/metadata"
)

type recordingMetadata struct {
	metadata.Nop
	mu      sync.Mutex
	events  []metadata.Event
	entries []metadata.AuditEntry
}

func (r *recordingMetadata) ListAudit(_ context.Context, _ metadata.AuditFilter) ([]metadata.AuditEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]metadata.AuditEntry(nil), r.entries...), nil
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

func TestAuditLogRead(t *testing.T) {
	s := newTestServer("")
	s.Metadata = &recordingMetadata{entries: []metadata.AuditEntry{{
		ID:      7,
		Action:  "workspace.create",
		Outcome: "success",
	}}}

	rec := request(t, s, http.MethodGet, "/api/audit?limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Events []metadata.AuditEntry `json:"events"`
		Limit  int                   `json:"limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Limit != 10 || len(body.Events) != 1 || body.Events[0].ID != 7 {
		t.Fatalf("audit body = %+v", body)
	}
}

func TestAuditLogDisabled(t *testing.T) {
	s := newTestServer("")
	rec := request(t, s, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("audit disabled status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuditLogRequiresAdmin(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	s.Metadata = &recordingMetadata{}

	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	rec := requestAs(t, s, dev, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin audit status = %d, body = %s", rec.Code, rec.Body.String())
	}

	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	rec = requestAs(t, s, admin, http.MethodGet, "/api/audit", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
