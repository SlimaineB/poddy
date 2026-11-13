package quota_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"poddy/internal/metadata"
	"poddy/internal/rbac"
)

func TestPostgresQuotaStore(t *testing.T) {
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

	namespace := fmt.Sprintf("poddy-quota-test-%d", time.Now().UnixNano())
	store := pg.Quota(namespace)

	if err := store.SetUserQuota(ctx, "alice@example.com", rbac.Quota{MaxWorkspaces: intPtr(2), CPU: "4"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTeamQuota(ctx, "backend", rbac.Quota{Memory: "8Gi"}); err != nil {
		t.Fatal(err)
	}

	users, err := store.UserQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := users["alice@example.com"]; got.MaxWorkspaces == nil || *got.MaxWorkspaces != 2 || got.CPU != "4" {
		t.Fatalf("user quotas = %+v", users)
	}
	teams, err := store.TeamQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if teams["backend"].Memory != "8Gi" {
		t.Fatalf("team quotas = %+v", teams)
	}

	if err := store.SetUserQuota(ctx, "alice@example.com", rbac.Quota{Storage: "10Gi"}); err != nil {
		t.Fatal(err)
	}
	users, err = store.UserQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := users["alice@example.com"]; got.MaxWorkspaces != nil || got.CPU != "" || got.Storage != "10Gi" {
		t.Fatalf("updated user quotas = %+v", users)
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
