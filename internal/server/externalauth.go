package server

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"poddy/internal/auth"
	"poddy/internal/externalauth"
	"poddy/internal/metadata"
	"poddy/internal/templates"
)

const (
	externalAuthCookie  = "poddy_external_auth"
	workspaceAuthHeader = "X-Poddy-Workspace-Token"
)

func externalAuthUserKey(user auth.User) string {
	switch {
	case user.Email != "":
		return strings.ToLower(strings.TrimSpace(user.Email))
	case user.Username != "":
		return strings.ToLower(strings.TrimSpace(user.Username))
	default:
		return strings.TrimSpace(user.Subject)
	}
}

func externalAuthOwnerKey(owner string) string {
	return strings.ToLower(strings.TrimSpace(owner))
}

func (s *Server) externalAuthStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.ExternalAuth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "providers": []any{}})
		return
	}
	ids := splitValues(r.URL.Query().Get("ids"))
	statuses, err := s.ExternalAuth.Status(r.Context(), externalAuthUserKey(id.User), ids)
	if err != nil {
		http.Error(w, "external auth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   s.ExternalAuth.Enabled(),
		"providers": statuses,
	})
}

func (s *Server) externalAuthLogin(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	providerID := r.PathValue("id")
	provider, ok := s.ExternalAuth.Provider(providerID)
	if !ok {
		http.Error(w, "unknown external auth provider", http.StatusNotFound)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	redirectURL := s.externalAuthRedirectURL(r, provider)
	authURL, flow, err := s.ExternalAuth.Begin(provider.ID, externalAuthUserKey(id.User), next, redirectURL)
	if err != nil {
		http.Error(w, "external auth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	value, err := s.ExternalAuth.SignFlow(flow)
	if err != nil {
		http.Error(w, "external auth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     externalAuthCookie,
		Value:    value,
		Path:     "/external-auth/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) externalAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	cookie, err := r.Cookie(externalAuthCookie)
	if err != nil {
		s.auditExternalAuth(r, "error", "external auth session expired")
		s.externalAuthError(w, r, "/", "External auth session expired, please try again.")
		return
	}
	flow, err := s.ExternalAuth.ParseFlow(cookie.Value)
	if err != nil {
		s.auditExternalAuth(r, "denied", "invalid external auth state")
		s.externalAuthError(w, r, "/", "Invalid external auth state, please try again.")
		return
	}
	if state := r.URL.Query().Get("state"); state != flow.State {
		s.auditExternalAuth(r, "denied", "invalid external auth state")
		s.externalAuthError(w, r, flow.Next, "Invalid external auth state, please try again.")
		return
	}
	if errCode := r.URL.Query().Get("error"); errCode != "" {
		message := "External auth failed: " + errCode
		if description := r.URL.Query().Get("error_description"); description != "" {
			message += " (" + description + ")"
		}
		s.auditExternalAuth(r, "denied", message)
		s.externalAuthError(w, r, flow.Next, message)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.auditExternalAuth(r, "denied", "missing authorization code")
		s.externalAuthError(w, r, flow.Next, "External auth response has no authorization code.")
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		s.auditExternalAuth(r, "denied", "sign in required")
		s.externalAuthError(w, r, flow.Next, "Sign in again before connecting external auth.")
		return
	}
	userKey := externalAuthUserKey(id.User)
	if flow.User != userKey {
		s.auditExternalAuth(r, "denied", "external auth state belongs to another user")
		s.externalAuthError(w, r, flow.Next, "External auth state belongs to another user.")
		return
	}
	if _, err := s.ExternalAuth.Complete(r.Context(), r.PathValue("id"), userKey, code, flow); err != nil {
		log.Printf("external auth callback: %v", err)
		s.auditExternalAuth(r, "error", err.Error())
		s.externalAuthError(w, r, flow.Next, "External auth failed: "+err.Error())
		return
	}
	s.auditExternalAuth(r, "success", "")
	http.SetCookie(w, &http.Cookie{
		Name:     externalAuthCookie,
		Value:    "",
		Path:     "/external-auth/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.Redirect(w, r, safeNext(flow.Next), http.StatusSeeOther)
}

func (s *Server) auditExternalAuth(r *http.Request, outcome, message string) {
	s.auditEvent(r, metadata.Event{
		Action:     "external_auth.connect",
		Outcome:    outcome,
		TargetType: "provider",
		TargetName: r.PathValue("id"),
		Error:      message,
	})
}

func (s *Server) externalAuthDisconnect(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	providerID := r.PathValue("id")
	if _, ok := s.ExternalAuth.Provider(providerID); !ok {
		http.Error(w, "unknown external auth provider", http.StatusNotFound)
		return
	}
	if err := s.ExternalAuth.Disconnect(r.Context(), externalAuthUserKey(id.User), providerID); err != nil {
		http.Error(w, "external auth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workspaceExternalAuthToken(w http.ResponseWriter, r *http.Request) {
	owner, requirements, ok := s.workspaceAuth(w, r)
	if !ok {
		return
	}
	providerID := r.PathValue("id")
	if _, declared := externalAuthRequirement(requirements, providerID); !declared {
		http.Error(w, "external auth provider is not declared by this template", http.StatusForbidden)
		return
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	token, err := s.ExternalAuth.Token(r.Context(), externalAuthOwnerKey(owner), providerID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, externalauth.ErrReauth) {
			status = http.StatusUnauthorized
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken": token.AccessToken,
		"tokenType":   token.TokenType,
		"expiresAt":   token.Expiry,
	})
}

func (s *Server) workspaceGitAskpass(w http.ResponseWriter, r *http.Request) {
	owner, requirements, ok := s.workspaceAuth(w, r)
	if !ok {
		return
	}
	prompt := r.URL.Query().Get("prompt")
	if prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return
	}
	username, password, err := s.ExternalAuth.GitCredentials(r.Context(), externalAuthOwnerKey(owner), requirements, prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	value := password
	if strings.Contains(strings.ToLower(prompt), "username") {
		value = username
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(value + "\n"))
}

func (s *Server) workspaceAuth(w http.ResponseWriter, r *http.Request) (string, []templates.ExternalAuth, bool) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "workspace is required", http.StatusBadRequest)
		return "", nil, false
	}
	value := r.Header.Get(workspaceAuthHeader)
	if value == "" {
		http.Error(w, "missing workspace token", http.StatusUnauthorized)
		return "", nil, false
	}
	if s.ExternalAuth == nil {
		http.Error(w, "external auth is not configured", http.StatusNotImplemented)
		return "", nil, false
	}
	owner, err := s.ExternalAuth.VerifyWorkspace(value, name)
	if err != nil {
		http.Error(w, "invalid workspace token", http.StatusUnauthorized)
		return "", nil, false
	}
	requirements, err := s.workspaceRequirements(r, name)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return "", nil, false
	}
	return owner, requirements, true
}

func (s *Server) workspaceRequirements(r *http.Request, name string) ([]templates.ExternalAuth, error) {
	if s.Workspaces == nil || s.Workspaces.Templates == nil {
		return nil, errors.New("templates are not configured")
	}
	templateName := ""
	if s.Regions != nil {
		_, info, err := s.Regions.FindWorkspace(r.Context(), name, s.hubAdminIdentity())
		if err != nil {
			return nil, err
		}
		templateName = info.Template
	} else {
		pod, err := s.Workspaces.Get(r.Context(), name)
		if err == nil {
			templateName = pod.Labels["poddy.io/template"]
		} else {
			state, stateErr := s.Workspaces.State(r.Context(), name)
			if stateErr != nil {
				return nil, err
			}
			templateName = state.Template
		}
	}
	tmpl, err := s.Workspaces.Templates.Get(templateName)
	if err != nil {
		return nil, err
	}
	return tmpl.ExternalAuth, nil
}

func externalAuthRequirement(requirements []templates.ExternalAuth, id string) (templates.ExternalAuth, bool) {
	for _, requirement := range requirements {
		if requirement.ID == id {
			return requirement, true
		}
	}
	return templates.ExternalAuth{}, false
}

func (s *Server) externalAuthRedirectURL(r *http.Request, provider *externalauth.Provider) string {
	if provider.RedirectURL != "" {
		return provider.RedirectURL
	}
	base := s.ExternalAuthRedirectBase
	if base == "" {
		base = requestBaseURL(r)
	}
	return strings.TrimRight(base, "/") + "/external-auth/" + url.PathEscape(provider.ID) + "/callback"
}

func (s *Server) externalAuthPortalURL(r *http.Request) string {
	if s.ExternalAuthURL != "" {
		return strings.TrimRight(s.ExternalAuthURL, "/")
	}
	return requestBaseURL(r)
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func (s *Server) externalAuthError(w http.ResponseWriter, r *http.Request, next, message string) {
	target, err := url.Parse(safeNext(next))
	if err != nil {
		target = &url.URL{Path: "/"}
	}
	query := target.Query()
	query.Set("externalAuthError", message)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func splitValues(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func (s *Server) writeExternalAuthRequired(w http.ResponseWriter, r *http.Request, err error) bool {
	var required *externalauth.AuthRequiredError
	if !errors.As(err, &required) {
		return false
	}
	response := map[string]any{
		"error":   required.Error(),
		"missing": required.Missing,
	}
	if id, ok := s.currentIdentity(r); ok && s.ExternalAuth != nil {
		if statuses, statusErr := s.ExternalAuth.Status(r.Context(), externalAuthUserKey(id.User), required.Missing); statusErr == nil {
			response["providers"] = statuses
		}
	}
	writeJSON(w, http.StatusConflict, response)
	return true
}
