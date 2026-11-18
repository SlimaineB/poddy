package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"poddy/internal/auth"
	"poddy/internal/secure"
)

const (
	cliCodeTTL       = 2 * time.Minute
	cliTokenTTL      = 7 * 24 * time.Hour
	cliCodeAudience  = "poddy-cli-code"
	cliTokenAudience = "poddy-cli-token"
	cliPKCEMethod    = "S256"
)

type cliCodeClaims struct {
	User                auth.User `json:"user"`
	Expires             int64     `json:"exp"`
	Audience            string    `json:"aud"`
	CodeChallenge       string    `json:"code_challenge,omitempty"`
	CodeChallengeMethod string    `json:"code_challenge_method,omitempty"`
}

type cliTokenClaims struct {
	User     auth.User `json:"user"`
	Expires  int64     `json:"exp"`
	Audience string    `json:"aud"`
}

// cliAuthorize turns a browser session into a short-lived code for a loopback
// CLI callback. It is intentionally limited to loopback redirects so it can
// never be used as an open redirect.
func (s *Server) cliAuthorize(w http.ResponseWriter, r *http.Request) {
	if len(s.CLISecret) == 0 {
		http.Error(w, "CLI login is not configured", http.StatusNotImplemented)
		return
	}
	redirect := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	if !validLoopbackRedirect(redirect) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	challenge := strings.TrimSpace(r.URL.Query().Get("code_challenge"))
	method := strings.TrimSpace(r.URL.Query().Get("code_challenge_method"))
	if !validPKCEValue(challenge) || method != cliPKCEMethod {
		http.Error(w, "invalid code_challenge", http.StatusBadRequest)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	code, err := s.signCLI(cliCodeClaims{
		User:                id.User,
		Expires:             time.Now().Add(cliCodeTTL).Unix(),
		Audience:            cliCodeAudience,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
	})
	if err != nil {
		http.Error(w, "cannot create CLI code", http.StatusInternalServerError)
		return
	}
	target, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	query := target.Query()
	query.Set("code", code)
	if state := strings.TrimSpace(r.URL.Query().Get("state")); state != "" {
		query.Set("state", state)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func (s *Server) cliToken(w http.ResponseWriter, r *http.Request) {
	if len(s.CLISecret) == 0 {
		http.Error(w, "CLI login is not configured", http.StatusNotImplemented)
		return
	}
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	claims, err := s.verifyCLI(req.Code, cliCodeAudience)
	if err != nil {
		http.Error(w, "invalid or expired CLI code", http.StatusUnauthorized)
		return
	}
	if claims.CodeChallengeMethod != cliPKCEMethod || !validPKCEValue(req.CodeVerifier) {
		http.Error(w, "invalid or expired CLI code", http.StatusUnauthorized)
		return
	}
	sum := sha256.Sum256([]byte(req.CodeVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(claims.CodeChallenge)) != 1 {
		http.Error(w, "invalid or expired CLI code", http.StatusUnauthorized)
		return
	}
	token, err := s.signCLI(cliCodeClaims{
		User:     claims.User,
		Expires:  time.Now().Add(cliTokenTTL).Unix(),
		Audience: cliTokenAudience,
	})
	if err != nil {
		http.Error(w, "cannot create CLI token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": time.Now().Add(cliTokenTTL).UTC().Format(time.RFC3339),
		"user":      claims.User.Display(),
	})
}

func (s *Server) signCLI(claims cliCodeClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return secure.Sign(s.CLISecret, payload), nil
}

func (s *Server) verifyCLI(value, audience string) (cliCodeClaims, error) {
	var claims cliCodeClaims
	payload, err := secure.Verify(s.CLISecret, strings.TrimSpace(value))
	if err != nil {
		return claims, err
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, err
	}
	if claims.Audience != audience {
		return claims, errors.New("invalid CLI token audience")
	}
	if claims.Expires < time.Now().Unix() {
		return claims, errors.New("CLI token expired")
	}
	return claims, nil
}

func (s *Server) verifyCLIToken(value string) (cliTokenClaims, error) {
	claims, err := s.verifyCLI(value, cliTokenAudience)
	return cliTokenClaims{
		User:     claims.User,
		Expires:  claims.Expires,
		Audience: claims.Audience,
	}, err
}

func validPKCEValue(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_', r == '~':
		default:
			return false
		}
	}
	return true
}

func validLoopbackRedirect(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.User != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	return strings.HasPrefix(u.Path, "/")
}
