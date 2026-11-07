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

func TestUsersPageUsersAndGroupsIndependently(t *testing.T) {
	s := newRBACServer(t, rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"one":   {Members: []string{"alice@example.com", "group:alpha"}},
			"two":   {Members: []string{"bob@example.com", "group:beta"}},
			"three": {Members: []string{"carol@example.com", "group:gamma"}},
		},
	})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, admin, http.MethodGet, "/api/users?page=2&per_page=1&group_page=2&group_per_page=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Users       []string `json:"users"`
		Groups      []string `json:"groups"`
		TotalUsers  int      `json:"totalUsers"`
		TotalGroups int      `json:"totalGroups"`
		Page        int      `json:"page"`
		Pages       int      `json:"pages"`
		GroupPage   int      `json:"groupPage"`
		GroupPages  int      `json:"groupPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalUsers != 4 || len(got.Users) != 1 || got.Page != 2 || got.Pages != 4 {
		t.Fatalf("users page = %+v", got)
	}
	if got.TotalGroups != 3 || len(got.Groups) != 1 || got.GroupPage != 2 || got.GroupPages != 3 {
		t.Fatalf("groups page = %+v", got)
	}
}
