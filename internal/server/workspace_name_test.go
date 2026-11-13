package server

import (
	"testing"

	"poddy/internal/auth"
)

func TestWorkspaceNamePrefixStripsEmailDomain(t *testing.T) {
	cases := []struct {
		name string
		id   *identity
		want string
	}{
		{"username email", &identity{User: auth.User{Username: "alice@example.com", Email: "alice@example.com"}}, "alice"},
		{"email fallback", &identity{User: auth.User{Email: "bob@corp.example.com"}}, "bob"},
		{"plain username", &identity{User: auth.User{Username: "Carol"}}, "carol"},
		{"subject fallback", &identity{User: auth.User{Subject: "user-123"}}, "user-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workspaceNamePrefix(tc.id); got != tc.want {
				t.Fatalf("workspaceNamePrefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkspaceNameForPrefixesCleanUsername(t *testing.T) {
	id := &identity{Method: "oidc", User: auth.User{Username: "alice@example.com", Email: "alice@example.com"}}
	if got := workspaceNameFor(id, "demo"); got != "alice-demo" {
		t.Fatalf("workspaceNameFor() = %q, want %q", got, "alice-demo")
	}
	if got := workspaceNameFor(id, "alice-demo"); got != "alice-demo" {
		t.Fatalf("workspaceNameFor() = %q, want %q", got, "alice-demo")
	}
}
