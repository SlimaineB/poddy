package server

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"poddy/internal/auth"
	"poddy/internal/metadata"
	"poddy/internal/region"
)

const (
	sessionCookie = "poddy_session"
	oidcCookie    = "poddy_oidc"
)

type identity struct {
	User   auth.User
	Method string
	Admin  bool
}

func (s *Server) auth(next http.Handler) http.Handler {
	if s.RegionMode {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := s.identify(r); !ok {
				s.auditDenied(r, "auth.denied", "", "")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	if s.Token == "" && s.OIDC == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if id, ok := s.identify(r); ok {
			s.Metrics.AuthAttempt(id.Method, "success")
			if wantsHTML(r) && s.Token != "" {
				if token := r.URL.Query().Get("token"); token != "" && tokenEqual(token, s.Token) {
					http.SetCookie(w, s.cookie(token, secureRequest(r)))
					target := *r.URL
					query := target.Query()
					query.Del("token")
					target.RawQuery = query.Encode()
					http.Redirect(w, r, target.String(), http.StatusSeeOther)
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}

		s.Metrics.AuthAttempt("none", "denied")
		if wantsHTML(r) {
			if s.OIDC != nil {
				http.Redirect(w, r, "/oidc/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			s.serveLogin(w, r.URL.RequestURI(), "", http.StatusUnauthorized)
			return
		}
		s.auditDenied(r, "auth.denied", "", "")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func (s *Server) cookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

func isPublic(path string) bool {
	if path == "/healthz" || path == "/login" || path == "/logout" || path == "/api/desktop/enroll" ||
		strings.HasPrefix(path, "/api/internal/region/") ||
		strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/oidc/") {
		return true
	}
	if strings.HasPrefix(path, "/api/workspaces/") {
		return strings.Contains(path, "/external-auth/") || strings.HasSuffix(path, "/git-askpass")
	}
	return false
}

func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	return isBrowserVSCodePath(r.URL.Path) || isBrowserJupyterPath(r.URL.Path) ||
		(!strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/ws")
}

func isBrowserVSCodePath(path string) bool {
	const prefix = "/api/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return false
	}
	tail := rest[slash:]
	return tail == "/code" || tail == "/code/"
}

func isBrowserJupyterPath(path string) bool {
	const prefix = "/api/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return false
	}
	tail := rest[slash:]
	return tail == "/lab" || tail == "/lab/"
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
}

func (s *Server) identify(r *http.Request) (*identity, bool) {
	if s.RegionMode {
		token := r.Header.Get(region.IdentityHeader)
		if token == "" {
			return nil, false
		}
		claims, err := region.VerifyIdentity(s.RegionKey, token)
		if err != nil {
			return nil, false
		}
		return &identity{Method: "region", User: claims.User, Admin: claims.Admin}, true
	}
	if s.OIDC != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			if user, err := s.OIDC.ParseSession(cookie.Value); err == nil {
				return &identity{Method: "oidc", User: user}, true
			}
		}
	}

	if s.Token != "" {
		if token := r.URL.Query().Get("token"); token != "" && tokenEqual(token, s.Token) {
			return &identity{Method: "token", User: auth.User{Username: "token"}}, true
		}
		if token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); token != "" && tokenEqual(token, s.Token) {
			return &identity{Method: "token", User: auth.User{Username: "token"}}, true
		}
		if cookie, err := r.Cookie(cookieName); err == nil && tokenEqual(cookie.Value, s.Token) {
			return &identity{Method: "token", User: auth.User{Username: "token"}}, true
		}
	}
	return nil, false
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auditEvent(r, metadata.Event{Action: "auth.logout", Outcome: "success"})
	for _, name := range []string{sessionCookie, cookieName, oidcCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secureRequest(r),
		})
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, map[string]any{
		"username":       id.User.Display(),
		"name":           id.User.Name,
		"email":          id.User.Email,
		"groups":         id.User.Groups,
		"method":         id.Method,
		"isAdmin":        s.isAdmin(id, cfg),
		"isAuditor":      s.isAuditor(id, cfg),
		"canViewAudit":   s.canViewAudit(id, cfg),
		"ownedTemplates": cfg.OwnedTemplates(rbacIdentity(id.User)),
	})
}
