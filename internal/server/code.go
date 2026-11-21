package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
)

const (
	browserVSCodePort  = 3000
	browserJupyterPort = 8888
	ideReloadScript    = `<script src="/static/ide-reload.js" defer></script>`
)

type browserIDEConfig struct {
	label    string
	port     int
	path     string
	enabled  func(k8s.Workspace) bool
	disabled string
}

func (s *Server) browserVSCode(w http.ResponseWriter, r *http.Request) {
	s.browserIDE(w, r, browserIDEConfig{
		label:    "browser VS Code",
		port:     browserVSCodePort,
		path:     "/code/",
		enabled:  func(ws k8s.Workspace) bool { return ws.BrowserVSCode },
		disabled: "browser VS Code is not enabled for this workspace",
	})
}

func (s *Server) browserJupyter(w http.ResponseWriter, r *http.Request) {
	s.browserIDE(w, r, browserIDEConfig{
		label:    "JupyterLab",
		port:     browserJupyterPort,
		path:     "/lab/",
		enabled:  func(ws k8s.Workspace) bool { return ws.JupyterLab },
		disabled: "JupyterLab is not enabled for this workspace",
	})
}

func (s *Server) browserJupyterRedirect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Path + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect) // #nosec G710 -- Target is a local request path.
}

func (s *Server) browserIDE(w http.ResponseWriter, r *http.Request, cfg browserIDEConfig) {
	name := r.PathValue("name")
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	targetPath := workspaceIDEPath(name, cfg.path)
	id, ok := s.currentIdentity(r)
	if ok {
		rbac, err := s.loadRBAC(r)
		if err != nil {
			http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
			return
		}
		allowed, err := s.interactiveAccess(r, name, id, rbac)
		if err != nil || !allowed.Allowed {
			if _, shared := s.shareAccess(r, name, targetPath); !shared {
				s.denyInteractive(w, id, rbac)
				return
			}
		} else {
			s.markPresence(name, ownerID(id.User), "browser")
		}
	} else if _, shared := s.shareAccess(r, name, targetPath); !shared {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := s.Workspaces.GetWorkspace(r.Context(), name)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if ws.Phase != string(corev1.PodRunning) {
		http.Error(w, "workspace is not running", http.StatusConflict)
		return
	}
	if !cfg.enabled(ws) {
		http.Error(w, cfg.disabled, http.StatusConflict)
		return
	}
	if s.Forwards == nil {
		http.Error(w, cfg.label+" is unavailable", http.StatusNotImplemented)
		return
	}

	port, err := s.Forwards.EnsurePort(s.Workspaces, name, cfg.port)
	if err != nil {
		log.Printf("%s %s: port-forward: %v", strings.ReplaceAll(strings.ReplaceAll(cfg.label, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
		http.Error(w, cfg.label+" is unavailable", http.StatusBadGateway)
		return
	}

	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	if cfg.port == browserVSCodePort {
		proxy.ModifyResponse = injectIDEReload
	}
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		stripPortalCookies(req.Header)
		stripPortalAuth(req.Header, s.Token)
		if cfg.port == browserVSCodePort && r.URL.Path == workspaceIDEPath(name, cfg.path) {
			req.Header.Set("Accept-Encoding", "identity")
		}
		req.Host = r.Host
		req.Header.Set("X-Forwarded-Host", r.Host)
		if secureRequest(r) {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("%s %s: proxy: %v", strings.ReplaceAll(strings.ReplaceAll(cfg.label, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
		if wantsHTML(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, ideStartingPage)
			return
		}
		http.Error(w, cfg.label+" is unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func workspaceLabURL(name string) string {
	return workspaceIDEPath(name, "/lab/")
}

func workspaceIDEPath(name, suffix string) string {
	return "/api/workspaces/" + url.PathEscape(name) + suffix
}

const ideStartingPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="1">
<title>Starting IDE</title>
<style>body{font-family:system-ui,sans-serif;background:#1e1e1e;color:#ddd;display:grid;place-items:center;height:100vh;margin:0}main{text-align:center}span{display:block;margin-top:12px;color:#999}</style>
</head>
<body><main><strong>Starting the IDE</strong><span>This page reloads automatically when it is ready.</span></main></body>
</html>`

func injectIDEReload(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if !bytes.Contains(body, []byte("</head>")) {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	body = bytes.Replace(body, []byte("</head>"), []byte(ideReloadScript+"</head>"), 1)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}
