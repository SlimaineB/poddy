package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"

	"poddy/internal/auth"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/local"
	"poddy/internal/metadata"
	"poddy/internal/metrics"
	"poddy/internal/rbac"
	"poddy/internal/sshkeys"
	"poddy/internal/userkeys"
	"poddy/internal/userstore"
)

const cookieName = "poddy_token"

type Server struct {
	Workspaces *k8s.Manager
	WebFS      fs.FS
	Token      string
	TokenAdmin bool
	Local      *local.Local
	Forwards   *Forwarder

	SSHHost           string
	SSHPort           string
	SSHKeyPath        string
	Windows           bool
	WindowsSSHKeyPath string
	SSHEnabled        bool
	SSHKeys           *sshkeys.Dynamic
	SSHAdminKeys      []string
	SSHAllowUnmapped  bool
	UserKeys          *userkeys.Store
	DesktopSecret     []byte

	OIDC            *auth.OIDC
	OIDCRedirectURL string

	RBAC     *rbac.Store
	Users    *userstore.Store
	Metadata metadata.Store
	Metrics  *metrics.Metrics

	ExternalAuth             *externalauth.Manager
	ExternalAuthURL          string
	ExternalAuthRedirectBase string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /terminal", s.terminalPage)
	mux.Handle("GET /static/", noCache(http.StripPrefix("/static/", http.FileServer(http.FS(s.WebFS)))))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /oidc/login", s.oidcLogin)
	mux.HandleFunc("GET /oidc/callback", s.oidcCallback)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("GET /api/me/ssh-keys", s.listSSHKeys)
	mux.HandleFunc("POST /api/me/ssh-keys", s.addSSHKey)
	mux.HandleFunc("DELETE /api/me/ssh-keys/{id}", s.removeSSHKey)
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/templates", s.templates)
	mux.HandleFunc("GET /api/templates/{name}/access", s.templateAccess)
	mux.HandleFunc("PUT /api/templates/{name}/access", s.updateTemplateAccess)
	mux.HandleFunc("GET /api/external-auth", s.externalAuthStatus)
	mux.HandleFunc("POST /api/external-auth/{id}/disconnect", s.externalAuthDisconnect)
	mux.HandleFunc("GET /external-auth/{id}/login", s.externalAuthLogin)
	mux.HandleFunc("GET /external-auth/{id}/callback", s.externalAuthCallback)
	mux.HandleFunc("GET /api/workspaces/{name}/external-auth/{id}/token", s.workspaceExternalAuthToken)
	mux.HandleFunc("GET /api/workspaces/{name}/git-askpass", s.workspaceGitAskpass)
	mux.HandleFunc("GET /api/users", s.users)
	mux.HandleFunc("GET /api/teams", s.teams)
	mux.HandleFunc("PUT /api/teams/{name}", s.updateTeam)
	mux.HandleFunc("DELETE /api/teams/{name}", s.deleteTeam)
	mux.HandleFunc("GET /api/workspaces", s.list)
	mux.HandleFunc("GET /api/topology", s.topology)
	mux.HandleFunc("GET /api/workspaces/{name}", s.workspaceDetails)
	mux.HandleFunc("GET /api/workspaces/{name}/ports", s.workspacePorts)
	mux.HandleFunc("/api/workspaces/{name}/ports/{port}", s.workspacePortRedirect)
	mux.HandleFunc("/api/workspaces/{name}/ports/{port}/", s.workspacePortProxy)
	mux.HandleFunc("GET /api/workspaces/{name}/logs", s.workspaceLogs)
	mux.HandleFunc("POST /api/workspaces", s.create)
	mux.HandleFunc("DELETE /api/workspaces/{name}", s.remove)
	mux.HandleFunc("POST /api/workspaces/{name}/stop", s.stop)
	mux.HandleFunc("POST /api/workspaces/{name}/start", s.start)
	mux.HandleFunc("POST /api/workspaces/{name}/restart", s.restart)
	mux.HandleFunc("POST /api/workspaces/{name}/vscode", s.vscode)
	mux.HandleFunc("POST /api/workspaces/{name}/desktop", s.desktop)
	mux.HandleFunc("POST /api/desktop/enroll", s.desktopEnroll)
	mux.HandleFunc("GET /api/workspaces/{name}/code/", s.browserVSCode)
	mux.HandleFunc("GET /ws", s.terminal)
	return s.metricsMiddleware(s.auth(s.audit(mux)))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, "index.html")
}

func (s *Server) terminalPage(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, "terminal.html")
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ssh":               s.SSHEnabled,
		"sshHost":           s.SSHHost,
		"sshPort":           s.SSHPort,
		"sshKeyPath":        s.SSHKeyPath,
		"windows":           s.Windows,
		"windowsSSHKeyPath": s.WindowsSSHKeyPath,
		"localVSCode":       s.Local != nil,
	})
}

func (s *Server) serveFile(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(s.WebFS, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}
