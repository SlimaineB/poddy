package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/auth"
	"poddy/internal/externalauth"
	"poddy/internal/templates"
)

func TestExternalAuthEndToEnd(t *testing.T) {
	s, provider := newExternalAuthTestServer(t)
	session := sessionFor(t, s, auth.User{Subject: "sub-1", Username: "alice", Email: "alice@example.com"})

	rec := requestAs(t, s, session, http.MethodPost, "/api/workspaces", map[string]string{"name": "alice-extws", "template": "ext"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("create without connection: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		Missing   []string `json:"missing"`
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if len(conflict.Missing) != 1 || conflict.Missing[0] != "mock" {
		t.Fatalf("missing = %+v", conflict.Missing)
	}
	if len(conflict.Providers) != 1 || conflict.Providers[0].ID != "mock" {
		t.Fatalf("providers = %+v", conflict.Providers)
	}

	rec = requestAs(t, s, session, http.MethodGet, "/api/external-auth?ids=mock", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Fatalf("expected disconnected provider: %s", rec.Body.String())
	}

	rec = requestAs(t, s, session, http.MethodGet, "/external-auth/mock/login?next=%2F", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(location.String(), provider.URL+"/auth?") {
		t.Fatalf("location = %q", location.String())
	}
	var flowCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == externalAuthCookie {
			flowCookie = cookie
		}
	}
	if flowCookie == nil || flowCookie.Value == "" {
		t.Fatalf("flow cookie not set: %+v", rec.Result().Cookies())
	}

	callback := httptest.NewRequest(http.MethodGet, "/external-auth/mock/callback?code=good-code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	callback.AddCookie(session)
	callback.AddCookie(flowCookie)
	callbackRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(callbackRec, callback)
	if callbackRec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, body = %s", callbackRec.Code, callbackRec.Body.String())
	}

	rec = requestAs(t, s, session, http.MethodGet, "/api/external-auth?ids=mock", nil)
	if !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("expected connected provider: %s", rec.Body.String())
	}

	rec = requestAs(t, s, session, http.MethodPost, "/api/workspaces", map[string]string{"name": "alice-extws", "template": "ext"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create after connection: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	pod, err := s.Workspaces.Get(context.Background(), "alice-extws")
	if err != nil {
		t.Fatal(err)
	}
	env := envMap(pod)
	if env["MOCK_TOKEN"] != "ext-access" || env["PODDY_EXTERNAL_AUTH_MOCK_TOKEN"] != "ext-access" {
		t.Fatalf("external auth env = %+v", env)
	}
	if env["PODDY_PORTAL_URL"] != "http://portal.example" || env["PODDY_WORKSPACE_NAME"] != "alice-extws" {
		t.Fatalf("workspace env = %+v", env)
	}
	if env["GIT_ASKPASS"] != "/usr/local/bin/poddy-git-askpass" || env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("git env = %+v", env)
	}
	workspaceToken := env["PODDY_WORKSPACE_TOKEN"]
	if workspaceToken == "" {
		t.Fatalf("workspace token is empty: %+v", env)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/alice-extws/external-auth/mock/token", nil)
	req.Header.Set(workspaceAuthHeader, workspaceToken)
	tokenRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(tokenRec, req)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d, body = %s", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenBody struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenBody); err != nil {
		t.Fatal(err)
	}
	if tokenBody.AccessToken != "ext-access" {
		t.Fatalf("token = %+v", tokenBody)
	}

	for prompt, want := range map[string]string{
		"Username for 'https://git.example.com':": "oauth2\n",
		"Password for 'https://git.example.com':": "ext-access\n",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/alice-extws/git-askpass?prompt="+url.QueryEscape(prompt), nil)
		req.Header.Set(workspaceAuthHeader, workspaceToken)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("askpass %q: status = %d, body = %s", prompt, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != want {
			t.Fatalf("askpass %q = %q, want %q", prompt, rec.Body.String(), want)
		}
	}
}

func newExternalAuthTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, _ := newOIDCTestServer(t)

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") != "good-code" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ext-access",
			"refresh_token": "ext-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(provider.Close)

	catalog, err := templates.LoadSources([]templates.Source{{
		Name: "ext",
		Template: []byte(`name: ext
displayName: External
image: poddy/workspace:latest
externalAuth:
  - id: mock
    env: [MOCK_TOKEN]
`),
		Pod: []byte(`apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      image: {{ .Image | quote }}
`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.Workspaces.Templates = catalog

	providers, err := externalauth.ParseProviders([]string{
		"PODDY_EXTERNAL_AUTH_0_ID=mock",
		"PODDY_EXTERNAL_AUTH_0_TYPE=oidc",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_ID=client",
		"PODDY_EXTERNAL_AUTH_0_CLIENT_SECRET=secret",
		"PODDY_EXTERNAL_AUTH_0_AUTH_URL=" + provider.URL + "/auth",
		"PODDY_EXTERNAL_AUTH_0_TOKEN_URL=" + provider.URL + "/token",
		"PODDY_EXTERNAL_AUTH_0_REGEX=git\\.example\\.com",
		"PODDY_EXTERNAL_AUTH_0_SCOPES=repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := externalauth.NewStore(s.Workspaces.Client, "poddy", "ext-tokens", "key")
	manager := externalauth.NewManager(providers, store, "key")
	s.ExternalAuth = manager
	s.ExternalAuthURL = "http://portal.example"
	s.ExternalAuthRedirectBase = "http://portal.example"
	s.Workspaces.ExternalAuth = func(ctx context.Context, owner, workspace, portalURL string, requirements []templates.ExternalAuth) (map[string]string, error) {
		return manager.Prepare(ctx, owner, requirements)
	}
	s.Workspaces.ExternalAuthToken = manager.SignWorkspace
	return s, provider
}

func envMap(pod *corev1.Pod) map[string]string {
	out := map[string]string{}
	for _, container := range pod.Spec.Containers {
		if container.Name != "workspace" {
			continue
		}
		for _, env := range container.Env {
			out[env.Name] = env.Value
		}
	}
	return out
}
