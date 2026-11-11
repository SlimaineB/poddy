package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
	"poddy/internal/local"
)

type Forwarder struct {
	mu      sync.Mutex
	tunnels map[string]*tunnel
}

type tunnel struct {
	port int
	stop func()
}

func NewForwarder() *Forwarder {
	return &Forwarder{tunnels: map[string]*tunnel{}}
}

func (f *Forwarder) Ensure(manager *k8s.Manager, name string) (int, error) {
	return f.EnsurePort(manager, name, 22)
}

func (f *Forwarder) EnsurePort(manager *k8s.Manager, name string, remotePort int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := fmt.Sprintf("%s:%d", name, remotePort)
	if t, ok := f.tunnels[key]; ok {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", t.port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return t.port, nil
		}
		t.stop()
		delete(f.tunnels, key)
	}

	port, stop, err := manager.PortForward(k8s.PodName(name), remotePort)
	if err != nil {
		return 0, err
	}
	f.tunnels[key] = &tunnel{port: port, stop: stop}
	return port, nil
}

func (f *Forwarder) Remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := name + ":"
	for key, t := range f.tunnels {
		if key == name || strings.HasPrefix(key, prefix) {
			t.stop()
			delete(f.tunnels, key)
		}
	}
}

func (f *Forwarder) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, t := range f.tunnels {
		t.stop()
		delete(f.tunnels, name)
	}
}

func (s *Server) vscode(w http.ResponseWriter, r *http.Request) {
	s.launchLocalEditor(w, r, "vscode")
}

func (s *Server) cursor(w http.ResponseWriter, r *http.Request) {
	s.launchLocalEditor(w, r, "cursor")
}

func (s *Server) gateway(w http.ResponseWriter, r *http.Request) {
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	name := r.PathValue("name")
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
	decision, err := s.interactiveAccess(r, name, id, cfg)
	if err != nil || !decision.Allowed {
		s.denyInteractive(w, id, cfg)
		return
	}
	pod, err := s.Workspaces.Get(r.Context(), name)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if pod.Status.Phase != corev1.PodRunning {
		http.Error(w, "workspace is not running", http.StatusConflict)
		return
	}
	if s.Local == nil || s.Forwards == nil {
		http.Error(w, "JetBrains Gateway launch is only available when the portal runs locally", http.StatusNotImplemented)
		return
	}
	port, err := s.Forwards.Ensure(s.Workspaces, name)
	if err != nil {
		http.Error(w, "port-forward: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Local.WriteSSHConfig(name, port); err != nil {
		http.Error(w, "ssh config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Local.WriteWindowsSSHConfig(name, port); err != nil {
		http.Error(w, "windows ssh config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": gatewayURL(local.Alias(name))})
}

func gatewayURL(alias string) string {
	params := url.Values{}
	params.Set("type", "ssh")
	params.Set("host", alias)
	params.Set("port", "22")
	params.Set("user", "dev")
	params.Set("projectPath", "/home/dev")
	return "jetbrains-gateway://connect#" + params.Encode()
}

func (s *Server) launchLocalEditor(w http.ResponseWriter, r *http.Request, editor string) {
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
	decision, err := s.interactiveAccess(r, name, id, cfg)
	if err != nil || !decision.Allowed {
		s.denyInteractive(w, id, cfg)
		return
	}

	pod, err := s.Workspaces.Get(r.Context(), name)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if pod.Status.Phase != corev1.PodRunning {
		http.Error(w, "workspace is not running", http.StatusConflict)
		return
	}
	if s.Local == nil || s.Forwards == nil {
		http.Error(w, "desktop editor launch is only available when the portal runs locally", http.StatusNotImplemented)
		return
	}
	if editor == "cursor" && !s.Local.HasCursor() {
		http.Error(w, "Cursor is not installed on the portal machine", http.StatusNotImplemented)
		return
	}
	if editor != "cursor" && !s.Local.HasVSCode() {
		http.Error(w, "VS Code is not installed on the portal machine", http.StatusNotImplemented)
		return
	}

	port, err := s.Forwards.Ensure(s.Workspaces, name)
	if err != nil {
		http.Error(w, "port-forward: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Local.WriteSSHConfig(name, port); err != nil {
		http.Error(w, "ssh config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Local.WriteWindowsSSHConfig(name, port); err != nil {
		http.Error(w, "windows ssh config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if editor == "cursor" {
		err = s.Local.LaunchCursor(name)
	} else {
		err = s.Local.Launch(name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
