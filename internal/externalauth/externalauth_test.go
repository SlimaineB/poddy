package externalauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/templates"
)

func TestParseProviders(t *testing.T) {
	providers, err := ParseProviders([]string{
		"PODDY_EXTERNAL_AUTH_0_ID=gitlab",
		"PODDY_EXTERNAL_AUTH_0_TYPE=gitlab",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_ID=client",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_SECRET=secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %+v", providers)
	}
	provider := providers[0]
	if provider.DisplayName != "Gitlab" || provider.Username != "oauth2" {
		t.Fatalf("provider = %+v", provider)
	}
	if !provider.MatchHost("https://gitlab.com/group/repo.git") {
		t.Fatal("gitlab.com should match")
	}
	if provider.MatchHost("https://github.com/group/repo.git") {
		t.Fatal("github.com should not match")
	}
	if len(provider.Env) != 1 || provider.Env[0] != "GITLAB_TOKEN" {
		t.Fatalf("env = %+v", provider.Env)
	}
}

func TestParseProvidersRejectsDuplicateIDs(t *testing.T) {
	_, err := ParseProviders([]string{
		"PODDY_EXTERNAL_AUTH_0_ID=gitlab",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_ID=a",
		"PODDY_EXTERNAL_AUTH_1_ID=gitlab",
		"PODDY_EXTERNAL_AUTH_1_CLIENT_ID=b",
	})
	if err == nil {
		t.Fatal("expected duplicate provider error")
	}
}

func TestStoreEncryptsTokens(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "tokens", "secret")
	ctx := context.Background()
	if err := store.Put(ctx, "alice@example.com", "gitlab", Token{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	secret, err := client.CoreV1().Secrets("poddy").Get(ctx, "tokens", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(secret.Data[secretKey]) < 3 || string(secret.Data[secretKey][:3]) != "v1:" {
		t.Fatalf("token store is not encrypted: %q", secret.Data[secretKey])
	}
	token, found, err := store.Get(ctx, "alice@example.com", "gitlab")
	if err != nil || !found {
		t.Fatalf("get: %v found=%v", err, found)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" {
		t.Fatalf("token = %+v", token)
	}
}

func TestManagerRefreshesToken(t *testing.T) {
	var refreshCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	providers, err := ParseProviders([]string{
		"PODDY_EXTERNAL_AUTH_0_ID=gitlab",
		"PODDY_EXTERNAL_AUTH_0_TYPE=oidc",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_ID=client",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_SECRET=secret",
		"PODDY_EXTERNAL_AUTH_0_AUTH_URL=" + server.URL + "/authorize",
		"PODDY_EXTERNAL_AUTH_0_TOKEN_URL=" + server.URL + "/token",
		"PODDY_EXTERNAL_AUTH_0_REGEX=gitlab\\.example\\.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "tokens", "secret")
	manager := NewManager(providers, store, "secret")
	ctx := context.Background()
	if err := store.Put(ctx, "alice@example.com", "gitlab", Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	token, err := manager.Token(ctx, "alice@example.com", "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d", refreshCalls)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" {
		t.Fatalf("token = %+v", token)
	}
}

func TestPrepareAndGitCredentials(t *testing.T) {
	providers, err := ParseProviders([]string{
		"PODDY_EXTERNAL_AUTH_0_ID=gitlab",
		"PODDY_EXTERNAL_AUTH_0_TYPE=oidc",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_ID=client",
		"PODDY_EXTERNAL_AUTH_0_AUTH_URL=https://gitlab.example.com/oauth/authorize",
		"PODDY_EXTERNAL_AUTH_0_TOKEN_URL=https://gitlab.example.com/oauth/token",
		"PODDY_EXTERNAL_AUTH_0_REGEX=gitlab\\.example\\.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "tokens", "secret")
	manager := NewManager(providers, store, "secret")
	ctx := context.Background()
	requirements := []templates.ExternalAuth{{ID: "gitlab", Env: []string{"GITLAB_TOKEN"}}}

	_, err = manager.Prepare(ctx, "alice@example.com", requirements)
	var required *AuthRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("expected AuthRequiredError, got %v", err)
	}

	if err := store.Put(ctx, "alice@example.com", "gitlab", Token{AccessToken: "access", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	env, err := manager.Prepare(ctx, "alice@example.com", requirements)
	if err != nil {
		t.Fatal(err)
	}
	if env["GITLAB_TOKEN"] != "access" || env["PODDY_EXTERNAL_AUTH_GITLAB_TOKEN"] != "access" {
		t.Fatalf("env = %+v", env)
	}
	username, password, err := manager.GitCredentials(ctx, "alice@example.com", requirements, "Username for 'https://gitlab.example.com':")
	if err != nil {
		t.Fatal(err)
	}
	if username != "oauth2" || password != "access" {
		t.Fatalf("credentials = %q %q", username, password)
	}
}

func TestWorkspaceToken(t *testing.T) {
	manager := NewManager(nil, nil, "secret")
	token, err := manager.SignWorkspace("demo", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := manager.VerifyWorkspace(token, "demo")
	if err != nil || owner != "alice@example.com" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if _, err := manager.VerifyWorkspace(token, "other"); err == nil {
		t.Fatal("expected workspace mismatch")
	}
}
