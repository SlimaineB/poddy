package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"poddy/internal/k8s"
)

const (
	shareAccessOff  = "off"
	shareCookie     = "poddy_share"
	shareCookieAge  = 12 * 60 * 60
	shareBodyLimit  = 1 << 16
	shareDefaultURL = "/share/"
)

type workspaceShareInfo struct {
	Access    string    `json:"access"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

func (s *Server) shareStore() *k8s.ShareStore {
	return k8s.NewShareStore(s.Workspaces.Client, s.Workspaces.Namespace, "")
}

func (s *Server) shareURL(token string) string {
	if s.RegionMode && s.RegionName != "" {
		return "/share/" + url.PathEscape(s.RegionName) + "/" + url.PathEscape(token)
	}
	return shareDefaultURL + url.PathEscape(token)
}

func workspaceShareTarget(name string, port int, ws k8s.Workspace) string {
	switch {
	case ws.JupyterLab && port == browserJupyterPort:
		return workspaceLabURL(name)
	case ws.BrowserVSCode && port == browserVSCodePort:
		return workspaceIDEPath(name, "/code/")
	default:
		return workspacePortURL(name, port)
	}
}

func (s *Server) shareInfo(share k8s.Share) workspaceShareInfo {
	return workspaceShareInfo{
		Access:    share.Access,
		URL:       s.shareURL(share.Token),
		CreatedAt: share.CreatedAt,
		UpdatedAt: share.UpdatedAt,
	}
}

func (s *Server) workspaceShare(w http.ResponseWriter, r *http.Request) {
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
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	if port == workspaceSSHPort {
		http.Error(w, "SSH is not available through the HTTP proxy", http.StatusBadRequest)
		return
	}
	var input struct {
		Access string `json:"access"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, shareBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	access := strings.ToLower(strings.TrimSpace(input.Access))
	switch access {
	case shareAccessOff, k8s.ShareAccessInternal, k8s.ShareAccessPublic:
	default:
		http.Error(w, "access must be internal, public, or off", http.StatusBadRequest)
		return
	}

	ws, err := s.Workspaces.GetWorkspace(r.Context(), name)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	target := workspaceShareTarget(name, port, ws)
	setAuditTarget(r, name)
	setAuditDetail(r, "port", port)
	setAuditDetail(r, "access", access)

	store := s.shareStore()
	if access == shareAccessOff {
		if err := store.DeleteTarget(r.Context(), name, target); err != nil {
			log.Printf("workspace share %s:%d: delete: %v", name, port, err)
			http.Error(w, "could not update share", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"share": nil})
		return
	}

	existing, ok, err := store.GetByTarget(r.Context(), name, target)
	if err != nil {
		log.Printf("workspace share %s:%d: get: %v", name, port, err)
		http.Error(w, "could not update share", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	token := existing.Token
	createdAt := existing.CreatedAt
	if !ok || token == "" {
		token, err = k8s.NewShareToken()
		if err != nil {
			http.Error(w, "could not create share", http.StatusInternalServerError)
			return
		}
		createdAt = now
	}
	share := k8s.Share{
		Token:     token,
		Workspace: name,
		Port:      port,
		Path:      target,
		Access:    access,
		CreatedBy: ownerID(id.User),
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	if err := store.Set(r.Context(), share); err != nil {
		log.Printf("workspace share %s:%d: save: %v", name, port, err)
		http.Error(w, "could not update share", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": s.shareInfo(share)})
}

func (s *Server) shareLink(w http.ResponseWriter, r *http.Request) {
	if s.Regions != nil {
		http.NotFound(w, r)
		return
	}
	s.handleShareLink(w, r, r.PathValue("token"))
}

func (s *Server) regionShareLink(w http.ResponseWriter, r *http.Request) {
	if s.Regions == nil {
		http.NotFound(w, r)
		return
	}
	regionName := r.PathValue("region")
	cfg, ok := s.Regions.Get(regionName)
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := s.Regions.Client(cfg.Name)
	if client == nil {
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}
	target := r.Clone(r.Context())
	target.URL.Path = "/share/" + url.PathEscape(r.PathValue("token"))
	target.URL.RawPath = ""
	if id, ok := s.identify(r); ok {
		client.Proxy(w, target, s.hubIdentity(id, false))
		return
	}
	client.ProxyAnonymous(w, target)
}

func (s *Server) handleShareLink(w http.ResponseWriter, r *http.Request, token string) {
	share, ok, err := s.shareStore().GetByToken(r.Context(), token)
	if err != nil {
		log.Printf("share load: %v", err)
		http.Error(w, "could not load share", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch share.Access {
	case k8s.ShareAccessPublic:
	case k8s.ShareAccessInternal:
		if _, ok := s.currentIdentity(r); !ok {
			http.Redirect(w, r, s.shareLoginURL(token), http.StatusSeeOther)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     shareCookie,
		Value:    share.Token,
		Path:     share.Path,
		MaxAge:   shareCookieAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.Redirect(w, r, share.Path, http.StatusSeeOther)
}

func (s *Server) shareLoginURL(token string) string {
	next := s.shareURL(token)
	if s.RegionMode && s.HubURL != "" {
		return strings.TrimRight(s.HubURL, "/") + "/login?next=" + url.QueryEscape(next)
	}
	return "/login?next=" + url.QueryEscape(next)
}

func (s *Server) shareAccess(r *http.Request, name, target string) (k8s.Share, bool) {
	cookie, err := r.Cookie(shareCookie)
	if err != nil || cookie.Value == "" {
		return k8s.Share{}, false
	}
	share, ok, err := s.shareStore().GetByToken(r.Context(), cookie.Value)
	if err != nil {
		log.Printf("share access: %v", err)
		return k8s.Share{}, false
	}
	if !ok || share.Workspace != name || share.Path != target {
		return k8s.Share{}, false
	}
	switch share.Access {
	case k8s.ShareAccessPublic:
		return share, true
	case k8s.ShareAccessInternal:
		if _, ok := s.currentIdentity(r); ok {
			return share, true
		}
	}
	return k8s.Share{}, false
}

func hasShareCookie(r *http.Request) bool {
	cookie, err := r.Cookie(shareCookie)
	return err == nil && cookie.Value != ""
}

func isWorkspaceSharePath(path string) bool {
	const prefix = "/api/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(path, prefix), "/", 3)
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "lab", "code":
		return len(parts) == 3
	case "ports":
		return len(parts) == 3 && parts[2] != ""
	default:
		return false
	}
}
