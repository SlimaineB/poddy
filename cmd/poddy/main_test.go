package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExternalAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Poddy-Workspace-Token") != "ws-token" {
			http.Error(w, "missing workspace token", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/workspaces/ws/external-auth/gitea/token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "secret",
			"tokenType":   "Bearer",
		})
	}))
	defer server.Close()
	setWorkspaceEnv(t, server.URL)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"external-auth", "token", "gitea"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "secret\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestExternalAuthStatusDisconnected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "external authentication required", http.StatusUnauthorized)
	}))
	defer server.Close()
	setWorkspaceEnv(t, server.URL)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"external-auth", "status", "gitea"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "gitea: not connected\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestExternalAuthLoginWaitsForToken(t *testing.T) {
	var tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/workspaces/ws/external-auth/gitea/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"provider":  "gitea",
				"loginUrl":  "http://portal.example/external-auth/gitea/cli?flow=signed",
				"expiresAt": time.Now().Add(time.Minute),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/workspaces/ws/external-auth/gitea/token":
			if tokenCalls.Add(1) == 1 {
				http.Error(w, "external authentication required", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "secret"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setWorkspaceEnv(t, server.URL)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"external-auth", "login",
		"--no-open", "--print-token", "--timeout", "2s", "--interval", "10ms",
		"gitea",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "secret\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "http://portal.example/external-auth/gitea/cli?flow=signed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestGitAskpass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/ws/git-askpass" || r.URL.Query().Get("prompt") == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("oauth2\n"))
	}))
	defer server.Close()
	setWorkspaceEnv(t, server.URL)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"git-askpass", "Username for 'https://git.example.com':"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "oauth2\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMissingWorkspaceEnv(t *testing.T) {
	t.Setenv("PODDY_PORTAL_URL", "")
	t.Setenv("PODDY_WORKSPACE_NAME", "")
	t.Setenv("PODDY_WORKSPACE_TOKEN", "")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"external-auth", "token", "gitea"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr.String(), "PODDY_PORTAL_URL") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func setWorkspaceEnv(t *testing.T, portalURL string) {
	t.Helper()
	t.Setenv("PODDY_PORTAL_URL", portalURL)
	t.Setenv("PODDY_WORKSPACE_NAME", "ws")
	t.Setenv("PODDY_WORKSPACE_TOKEN", "ws-token")
}
