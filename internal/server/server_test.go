package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/auth"
	"poddy/internal/k8s"
	"poddy/internal/quota"
	"poddy/internal/templates"
	"poddy/web"
)

func newTestServer(token string) *Server {
	catalog, err := templates.Load("../templates/files")
	if err != nil {
		panic(err)
	}
	client := fake.NewClientset()
	return &Server{
		Workspaces: &k8s.Manager{
			Client:          client,
			Namespace:       "poddy",
			Templates:       catalog,
			DefaultTemplate: "base",
		},
		WebFS:  web.FS,
		Token:  token,
		Access: k8s.NewMemoryAccessStore(),
		Quota:  quota.NewMemory(),
	}
}

func request(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCreateAndList(t *testing.T) {
	s := newTestServer("")

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}

	var list []k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "app" {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreatePythonWorkspaceIncludesJupyterLab(t *testing.T) {
	s := newTestServer("")

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "py", "template": "python"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if !ws.JupyterLab {
		t.Fatalf("python workspace did not include JupyterLab: %+v", ws)
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces/py", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("details status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var details map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details["jupyterLab"] != true {
		t.Fatalf("details jupyterLab = %#v", details["jupyterLab"])
	}
}

func TestCreatePrefixesWorkspaceName(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	alice := sessionFor(t, s, auth.User{Username: "Alice Smith", Email: "alice@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Name != "alice-smith-app" {
		t.Fatalf("workspace name = %q", ws.Name)
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-smith-other", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("prefixed create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Name != "alice-smith-other" {
		t.Fatalf("prefixed workspace name = %q", ws.Name)
	}

	rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "template": "base"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreatePrefixesTokenWorkspaceName(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"app"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Name != "token-app" {
		t.Fatalf("workspace name = %q", ws.Name)
	}
}

func TestCreateInvalid(t *testing.T) {
	s := newTestServer("")
	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "Bad_Name"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDelete(t *testing.T) {
	s := newTestServer("")
	if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]string{"name": "app"}); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	if rec := request(t, s, http.MethodDelete, "/api/workspaces/app", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec := request(t, s, http.MethodDelete, "/api/workspaces/app", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d", rec.Code)
	}
}

func TestAuth(t *testing.T) {
	s := newTestServer("secret")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthIsPublic(t *testing.T) {
	s := newTestServer("secret")
	rec := request(t, s, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUnauthorizedShowsLogin(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "access token") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestQueryTokenSetsCookieAndRedirects(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/?token=secret", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("location = %q", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestLoginCookieAuthorizesAPI(t *testing.T) {
	s := newTestServer("secret")
	form := url.Values{"token": {"secret"}, "next": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v", cookies)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"app"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidLogin(t *testing.T) {
	s := newTestServer("secret")
	form := url.Values{"token": {"wrong"}, "next": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid token") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestTemplatesAndCreateWithTemplate(t *testing.T) {
	s := newTestServer("")

	rec := request(t, s, http.MethodGet, "/api/templates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("templates status = %d", rec.Code)
	}
	var list []templates.Info
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatalf("templates = %+v", list)
	}

	rec = request(t, s, http.MethodPost, "/api/workspaces", map[string]any{
		"name":       "gows",
		"template":   "go",
		"parameters": map[string]string{"cpu": "2"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Template != "go" {
		t.Fatalf("workspace = %+v", ws)
	}
}

func TestMeWithToken(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/api/me?token=secret", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["method"] != "token" || body["username"] != "token" {
		t.Fatalf("body = %+v", body)
	}
}

func TestMeUnauthorized(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestOIDCLoginRedirectsWhenDisabled(t *testing.T) {
	s := newTestServer("")
	req := httptest.NewRequest(http.MethodGet, "/oidc/login", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("location = %q", loc)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "secret"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == cookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("token cookie was not cleared: %+v", cookies)
	}
}

func TestBrowserVSCodePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/workspaces/app/code", true},
		{"/api/workspaces/app/code/", true},
		{"/api/workspaces/app/code/static/main.js", false},
		{"/api/workspaces/app", false},
		{"/api/workspaces/code/other", false},
		{"/api/workspaces/app/vscode", false},
	}
	for _, test := range tests {
		if got := isBrowserVSCodePath(test.path); got != test.want {
			t.Errorf("isBrowserVSCodePath(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestBrowserJupyterPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/workspaces/app/lab", true},
		{"/api/workspaces/app/lab/", true},
		{"/api/workspaces/app/lab/static/main.js", false},
		{"/api/workspaces/app", false},
		{"/api/workspaces/lab/other", false},
		{"/api/workspaces/app/jupyter", false},
	}
	for _, test := range tests {
		if got := isBrowserJupyterPath(test.path); got != test.want {
			t.Errorf("isBrowserJupyterPath(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestBrowserJupyterTokenSetsCookieAndRedirects(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/app/lab/?token=secret", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/workspaces/app/lab/" {
		t.Fatalf("location = %q", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestBrowserVSCodeTokenSetsCookieAndRedirects(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/app/code/?token=secret", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/workspaces/app/code/" {
		t.Fatalf("location = %q", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestBrowserVSCodeRequiresRunningWorkspace(t *testing.T) {
	s := newTestServer("secret")
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"app"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/token-app/code/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceLogsResponseAndAudit(t *testing.T) {
	s := newTestServer("")
	store := &recordingMetadata{}
	s.Metadata = store

	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces/app/logs?tail=50", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var logs k8s.WorkspaceLogs
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if logs.Name != "app" || len(logs.Containers) < 1 {
		t.Fatalf("logs = %+v", logs)
	}
	found := map[string]bool{}
	for _, container := range logs.Containers {
		found[container.Name] = true
	}
	if !found["workspace"] || !found["code-server"] {
		t.Fatalf("logs containers = %+v", logs.Containers)
	}

	event := store.last()
	if event.Action != "workspace.logs.view" || event.Outcome != "success" || event.TargetName != "app" {
		t.Fatalf("logs audit = %+v", event)
	}
}
