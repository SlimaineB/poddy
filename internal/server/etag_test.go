package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListETagAndNotModified(t *testing.T) {
	s := newTestServer("")
	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	first := request(t, s, http.MethodGet, "/api/workspaces", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("list status = %d", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("list response has no ETag")
	}
	if cache := first.Header().Get("Cache-Control"); !strings.Contains(cache, "no-cache") {
		t.Fatalf("Cache-Control = %q", cache)
	}

	for _, header := range []string{etag, "W/" + etag} {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
		req.Header.Set("If-None-Match", header)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Fatalf("If-None-Match %q status = %d", header, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("304 body = %q", rec.Body.String())
		}
	}

	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "other"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("changed list status = %d", rec.Code)
	}
	if next := rec.Header().Get("ETag"); next == "" || next == etag {
		t.Fatalf("ETag did not change: %q then %q", etag, next)
	}
}
