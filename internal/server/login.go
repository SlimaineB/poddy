package server

import (
	"html/template"
	"net/http"
)

type loginData struct {
	Next         string
	Error        string
	OIDC         bool
	TokenEnabled bool
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.serveLogin(w, "/", "", http.StatusOK)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	next := safeNext(r.FormValue("next"))
	if s.Token == "" {
		s.serveLogin(w, next, "Token sign-in is disabled.", http.StatusUnauthorized)
		return
	}
	token := r.FormValue("token")
	if !tokenEqual(token, s.Token) {
		s.serveLogin(w, next, "Invalid token", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, s.cookie(token, secureRequest(r)))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) serveLogin(w http.ResponseWriter, next, message string, status int) {
	tpl, err := template.ParseFS(s.WebFS, "login.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	tpl.Execute(w, loginData{
		Next:         next,
		Error:        message,
		OIDC:         s.OIDC != nil,
		TokenEnabled: s.Token != "",
	})
}
