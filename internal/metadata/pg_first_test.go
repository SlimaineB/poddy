package metadata

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/envstore"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/rbac"
	"poddy/internal/userstore"
)

func TestPostgresFirstStores(t *testing.T) {
	url := os.Getenv("PODDY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set PODDY_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx := context.Background()
	store, err := Open(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pg, ok := store.(*Postgres)
	if !ok {
		t.Fatalf("store = %T", store)
	}

	if !pg.PGFirstReady(ctx) {
		t.Fatal("PostgreSQL-first schema is not ready")
	}

	namespace := fmt.Sprintf("poddy-pgfirst-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "DELETE FROM poddy.tenants WHERE namespace = $1", namespace)
	})

	t.Run("rbac", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		mirror := rbac.NewStore(client, namespace, "")
		if err := mirror.Save(ctx, rbac.Config{
			Admins: []string{"admin@example.com"},
			Teams: map[string]rbac.Team{
				"devs": {Members: []string{"alice@example.com"}},
			},
		}); err != nil {
			t.Fatal(err)
		}

		store := pg.RBACStore(namespace, mirror)
		cfg, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Admins) != 1 || cfg.Admins[0] != "admin@example.com" {
			t.Fatalf("seeded admins = %v", cfg.Admins)
		}

		cfg, err = store.Update(ctx, func(cfg *rbac.Config) error {
			cfg.Teams["ops"] = rbac.Team{Members: []string{"bob@example.com"}}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := cfg.Teams["ops"]; !ok {
			t.Fatalf("updated teams = %v", cfg.Teams)
		}
		mirrored, err := mirror.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := mirrored.Teams["ops"]; !ok {
			t.Fatalf("mirrored teams = %v", mirrored.Teams)
		}
	})

	t.Run("users", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		mirror := userstore.NewStore(client, namespace, "")
		if _, err := mirror.Upsert(ctx, userstore.User{
			Subject: "subject-alice",
			Email:   "alice@example.com",
			Name:    "Alice",
		}); err != nil {
			t.Fatal(err)
		}

		store := pg.UserStore(namespace, mirror)
		users, err := store.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 || users[0].Email != "alice@example.com" {
			t.Fatalf("seeded users = %+v", users)
		}
		if _, err := pg.pool.Exec(ctx, `
			UPDATE poddy.users SET first_seen = NULL, last_seen = NULL
			WHERE namespace = $1 AND identity = 'alice@example.com'`, namespace); err != nil {
			t.Fatal(err)
		}
		if _, err := store.List(ctx); err != nil {
			t.Fatalf("list users with null timestamps: %v", err)
		}

		if _, err := store.Upsert(ctx, userstore.User{
			Subject: "subject-bob",
			Email:   "bob@example.com",
			Name:    "Bob",
		}); err != nil {
			t.Fatal(err)
		}
		users, err = store.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 2 {
			t.Fatalf("users = %+v", users)
		}
		mirrored, err := mirror.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(mirrored) != 2 {
			t.Fatalf("mirrored users = %+v", mirrored)
		}
	})

	t.Run("external auth", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		source := externalauth.NewStore(client, namespace, "", "secret")
		if err := source.Put(ctx, "alice@example.com", "gitlab", externalauth.Token{
			AccessToken:  "access",
			RefreshToken: "refresh",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(time.Hour).UTC(),
			Scopes:       []string{"api"},
		}); err != nil {
			t.Fatal(err)
		}
		tokens, err := source.All(ctx)
		if err != nil {
			t.Fatal(err)
		}

		pgStore := pg.ExternalAuthStore(namespace, "secret")
		if err := pgStore.SeedIfEmpty(ctx, tokens); err != nil {
			t.Fatal(err)
		}
		token, found, err := pgStore.Get(ctx, "alice@example.com", "gitlab")
		if err != nil || !found {
			t.Fatalf("get token: %v found=%v", err, found)
		}
		if token.AccessToken != "access" || token.RefreshToken != "refresh" {
			t.Fatalf("token = %+v", token)
		}

		var ciphertext []byte
		if err := pg.pool.QueryRow(ctx, `
			SELECT access_token_ciphertext FROM poddy.external_auth_tokens
			WHERE namespace = $1 AND owner = 'alice@example.com' AND provider = 'gitlab'`,
			namespace,
		).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		if string(ciphertext) == "access" {
			t.Fatal("access token is stored in plaintext")
		}

		if _, err := pg.pool.Exec(ctx,
			"DELETE FROM poddy.external_auth_tokens WHERE namespace = $1", namespace); err != nil {
			t.Fatal(err)
		}
		if err := pgStore.SeedIfEmpty(ctx, tokens); err != nil {
			t.Fatal(err)
		}
		if _, found, err := pgStore.Get(ctx, "alice@example.com", "gitlab"); err != nil || found {
			t.Fatalf("deleted token was re-imported: found=%v err=%v", found, err)
		}
	})

	t.Run("shares", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		mirror := k8s.NewShareStore(client, namespace, "")
		if err := mirror.Set(ctx, k8s.Share{
			Token:     "raw-share-token",
			Workspace: "demo",
			Port:      3000,
			Path:      "/demo/3000",
			Access:    k8s.ShareAccessInternal,
			CreatedBy: "alice@example.com",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}

		store := pg.ShareStore(namespace, "secret", mirror)
		shares, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(shares) != 1 {
			t.Fatalf("shares = %+v", shares)
		}
		share, found, err := store.GetByToken(ctx, "raw-share-token")
		if err != nil || !found {
			t.Fatalf("get share: %v found=%v", err, found)
		}
		if share.Workspace != "demo" || share.Port != 3000 {
			t.Fatalf("share = %+v", share)
		}

		share.Access = k8s.ShareAccessPublic
		share.UpdatedAt = time.Now().UTC()
		if err := store.Set(ctx, share); err != nil {
			t.Fatal(err)
		}
		got, found, err := store.GetByTarget(ctx, "demo", "/demo/3000")
		if err != nil || !found || got.Access != k8s.ShareAccessPublic {
			t.Fatalf("get target: %+v found=%v err=%v", got, found, err)
		}

		var hash string
		var ciphertext []byte
		if err := pg.pool.QueryRow(ctx, `
			SELECT token_hash, token_ciphertext FROM poddy.share_links
			WHERE namespace = $1 AND workspace = 'demo'`,
			namespace,
		).Scan(&hash, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if hash == "raw-share-token" || string(ciphertext) == "raw-share-token" {
			t.Fatal("share token is not hashed and encrypted")
		}

		if err := store.DeleteWorkspace(ctx, "demo"); err != nil {
			t.Fatal(err)
		}
		if _, found, err := store.GetByToken(ctx, "raw-share-token"); err != nil || found {
			t.Fatalf("deleted share found=%v err=%v", found, err)
		}
		fresh := pg.ShareStore(namespace, "secret", mirror)
		shares, err = fresh.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(shares) != 0 {
			t.Fatalf("deleted shares were re-imported: %+v", shares)
		}
	})

	t.Run("environment", func(t *testing.T) {
		store := pg.EnvStore(namespace, "secret")
		for _, item := range []struct {
			scope  envstore.Scope
			target string
			name   string
			value  string
			secret bool
		}{
			{envstore.ScopeTemplate, "base", "A", "template", false},
			{envstore.ScopeTeam, "devs", "A", "team", false},
			{envstore.ScopeUser, "alice@example.com", "A", "user", false},
			{envstore.ScopeWorkspace, "demo", "A", "workspace", false},
			{envstore.ScopeUser, "alice@example.com", "TOKEN", "encrypted", true},
		} {
			if err := store.Set(ctx, item.scope, item.target, item.name, item.value, item.secret); err != nil {
				t.Fatal(err)
			}
		}
		resolved, err := store.Resolve(ctx, "Alice@Example.com", []string{"devs"}, "base", "demo")
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Values["A"] != "workspace" || resolved.Values["TOKEN"] != "encrypted" || !resolved.Secrets["TOKEN"] {
			t.Fatalf("resolved = %+v", resolved)
		}
		items, err := store.List(ctx, envstore.ScopeUser, "alice@example.com")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.Name == "TOKEN" && (item.Value != "" || !item.Set || !item.Secret) {
				t.Fatalf("secret list entry = %+v", item)
			}
		}
		if err := store.DeleteTarget(ctx, envstore.ScopeWorkspace, "demo"); err != nil {
			t.Fatal(err)
		}
		resolved, err = store.Resolve(ctx, "alice@example.com", []string{"devs"}, "base", "demo")
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Values["A"] != "user" {
			t.Fatalf("workspace value was not deleted: %+v", resolved)
		}
	})
}
