package access_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"poddy/internal/access"
	"poddy/internal/metadata"
)

func TestPostgresAccessStore(t *testing.T) {
	url := os.Getenv("PODDY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set PODDY_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx := context.Background()
	meta, err := metadata.Open(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	pg, ok := meta.(*metadata.Postgres)
	if !ok {
		t.Fatalf("metadata store = %T", meta)
	}

	namespace := fmt.Sprintf("poddy-access-test-%d", time.Now().UnixNano())
	store := pg.Access(namespace)
	t.Cleanup(func() {
		_ = store.DeleteWorkspace(context.Background(), "demo")
	})

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
	if _, ok := state.Grant("demo", "admin@example.com", time.Now()); !ok {
		t.Fatal("approval did not create a grant")
	}

	now := time.Now()
	if err := store.SetPresence(ctx, "demo", "s1", access.Presence{Actor: "alice@example.com", Kind: "terminal", Since: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPresence(ctx, "demo", "s1", access.Presence{Actor: "alice@example.com", Kind: "terminal", Since: now.Add(time.Hour), LastSeen: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	summary := state.PresenceSummary("demo", now.Add(2*time.Second))
	if len(summary) != 1 {
		t.Fatalf("presence summary = %+v", summary)
	}
	if delta := summary[0].Since.Sub(now); delta > time.Millisecond || delta < -time.Millisecond {
		t.Fatalf("presence since = %s, want %s", summary[0].Since, now)
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

	if err := store.Share(ctx, "demo", "Bob@Example.com", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	share, ok := state.Share("demo", "bob@example.com")
	if !ok {
		t.Fatal("share was not stored")
	}
	if share.SharedBy != "alice@example.com" {
		t.Fatalf("sharedBy = %q", share.SharedBy)
	}
	if err := store.Unshare(ctx, "demo", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.IsShared("demo", "bob@example.com") {
		t.Fatal("unshare left the share in place")
	}

	if err := store.Share(ctx, "demo", "bob@example.com", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspace(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Shares["demo"]) != 0 {
		t.Fatalf("delete workspace left shares: %+v", state.Shares)
	}
}
