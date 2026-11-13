package region

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/k8s"
)

func TestHubListKeepsCachedWorkspacesOnRegionFailure(t *testing.T) {
	var fail atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode([]k8s.Workspace{{Name: "app", Phase: "Running"}})
	}))
	defer backend.Close()

	registry, err := Parse([]byte("regions:\n- name: eu\n  key: secret\n  url: " + backend.URL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(registry)
	identity := Identity{User: auth.User{Username: "alice"}, Admin: true}

	list, problems := hub.List(context.Background(), identity)
	if len(problems) != 0 || len(list) != 1 || list[0].Name != "app" {
		t.Fatalf("first list = %+v problems = %v", list, problems)
	}

	fail.Store(true)
	list, problems = hub.List(context.Background(), identity)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if len(list) != 1 || list[0].Name != "app" {
		t.Fatalf("cached list = %+v", list)
	}
}

func TestHubListCacheIsScopedToIdentity(t *testing.T) {
	var fail atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode([]k8s.Workspace{{Name: "app", Phase: "Running"}})
	}))
	defer backend.Close()

	registry, err := Parse([]byte("regions:\n- name: eu\n  key: secret\n  url: " + backend.URL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(registry)

	if _, problems := hub.List(context.Background(), Identity{User: auth.User{Username: "alice"}, Admin: true}); len(problems) != 0 {
		t.Fatalf("alice problems = %v", problems)
	}
	fail.Store(true)
	list, problems := hub.List(context.Background(), Identity{User: auth.User{Username: "bob"}, Admin: true})
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if len(list) != 0 {
		t.Fatalf("bob saw another identity's cached list: %+v", list)
	}
}

func TestHubFindWorkspaceUsesCachedRegion(t *testing.T) {
	var euHits, usHits atomic.Int32
	eu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		euHits.Add(1)
		if r.URL.Path == "/api/workspaces" {
			_ = json.NewEncoder(w).Encode([]k8s.Workspace{})
			return
		}
		http.NotFound(w, r)
	}))
	defer eu.Close()
	us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usHits.Add(1)
		if r.URL.Path == "/api/workspaces" {
			_ = json.NewEncoder(w).Encode([]k8s.Workspace{{Name: "app", Phase: "Running"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "app", "phase": "Running"})
	}))
	defer us.Close()

	registry, err := Parse([]byte(
		"regions:\n- name: eu\n  key: secret\n  url: " + eu.URL + "\n- name: us\n  key: secret\n  url: " + us.URL + "\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(registry)
	identity := Identity{User: auth.User{Username: "alice"}, Admin: true}

	if _, problems := hub.List(context.Background(), identity); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	euBefore, usBefore := euHits.Load(), usHits.Load()

	cfg, info, err := hub.FindWorkspace(context.Background(), "app", identity)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "us" || info.Name != "app" {
		t.Fatalf("cfg = %+v info = %+v", cfg, info)
	}
	if got := euHits.Load(); got != euBefore {
		t.Fatalf("eu hits = %d, want %d", got, euBefore)
	}
	if got := usHits.Load(); got != usBefore+1 {
		t.Fatalf("us hits = %d, want %d", got, usBefore+1)
	}
}
