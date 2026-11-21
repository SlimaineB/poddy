package server

import (
	"net/http"
	"strings"

	"poddy/internal/mesh"
)

type meshClientConfig struct {
	ControlURL string `json:"controlUrl"`
	LoginURL   string `json:"loginUrl,omitempty"`
	AuthKey    string `json:"authKey"`
	Workspace  string `json:"workspace,omitempty"`
	SSHHost    string `json:"sshHost,omitempty"`
}

func (s *Server) meshClientConfig(workspace string) meshClientConfig {
	controlURL := strings.TrimSpace(s.MeshControlURL)
	if controlURL == "" {
		controlURL = strings.TrimSpace(s.MeshLoginURL)
	}
	out := meshClientConfig{
		ControlURL: controlURL,
		LoginURL:   strings.TrimSpace(s.MeshLoginURL),
		AuthKey:    strings.TrimSpace(s.MeshClientAuthKey),
		Workspace:  workspace,
	}
	if workspace != "" {
		out.SSHHost = mesh.WorkspaceHostname(workspace)
	}
	return out
}

func (s *Server) desktopMesh(w http.ResponseWriter, r *http.Request) {
	if !s.MeshEnabled || s.meshClientConfig("").AuthKey == "" {
		http.Error(w, "mesh access is not configured", http.StatusNotImplemented)
		return
	}
	claims, err := s.verifyDesktopTicket(strings.TrimSpace(r.URL.Query().Get("ticket")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, s.meshClientConfig(claims.Workspace))
}

func (s *Server) cliMesh(w http.ResponseWriter, r *http.Request) {
	if !s.MeshEnabled || s.meshClientConfig("").AuthKey == "" {
		http.Error(w, "mesh access is not configured", http.StatusNotImplemented)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if workspace != "" {
		cfg, err := s.loadRBAC(r)
		if err != nil {
			http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if s.Regions != nil {
			_, _, err := s.Regions.FindWorkspace(r.Context(), workspace, s.hubIdentity(id, s.isAdmin(id, cfg)))
			if err != nil {
				writeRegionError(w, err)
				return
			}
		} else if s.Workspaces != nil {
			decision, err := s.interactiveAccess(r, workspace, id, cfg)
			if err != nil || !decision.Allowed {
				http.Error(w, "workspace not found", http.StatusNotFound)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, s.meshClientConfig(workspace))
}
