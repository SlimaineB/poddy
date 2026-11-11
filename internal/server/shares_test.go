package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/k8s"
)

func TestWorkspaceShareInternalAndPublic(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, admin, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/workspaces/"+ws.Name+"/ports/4000/share", map[string]string{"access": "internal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Share struct {
			Access string `json:"access"`
			URL    string `json:"url"`
		} `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Share.Access != "internal" || created.Share.URL == "" {
		t.Fatalf("share = %+v", created.Share)
	}

	rec = request(t, s, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous internal share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/login?next="+url.QueryEscape(created.Share.URL) {
		t.Fatalf("login location = %q", got)
	}

	rec = requestAs(t, s, admin, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("authenticated share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != shareCookie {
		t.Fatalf("cookies = %+v", cookies)
	}
	shareCookieValue := cookies[0]

	target := "/api/workspaces/" + ws.Name + "/ports/4000/"
	if got := rec.Header().Get("Location"); got != target {
		t.Fatalf("share location = %q", got)
	}
	if shareCookieValue.Path != target {
		t.Fatalf("cookie path = %q", shareCookieValue.Path)
	}

	rec = requestAs(t, s, nil, http.MethodGet, target, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("internal target without session status = %d", rec.Code)
	}

	rec = requestWithCookies(t, s, []*http.Cookie{admin, shareCookieValue}, http.MethodGet, target, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("internal target status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/workspaces/"+ws.Name+"/ports/4000/share", map[string]string{"access": "public"})
	if rec.Code != http.StatusOK {
		t.Fatalf("public share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Share.Access != "public" {
		t.Fatalf("share access = %q", created.Share.Access)
	}

	rec = request(t, s, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("public share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies = rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != shareCookie {
		t.Fatalf("public cookies = %+v", cookies)
	}

	rec = requestAs(t, s, cookies[0], http.MethodGet, target, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("public target status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/workspaces/"+ws.Name+"/ports/4000/share", map[string]string{"access": "off"})
	if rec.Code != http.StatusOK {
		t.Fatalf("off status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var removed struct {
		Share any `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &removed); err != nil {
		t.Fatal(err)
	}
	if removed.Share != nil {
		t.Fatalf("share after off = %#v", removed.Share)
	}
	rec = request(t, s, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("removed share status = %d", rec.Code)
	}
}

func requestWithCookies(t *testing.T, s *Server, cookies []*http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestJupyterShareUsesLabPath(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, s, admin, http.MethodPost, "/api/workspaces", map[string]any{"name": "py", "template": "python"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/workspaces/"+ws.Name+"/ports/8888/share", map[string]string{"access": "public"})
	if rec.Code != http.StatusOK {
		t.Fatalf("share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Share struct {
			URL string `json:"url"`
		} `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = request(t, s, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v", cookies)
	}
	target := "/api/workspaces/" + ws.Name + "/lab/"
	if cookies[0].Path != target {
		t.Fatalf("cookie path = %q", cookies[0].Path)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Fatalf("location = %q", got)
	}
	rec = requestAs(t, s, cookies[0], http.MethodGet, target, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("lab status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceShareTarget(t *testing.T) {
	if got := workspaceShareTarget("demo", 4000, k8s.Workspace{}); got != "/api/workspaces/demo/ports/4000/" {
		t.Fatalf("generic target = %q", got)
	}
	if got := workspaceShareTarget("demo", 8888, k8s.Workspace{JupyterLab: true}); got != "/api/workspaces/demo/lab/" {
		t.Fatalf("jupyter target = %q", got)
	}
	if got := workspaceShareTarget("demo", 3000, k8s.Workspace{BrowserVSCode: true}); got != "/api/workspaces/demo/code/" {
		t.Fatalf("vscode target = %q", got)
	}
}

func TestIsWorkspaceSharePath(t *testing.T) {
	for _, path := range []string{
		"/api/workspaces/demo/ports/4000/",
		"/api/workspaces/demo/ports/4000/app",
		"/api/workspaces/demo/lab/",
		"/api/workspaces/demo/code/",
	} {
		if !isWorkspaceSharePath(path) {
			t.Fatalf("path %q not allowed", path)
		}
	}
	for _, path := range []string{
		"/api/workspaces",
		"/api/workspaces/demo",
		"/api/workspaces/demo/ports",
		"/api/workspaces/demo/ports/",
		"/api/workspaces/demo/lab",
		"/api/workspaces/demo/code",
		"/api/workspaces/demo/logs",
	} {
		if isWorkspaceSharePath(path) {
			t.Fatalf("path %q allowed", path)
		}
	}
}

func TestShareRequestAllowedRequiresCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/demo/ports/4000/", nil)
	if shareRequestAllowed(req) {
		t.Fatal("request without share cookie allowed")
	}
	req.AddCookie(&http.Cookie{Name: shareCookie, Value: "token"})
	if !shareRequestAllowed(req) {
		t.Fatal("request with share cookie not allowed")
	}
}
