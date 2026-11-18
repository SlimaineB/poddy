package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poddy/internal/auth"
)

const (
	testPKCEVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	testPKCEChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func TestValidLoopbackRedirect(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:4567/callback",
		"http://localhost:1/callback",
		"http://[::1]:8080/callback",
	}
	for _, value := range valid {
		if !validLoopbackRedirect(value) {
			t.Errorf("validLoopbackRedirect(%q) = false", value)
		}
	}
	invalid := []string{
		"https://127.0.0.1:4567/callback",
		"http://example.com:4567/callback",
		"http://127.0.0.1/callback",
		"http://127.0.0.1:0/callback",
		"http://user@127.0.0.1:4567/callback",
		"",
	}
	for _, value := range invalid {
		if validLoopbackRedirect(value) {
			t.Errorf("validLoopbackRedirect(%q) = true", value)
		}
	}
}

func TestCLITokenRoundTrip(t *testing.T) {
	s := &Server{CLISecret: []byte("cli-secret")}
	user := auth.User{Username: "alice", Email: "alice@example.com"}
	code, err := s.signCLI(cliCodeClaims{
		User:     user,
		Expires:  time.Now().Add(time.Minute).Unix(),
		Audience: cliCodeAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.verifyCLI(code, cliCodeAudience)
	if err != nil {
		t.Fatal(err)
	}
	if claims.User.Username != "alice" {
		t.Fatalf("user = %+v", claims.User)
	}
	if _, err := s.verifyCLI(code, cliTokenAudience); err == nil {
		t.Fatal("code accepted as a token")
	}

	token, err := s.signCLI(cliCodeClaims{
		User:     user,
		Expires:  time.Now().Add(time.Minute).Unix(),
		Audience: cliTokenAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyCLIToken(token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	id, ok := s.identify(req)
	if !ok || id.Method != "cli" || id.User.Username != "alice" {
		t.Fatalf("identify = %+v, %v", id, ok)
	}
}

func TestCLIAuthorizeRedirectsToLoopback(t *testing.T) {
	s := &Server{CLISecret: []byte("cli-secret")}
	req := httptest.NewRequest("GET", "/api/cli/authorize?redirect_uri="+
		"http%3A%2F%2F127.0.0.1%3A4321%2Fcallback&state=abc"+
		"&code_challenge="+testPKCEChallenge+"&code_challenge_method=S256", nil)
	rec := httptest.NewRecorder()
	s.cliAuthorize(rec, req)
	if rec.Code != 303 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "http://127.0.0.1:4321/callback?") {
		t.Fatalf("location = %q", location)
	}
	if !strings.Contains(location, "state=abc") || !strings.Contains(location, "code=") {
		t.Fatalf("location = %q", location)
	}
}

func TestCLITokenEndpoint(t *testing.T) {
	s := &Server{CLISecret: []byte("cli-secret")}
	code, err := s.signCLI(cliCodeClaims{
		User:                auth.User{Username: "alice"},
		Expires:             time.Now().Add(time.Minute).Unix(),
		Audience:            cliCodeAudience,
		CodeChallenge:       testPKCEChallenge,
		CodeChallengeMethod: cliPKCEMethod,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": testPKCEVerifier})
	req := httptest.NewRequest("POST", "/api/cli/token", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	s.cliToken(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyCLIToken(response.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCLITokenEndpointRejectsWrongVerifier(t *testing.T) {
	s := &Server{CLISecret: []byte("cli-secret")}
	code, err := s.signCLI(cliCodeClaims{
		User:                auth.User{Username: "alice"},
		Expires:             time.Now().Add(time.Minute).Unix(),
		Audience:            cliCodeAudience,
		CodeChallenge:       testPKCEChallenge,
		CodeChallengeMethod: cliPKCEMethod,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": "wrong-verifier-that-is-long-enough-1234567890"})
	req := httptest.NewRequest("POST", "/api/cli/token", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	s.cliToken(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidPKCEValue(t *testing.T) {
	if !validPKCEValue(testPKCEVerifier) {
		t.Fatal("valid verifier rejected")
	}
	for _, value := range []string{"", "short", testPKCEVerifier + "!", strings.Repeat("a", 129)} {
		if validPKCEValue(value) {
			t.Fatalf("invalid verifier %q accepted", value)
		}
	}
}
