package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/dotfiles"
	"poddy/internal/rbac"
)

func TestDotfilesEndpoints(t *testing.T) {
	s := newRBACServer(t, rbac.Config{Admins: []string{"admin@example.com"}})
	s.GlobalDotfiles = dotfiles.Config{URL: "https://example.com/global.git", Branch: "main"}
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "Alice@Example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	rec := requestAs(t, s, alice, http.MethodGet, "/api/me/dotfiles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get global = %d, body = %s", rec.Code, rec.Body.String())
	}
	var settings dotfilesSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.URL != s.GlobalDotfiles.URL || settings.Source != dotfiles.SourceGlobal {
		t.Fatalf("global settings = %+v", settings)
	}

	rec = requestAs(t, s, alice, http.MethodPut, "/api/me/dotfiles", map[string]any{
		"url":    "https://github.com/alice/dotfiles.git",
		"branch": "work",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.URL != "https://github.com/alice/dotfiles.git" || settings.Branch != "work" || settings.Source != dotfiles.SourceUser {
		t.Fatalf("user settings = %+v", settings)
	}

	rec = requestAs(t, s, bob, http.MethodGet, "/api/me/dotfiles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get bob = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.URL != s.GlobalDotfiles.URL || settings.Source != dotfiles.SourceGlobal {
		t.Fatalf("bob should inherit global: %+v", settings)
	}

	rec = requestAs(t, s, alice, http.MethodPut, "/api/me/dotfiles", map[string]any{
		"url": "https://token@example.com/dotfiles.git",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid URL = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, alice, http.MethodDelete, "/api/me/dotfiles", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = requestAs(t, s, alice, http.MethodGet, "/api/me/dotfiles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get after delete = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Source != dotfiles.SourceGlobal || settings.URL != s.GlobalDotfiles.URL {
		t.Fatalf("settings after delete = %+v", settings)
	}
}
