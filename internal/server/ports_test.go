package server

import (
	"net/http"
	"net/url"
	"testing"
)

func TestWorkspacePortURL(t *testing.T) {
	if got := workspacePortURL("alice-ws", 3000); got != "/api/workspaces/alice-ws/ports/3000/" {
		t.Fatalf("url = %q", got)
	}
}

func TestWorkspacePortRedirect(t *testing.T) {
	s := newTestServer("")
	rec := request(t, s, http.MethodGet, "/api/workspaces/demo/ports/3000?x=1", nil)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/workspaces/demo/ports/3000/?x=1" {
		t.Fatalf("location = %q", got)
	}
}

func TestWorkspacePortProxyRejectsInvalidPort(t *testing.T) {
	s := newTestServer("")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := request(t, s, method, "/api/workspaces/demo/ports/0/", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, body = %s", method, rec.Code, rec.Body.String())
		}
	}
}

func TestWorkspacePortRedirectPreservesMethod(t *testing.T) {
	s := newTestServer("")
	rec := request(t, s, http.MethodPost, "/api/workspaces/demo/ports/3000", nil)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/workspaces/demo/ports/3000/" {
		t.Fatalf("location = %q", got)
	}
}

func TestRewriteProxyLocation(t *testing.T) {
	resp := &http.Response{
		Header:  http.Header{"Location": []string{"/login?next=%2F"}},
		Request: &http.Request{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:4000"}},
	}
	rewriteProxyLocation(resp, "/api/workspaces/demo/ports/3000")
	if got := resp.Header.Get("Location"); got != "/api/workspaces/demo/ports/3000/login?next=%2F" {
		t.Fatalf("location = %q", got)
	}

	resp.Header.Set("Location", "http://127.0.0.1:4000/app?x=1")
	rewriteProxyLocation(resp, "/api/workspaces/demo/ports/3000")
	if got := resp.Header.Get("Location"); got != "/api/workspaces/demo/ports/3000/app?x=1" {
		t.Fatalf("absolute location = %q", got)
	}

	resp.Header.Set("Location", "//example.com/app")
	rewriteProxyLocation(resp, "/api/workspaces/demo/ports/3000")
	if got := resp.Header.Get("Location"); got != "//example.com/app" {
		t.Fatalf("protocol-relative location = %q", got)
	}
}

func TestStripPortalCookies(t *testing.T) {
	header := http.Header{
		"Cookie": []string{"theme=dark; poddy_token=secret; poddy_session=session; app=1; poddy_oidc=oidc"},
	}
	stripPortalCookies(header)
	if got := header.Get("Cookie"); got != "theme=dark; app=1" {
		t.Fatalf("cookie = %q", got)
	}
}

func TestRewriteProxyCookies(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Set-Cookie": []string{"session=abc; Path=/; HttpOnly"},
		},
	}
	rewriteProxyCookies(resp, "/api/workspaces/demo/ports/3000")
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v", cookies)
	}
	if cookies[0].Path != "/api/workspaces/demo/ports/3000/" {
		t.Fatalf("path = %q", cookies[0].Path)
	}
}
