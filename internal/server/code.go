package server

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	corev1 "k8s.io/api/core/v1"
)

const browserVSCodePort = 3000

func (s *Server) browserVSCode(w http.ResponseWriter, r *http.Request) {
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
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	allowed, err := s.workspaceAllowed(r, name, id, cfg)
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
	if !ws.BrowserVSCode {
		http.Error(w, "browser VS Code is not enabled for this workspace", http.StatusConflict)
		return
	}
	if s.Forwards == nil {
		http.Error(w, "browser VS Code is unavailable", http.StatusNotImplemented)
		return
	}

	port, err := s.Forwards.EnsurePort(s.Workspaces, name, browserVSCodePort)
	if err != nil {
		log.Printf("browser vscode %s: port-forward: %v", name, err)
		http.Error(w, "browser VS Code is unavailable", http.StatusBadGateway)
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
		log.Printf("browser vscode %s: proxy: %v", name, err)
		http.Error(w, "browser VS Code is unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
