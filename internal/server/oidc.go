package server

import (
	"log"
	"net/http"

	"poddy/internal/auth"
	"poddy/internal/userstore"
)

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	next := safeNext(r.URL.Query().Get("next"))
	redirectURL := auth.RedirectURL(r, s.OIDCRedirectURL)
	authURL, flow, err := s.OIDC.BeginURL(next, redirectURL)
	if err != nil {
		http.Error(w, "oidc: "+err.Error(), http.StatusInternalServerError)
		return
	}

	value, err := s.OIDC.SignFlow(flow)
	if err != nil {
		http.Error(w, "oidc: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if errCode := r.URL.Query().Get("error"); errCode != "" {
		description := r.URL.Query().Get("error_description")
		message := "SSO error: " + errCode
		if description != "" {
			message += " (" + description + ")"
		}
		s.serveLogin(w, "/", message, http.StatusUnauthorized)
		return
	}

	cookie, err := r.Cookie(oidcCookie)
	if err != nil {
		s.serveLogin(w, "/", "SSO session expired, please try again.", http.StatusUnauthorized)
		return
	}
	flow, err := s.OIDC.ParseFlow(cookie.Value)
	if err != nil {
		s.serveLogin(w, "/", "Invalid SSO state, please try again.", http.StatusUnauthorized)
		return
	}
	if state := r.URL.Query().Get("state"); state != flow.State {
		s.serveLogin(w, "/", "Invalid SSO state, please try again.", http.StatusUnauthorized)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.serveLogin(w, "/", "SSO response has no authorization code.", http.StatusUnauthorized)
		return
	}

	user, err := s.OIDC.Complete(r.Context(), code, flow)
	if err != nil {
		log.Printf("oidc callback: %v", err)
		s.serveLogin(w, flow.Next, "SSO sign-in failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	if s.Users != nil {
		if _, err := s.Users.Upsert(r.Context(), userstore.User{
			Subject:  user.Subject,
			Username: user.Username,
			Email:    user.Email,
			Name:     user.Name,
			Groups:   user.Groups,
		}); err != nil {
			log.Printf("user directory: %v", err)
		}
	}

	value, err := s.OIDC.SignSession(user)
	if err != nil {
		http.Error(w, "oidc: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(s.OIDC.SessionTTL().Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	http.Redirect(w, r, safeNext(flow.Next), http.StatusSeeOther)
}
