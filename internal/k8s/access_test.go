package k8s

import (
	"context"
	"testing"
	"time"
)

func TestAccessStoreRequestsAndGrants(t *testing.T) {
	store := NewMemoryAccessStore()
	ctx := context.Background()

	granted, err := store.Request(ctx, "demo", "Admin@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("request reported an existing grant")
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasRequest("demo", "admin@example.com") {
		t.Fatal("request was not stored case-insensitively")
	}
	if _, ok := state.Grant("demo", "admin@example.com", time.Now()); ok {
		t.Fatal("request created a grant")
	}

	if err := store.Approve(ctx, "demo", "admin@example.com", "alice@example.com", time.Minute); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.HasRequest("demo", "admin@example.com") {
		t.Fatal("approval did not clear the request")
	}
	grant, ok := state.Grant("demo", "admin@example.com", time.Now())
	if !ok {
		t.Fatal("approval did not create a grant")
	}
	if grant.ApprovedBy != "alice@example.com" {
		t.Fatalf("approvedBy = %q", grant.ApprovedBy)
	}

	if _, ok := state.Grant("demo", "admin@example.com", time.Now().Add(2*time.Minute)); ok {
		t.Fatal("expired grant still valid")
	}

	if err := store.Revoke(ctx, "demo", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Grant("demo", "admin@example.com", time.Now()); ok {
		t.Fatal("revoke left the grant in place")
	}
}

func TestAccessStorePresence(t *testing.T) {
	store := NewMemoryAccessStore()
	ctx := context.Background()
	now := time.Now()

	if err := store.SetPresence(ctx, "demo", "s1", Presence{Actor: "alice@example.com", Kind: "terminal", Since: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPresence(ctx, "demo", "s2", Presence{Actor: "alice@example.com", Kind: "ssh", Since: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPresence(ctx, "demo", "s3", Presence{Actor: "admin@example.com", Kind: "browser", Since: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	summary := state.PresenceSummary("demo", now)
	if len(summary) != 2 {
		t.Fatalf("presence summary = %+v", summary)
	}
	if summary[0].Actor != "admin@example.com" || summary[1].Actor != "alice@example.com" {
		t.Fatalf("presence order = %+v", summary)
	}
	if len(summary[1].Kinds) != 2 {
		t.Fatalf("alice kinds = %+v", summary[1].Kinds)
	}

	stale := now.Add(DefaultPresenceTTL + time.Second)
	if got := state.PresenceSummary("demo", stale); len(got) != 0 {
		t.Fatalf("stale presence = %+v", got)
	}
	state.Prune(stale)
	if len(state.Presence) != 0 {
		t.Fatalf("prune left presence: %+v", state.Presence)
	}

	if err := store.ClearPresence(ctx, "demo", "s1"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Presence["demo"]["s1"]; ok {
		t.Fatal("clear presence left the session")
	}
}

func TestAccessStoreDeleteWorkspace(t *testing.T) {
	store := NewMemoryAccessStore()
	ctx := context.Background()
	if _, err := store.Request(ctx, "demo", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPresence(ctx, "demo", "s1", Presence{Actor: "admin@example.com", Kind: "terminal", Since: time.Now(), LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspace(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Requests) != 0 || len(state.Grants) != 0 || len(state.Presence) != 0 {
		t.Fatalf("state after delete = %+v", state)
	}
}
