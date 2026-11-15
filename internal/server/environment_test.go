package server

import (
	"context"
	"net/http"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/envstore"
	"poddy/internal/rbac"
)

func TestEnvironmentItemsHidesSecretValues(t *testing.T) {
	items := environmentItems(envstore.Resolved{
		Values: map[string]string{
			"PLAIN":  "visible",
			"SECRET": "hidden",
		},
		Secrets: map[string]bool{"SECRET": true},
		Sources: map[string]envstore.Source{
			"PLAIN":  {Scope: envstore.ScopeTeam, Target: "backend"},
			"SECRET": {Scope: envstore.ScopeWorkspace, Target: "app"},
		},
	})
	if len(items) != 2 || items[0].Name != "PLAIN" || items[1].Name != "SECRET" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Value != "visible" || items[0].Source.Scope != envstore.ScopeTeam || items[0].Source.Target != "backend" {
		t.Fatalf("plain item = %+v", items[0])
	}
	if items[1].Value != "" || !items[1].Secret || items[1].Source.Scope != envstore.ScopeWorkspace {
		t.Fatalf("secret item = %+v", items[1])
	}
}

type stubEnvironmentStore struct{}

func (stubEnvironmentStore) List(context.Context, envstore.Scope, string) ([]envstore.Entry, error) {
	return nil, nil
}

func (stubEnvironmentStore) Set(context.Context, envstore.Scope, string, string, string, bool) error {
	return nil
}

func (stubEnvironmentStore) Delete(context.Context, envstore.Scope, string, string) error {
	return nil
}

func (stubEnvironmentStore) DeleteTarget(context.Context, envstore.Scope, string) error {
	return nil
}

func (stubEnvironmentStore) Resolve(context.Context, string, []string, string, string) (envstore.Resolved, error) {
	return envstore.Resolved{}, nil
}

func TestNonAdminEnvironmentAccess(t *testing.T) {
	s := newRBACServer(t, rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com"}},
		},
	})
	s.Environment = stubEnvironmentStore{}
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"own user scope", "/api/environment?scope=user&target=alice@example.com", http.StatusOK},
		{"other user scope", "/api/environment?scope=user&target=bob@example.com", http.StatusForbidden},
		{"team scope", "/api/environment?scope=team&target=backend", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := requestAs(t, s, alice, http.MethodGet, tc.path, nil); rec.Code != tc.want {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
