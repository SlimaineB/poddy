package quota_test

import (
	"context"
	"testing"

	"poddy/internal/quota"
	"poddy/internal/rbac"
)

func intPtr(value int) *int { return &value }

func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := quota.NewMemory()

	if err := store.SetUserQuota(ctx, "alice@example.com", rbac.Quota{MaxWorkspaces: intPtr(2)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTeamQuota(ctx, "backend", rbac.Quota{CPU: "4"}); err != nil {
		t.Fatal(err)
	}
	users, err := store.UserQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := users["alice@example.com"]; got.MaxWorkspaces == nil || *got.MaxWorkspaces != 2 {
		t.Fatalf("user quotas = %+v", users)
	}
	teams, err := store.TeamQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if teams["backend"].CPU != "4" {
		t.Fatalf("team quotas = %+v", teams)
	}

	if err := store.DeleteUserQuota(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTeamQuota(ctx, "backend"); err != nil {
		t.Fatal(err)
	}
	if users, _ := store.UserQuotas(ctx); len(users) != 0 {
		t.Fatalf("user quotas after delete = %+v", users)
	}
	if teams, _ := store.TeamQuotas(ctx); len(teams) != 0 {
		t.Fatalf("team quotas after delete = %+v", teams)
	}
}

func TestEffectiveUsesStoreAndMembership(t *testing.T) {
	ctx := context.Background()
	store := quota.NewMemory()
	if err := store.SetTeamQuota(ctx, "backend", rbac.Quota{CPU: "4", Memory: "8Gi"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserQuota(ctx, "alice@example.com", rbac.Quota{CPU: "8"}); err != nil {
		t.Fatal(err)
	}
	cfg := rbac.Config{
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com", "bob@example.com"}},
		},
	}

	alice, err := quota.Effective(ctx, store, cfg, rbac.Identity{Username: "alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if alice.CPU != "8" {
		t.Fatalf("alice CPU override = %q", alice.CPU)
	}
	if alice.Memory != "8Gi" {
		t.Fatalf("alice memory inherited = %q", alice.Memory)
	}

	bob, err := quota.Effective(ctx, store, cfg, rbac.Identity{Username: "bob", Email: "bob@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if bob.CPU != "4" {
		t.Fatalf("bob CPU inherited = %q", bob.CPU)
	}

	carol, err := quota.Effective(ctx, store, cfg, rbac.Identity{Username: "carol", Email: "carol@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !carol.IsZero() {
		t.Fatalf("carol should be unlimited: %+v", carol)
	}
}
