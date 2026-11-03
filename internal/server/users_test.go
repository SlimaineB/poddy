package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/rbac"
)

func TestUsersDirectory(t *testing.T) {
	s := newRBACServer(t, rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com", "group:platform"}},
		},
		Templates: map[string]rbac.Access{
			"go": {Owners: []string{"dev@example.com"}, Users: []string{"bob@example.com", "user:carol"}},
		},
	})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com", Groups: []string{"platform"}})

	rec := requestAs(t, s, admin, http.MethodGet, "/api/users", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Users  []string `json:"users"`
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"admin@example.com", "alice@example.com", "bob@example.com", "carol", "dev@example.com"} {
		if !contains(got.Users, want) {
			t.Errorf("missing user %q in %v", want, got.Users)
		}
	}
	if !contains(got.Groups, "platform") {
		t.Errorf("missing group platform in %v", got.Groups)
	}

	if rec := requestAs(t, s, dev, http.MethodGet, "/api/users", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner status = %d", rec.Code)
	}
	if rec := requestAs(t, s, alice, http.MethodGet, "/api/users", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("regular user status = %d", rec.Code)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
