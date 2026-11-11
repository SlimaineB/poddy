package server

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
)

const (
	browserVSCodePort  = 3000
	browserJupyterPort = 8888
)

type browserIDEConfig struct {
	label    string
	port     int
	enabled  func(k8s.Workspace) bool
	disabled string
}

func (s *Server) browserVSCode(w http.ResponseWriter, r *http.Request) {
	s.browserIDE(w, r, browserIDEConfig{
		label:    "browser VS Code",
		port:     browserVSCodePort,
		enabled:  func(ws k8s.Workspace) bool { return ws.BrowserVSCode },
		disabled: "browser VS Code is not enabled for this workspace",
	})
}

func (s *Server) browserJupyter(w http.ResponseWriter, r *http.Request) {
	s.browserIDE(w, r, browserIDEConfig{
		label:    "JupyterLab",
		port:     browserJupyterPort,
		enabled:  func(ws k8s.Workspace) bool { return ws.JupyterLab },
		disabled: "JupyterLab is not enabled for this workspace",
	})
}

func (s *Server) browserJupyterRedirect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Path + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) browserIDE(w http.ResponseWriter, r *http.Request, cfg browserIDEConfig) {
	name := r.PathValue("name")
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rbac, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	allowed, err := s.workspaceAllowed(r, name, id, rbac)
	if err != nil || !allowed {
		http.Error(w, "workspace not found", http.StatusNotFound)
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
		log.Printf("%s %s: port-forward: %v", cfg.label, name, err)
		http.Error(w, cfg.label+" is unavailable", http.StatusBadGateway)
		return
	}

	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = r.Host
		req.Header.Set("X-Forwarded-Host", r.Host)
		if secureRequest(r) {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("%s %s: proxy: %v", cfg.label, name, err)
		http.Error(w, cfg.label+" is unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
