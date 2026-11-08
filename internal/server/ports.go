package server

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
)

const workspaceSSHPort = 22

type workspacePort struct {
	k8s.Port
	URL string `json:"url"`
}

func (s *Server) workspacePorts(w http.ResponseWriter, r *http.Request) {
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
	allowed, err := s.workspaceAllowedAny(r, name, id, cfg)
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
		writeJSON(w, http.StatusOK, map[string]any{
			"ports":   []workspacePort{},
			"message": "Start the workspace to detect listening ports.",
		})
		return
	}

	ports, err := s.Workspaces.ListeningPorts(r.Context(), name)
	if err != nil {
		log.Printf("workspace ports %s: %v", name, err)
		writeJSON(w, http.StatusOK, map[string]any{
			"ports":   []workspacePort{},
			"message": "Could not detect listening ports. Enter a port manually below.",
		})
		return
	}

	out := make([]workspacePort, 0, len(ports))
	for _, port := range ports {
		out = append(out, workspacePort{
			Port: port,
			URL:  workspacePortURL(name, port.Port),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": out})
}

func (s *Server) workspacePortProxy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	if port == workspaceSSHPort {
		http.Error(w, "SSH is not available through the HTTP proxy", http.StatusBadRequest)
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
	if s.Forwards == nil {
		http.Error(w, "port forwarding is unavailable", http.StatusNotImplemented)
		return
	}

	localPort, err := s.Forwards.EnsurePort(s.Workspaces, name, port)
	if err != nil {
		log.Printf("workspace port %s:%d: port-forward: %v", name, port, err)
		http.Error(w, "could not reach the port in the workspace", http.StatusBadGateway)
		return
	}

	prefix := workspacePortPrefix(name, port)
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", localPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.RawPath = ""
		query := req.URL.Query()
		query.Del("token")
		req.URL.RawQuery = query.Encode()
		stripPortalCookies(req.Header)
		if s.Token != "" {
			if token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "); token != "" && tokenEqual(token, s.Token) {
				req.Header.Del("Authorization")
			}
		}
		req.Host = r.Host
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-Prefix", prefix)
		if secureRequest(r) {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		rewriteProxyLocation(resp, prefix)
		rewriteProxyCookies(resp, prefix)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("workspace port %s:%d: proxy: %v", name, port, err)
		http.Error(w, "could not reach the port in the workspace", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) workspacePortRedirect(w http.ResponseWriter, r *http.Request) {
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	target := r.URL.Path + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func workspacePortURL(name string, port int) string {
	return workspacePortPrefix(name, port) + "/"
}

func workspacePortPrefix(name string, port int) string {
	return "/api/workspaces/" + url.PathEscape(name) + "/ports/" + strconv.Itoa(port)
}

func rewriteProxyLocation(resp *http.Response, prefix string) {
	location := resp.Header.Get("Location")
	if location == "" {
		return
	}
	if strings.HasPrefix(location, "//") {
		return
	}
	if strings.HasPrefix(location, "/") {
		if location != prefix && !strings.HasPrefix(location, prefix+"/") {
			resp.Header.Set("Location", prefix+location)
		}
		return
	}

	parsed, err := url.Parse(location)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return
	}
	if parsed.Host != resp.Request.URL.Host && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return
	}
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, prefix+"/") && path != prefix {
		path = prefix + path
	}
	parsed.Scheme = ""
	parsed.Host = ""
	parsed.Path = path
	parsed.RawPath = ""
	resp.Header.Set("Location", parsed.String())
}

func stripPortalCookies(header http.Header) {
	values := header.Values("Cookie")
	if len(values) == 0 {
		return
	}
	header.Del("Cookie")
	for _, value := range values {
		var kept []string
		for _, part := range strings.Split(value, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, _, _ := strings.Cut(part, "=")
			switch strings.TrimSpace(name) {
			case cookieName, sessionCookie, oidcCookie:
				continue
			}
			kept = append(kept, part)
		}
		if len(kept) > 0 {
			header.Add("Cookie", strings.Join(kept, "; "))
		}
	}
}

func rewriteProxyCookies(resp *http.Response, prefix string) {
	values := resp.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	pathPrefix := prefix + "/"
	for _, value := range values {
		cookie, err := http.ParseSetCookie(value)
		if err != nil {
			resp.Header.Add("Set-Cookie", value)
			continue
		}
		cookie.Domain = ""
		switch {
		case cookie.Path == "" || cookie.Path == "/":
			cookie.Path = pathPrefix
		case strings.HasPrefix(cookie.Path, "/") && cookie.Path != prefix && !strings.HasPrefix(cookie.Path, pathPrefix):
			cookie.Path = prefix + cookie.Path
		}
		resp.Header.Add("Set-Cookie", cookie.String())
	}
}
